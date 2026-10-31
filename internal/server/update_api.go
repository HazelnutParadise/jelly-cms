package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
	"github.com/HazelnutParadise/jelly-cms/internal/version"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// Update API (ticket 08, 「更新」). Every route is admin only. Apply and rollback
// are interactive only (see auth.RequireInteractive). A container,
// Kubernetes or package-managed runtime answers 409 with its kind and shows the
// upgrade commands instead.

const (
	keyUpdateChannel  = "update_channel"
	keyUpdateAutoChk  = "update_auto_check"
	keyUpdateOnNew    = "update_on_new"
	keyUpdateAutoTime = "update_auto_time"
	keyUpdateKeep     = "update_keep_versions"

	updateImage = "ghcr.io/hazelnutparadise/jelly-cms"
)

func registerUpdateRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/update", handleUpdateStatus, admin)
	e.POST("/api/admin/update/check", handleUpdateCheck, admin)
	e.POST("/api/admin/update/apply", handleUpdateApply, auth.RequireInteractive(), admin)
	e.POST("/api/admin/update/cancel", handleUpdateCancel, admin)
	e.POST("/api/admin/update/rollback", handleUpdateRollback, auth.RequireInteractive(), admin)
	e.PUT("/api/admin/update/settings", handleUpdateSettings, admin)
}

// updateSettingsView is the schedule and channel part of the status response.
func updateSettingsView(c echo.Context) map[string]any {
	ctx := c.Request().Context()
	keep := 0
	if n, err := strconv.Atoi(settings.Get(ctx, keyUpdateKeep)); err == nil {
		keep = n
	}
	return map[string]any{
		"channel":       settings.Get(ctx, keyUpdateChannel),
		"auto_check":    settings.Get(ctx, keyUpdateAutoChk) == "true",
		"on_new":        settings.Get(ctx, keyUpdateOnNew),
		"auto_time":     settings.Get(ctx, keyUpdateAutoTime),
		"keep_versions": keep,
	}
}

// updateCommands returns the upgrade steps for a runtime that cannot replace
// its own executable. The standalone binary gets no commands.
func updateCommands(kind, latest string) []string {
	tag := latest
	if tag == "" {
		tag = "latest"
	}
	switch kind {
	case version.KindContainer:
		return []string{"docker pull " + updateImage + ":" + tag, "docker compose up -d"}
	case version.KindKubernetes:
		return []string{"kubectl set image deployment/jelly-cms jelly-cms=" + updateImage + ":" + tag}
	case version.KindPackage:
		return []string{"brew upgrade jelly-cms"}
	}
	return nil
}

// GET /api/admin/update

func handleUpdateStatus(c echo.Context) error {
	st, err := update.LoadState(update.StatePath)
	if err != nil {
		log.Printf("update: load state: %v", err)
		return infraError(c, http.StatusInternalServerError, "Update state is unreadable")
	}
	rt := version.Runtime()
	var checked any
	if !st.CheckedAt.IsZero() {
		checked = st.CheckedAt.Format(time.RFC3339)
	}
	body := map[string]any{
		"current":          version.Version,
		"latest":           st.Latest,
		"checked_at":       checked,
		"update_available": update.IsNewer(st.Latest, version.Version),
		"runtime": map[string]any{
			"kind":        rt.Kind,
			"detected_by": rt.DetectedBy,
			"writable":    rt.Writable,
		},
		"can_update": rt.Kind == version.KindBinary && rt.Writable,
		"commands":   updateCommands(rt.Kind, st.Latest),
		"state": map[string]any{
			"status":       st.Status,
			"previous":     st.Previous,
			"failed_boots": st.FailedBoots,
			"last_error":   st.LastError,
		},
	}
	for k, v := range updateSettingsView(c) {
		body[k] = v
	}
	return c.JSON(http.StatusOK, body)
}

// POST /api/admin/update/check

func handleUpdateCheck(c echo.Context) error {
	ctx := c.Request().Context()
	channel := settings.Get(ctx, keyUpdateChannel)
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		payload, err := json.Marshal(map[string]any{"manual": true, "channel": channel})
		if err != nil {
			return infraError(c, http.StatusInternalServerError, "Unable to queue update check")
		}
		id, err := EnqueueSystemJob(ctx, data.DB, queue.Job{
			Queue: queue.QueueSchedule, Kind: "update.check", Payload: payload, MaxAttempts: queue.DefaultMaxAttempts,
		})
		if err != nil {
			return infraError(c, http.StatusServiceUnavailable, "Update worker queue is unavailable")
		}
		syslog.Audit(ctx, infraActor(c), "update.check", map[string]any{"state": "queued", "channel": channel, "job_id": id})
		return c.JSON(http.StatusAccepted, map[string]any{"state": "queued", "job_id": id, "channel": channel})
	}
	rel, err := update.Check(ctx, channel)
	if err != nil {
		log.Printf("update: check: %v", err)
		if errors.Is(err, update.ErrNoRelease) {
			return infraError(c, http.StatusNotFound, "No release on this channel")
		}
		return infraError(c, http.StatusBadGateway, "Update check failed")
	}
	syslog.Audit(ctx, infraActor(c), "update.check", map[string]any{"latest": rel.Version, "channel": channel})
	return c.JSON(http.StatusOK, map[string]any{
		"latest":           rel.Version,
		"channel":          channel,
		"update_available": update.IsNewer(rel.Version, version.Version),
	})
}

// POST[I] /api/admin/update/apply {version}

type updateApplyRequest struct {
	Version string `json:"version"`
}

func handleUpdateApply(c echo.Context) error {
	var req updateApplyRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	target := strings.TrimSpace(req.Version)
	if err := update.ValidVersion(target); err != nil {
		return infraError(c, http.StatusBadRequest, "version must be a semantic version such as v1.2.0")
	}
	if err := updateRuntimeGuard(); err != nil {
		return updateError(c, err)
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		payload, err := json.Marshal(map[string]any{"version": target, "expected_version": version.Version, "actor": actor})
		if err != nil {
			return infraError(c, http.StatusInternalServerError, "Unable to queue update")
		}
		id, err := EnqueueSystemJob(ctx, data.DB, queue.Job{Queue: queue.QueueSchedule, Kind: "update.apply", DedupeKey: "update.apply:" + target, Payload: payload, MaxAttempts: queue.DefaultMaxAttempts})
		if err != nil {
			return infraError(c, http.StatusServiceUnavailable, "Update worker queue is unavailable")
		}
		syslog.Audit(ctx, actor, "update.apply", map[string]any{"version": target, "state": "queued", "job_id": id})
		return c.JSON(http.StatusAccepted, map[string]any{"version": target, "state": "queued", "restart": true, "job_id": id})
	}
	ctx = withUpdatePreBackup(ctx, actor, uuid.NewString(), target)
	if err := update.Apply(ctx, target); err != nil {
		return updateError(c, err)
	}
	syslog.Audit(ctx, actor, "update.apply", map[string]any{"version": target})
	restart := !update.TestSeamsActive()
	if restart {
		// The response is written before the server shuts down for the restart.
		update.RequestRestart()
	}
	return c.JSON(http.StatusAccepted, map[string]any{"version": target, "state": update.StatusApplied, "restart": restart})
}

// POST /api/admin/update/cancel

func handleUpdateCancel(c echo.Context) error {
	if !update.Cancel() {
		return infraError(c, http.StatusConflict, "No update is running")
	}
	syslog.Audit(c.Request().Context(), infraActor(c), "update.cancel", nil)
	return c.JSON(http.StatusOK, map[string]any{"cancelled": true})
}

// POST[I] /api/admin/update/rollback

func handleUpdateRollback(c echo.Context) error {
	if err := updateRuntimeGuard(); err != nil {
		return updateError(c, err)
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		payload, err := json.Marshal(map[string]any{"expected_version": version.Version, "actor": actor})
		if err != nil {
			return infraError(c, http.StatusInternalServerError, "Unable to queue rollback")
		}
		id, err := EnqueueSystemJob(ctx, data.DB, queue.Job{Queue: queue.QueueSchedule, Kind: "update.rollback", DedupeKey: "update.rollback:" + version.Version, Payload: payload, MaxAttempts: queue.DefaultMaxAttempts})
		if err != nil {
			return infraError(c, http.StatusServiceUnavailable, "Update worker queue is unavailable")
		}
		syslog.Audit(ctx, actor, "update.rollback", map[string]any{"state": "queued", "job_id": id})
		return c.JSON(http.StatusAccepted, map[string]any{"state": "queued", "restart": true, "job_id": id})
	}
	if err := update.Rollback(ctx); err != nil {
		return updateError(c, err)
	}
	syslog.Audit(ctx, actor, "update.rollback", nil)
	restart := !update.TestSeamsActive()
	if restart {
		update.RequestRestart()
	}
	return c.JSON(http.StatusAccepted, map[string]any{"state": update.StatusRolledBack, "restart": restart})
}

// updateRuntimeGuard rejects runtimes that cannot replace their executable
// before either the synchronous path or the queued worker path can start.
func updateRuntimeGuard() error {
	rt := version.Runtime()
	if rt.Kind != version.KindBinary {
		return &update.RuntimeError{Kind: rt.Kind}
	}
	if !rt.Writable {
		return update.ErrNotWritable
	}
	return nil
}

// PUT /api/admin/update/settings {channel, auto_check, on_new, auto_time, keep_versions}

type updateSettingsRequest struct {
	Channel      *string `json:"channel"`
	AutoCheck    *bool   `json:"auto_check"`
	OnNew        *string `json:"on_new"`
	AutoTime     *string `json:"auto_time"`
	KeepVersions *int    `json:"keep_versions"`
}

func handleUpdateSettings(c echo.Context) error {
	var req updateSettingsRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	kv := map[string]string{}
	if req.Channel != nil {
		if *req.Channel != update.ChannelStable && *req.Channel != update.ChannelPreview {
			return infraError(c, http.StatusBadRequest, "channel must be stable or preview")
		}
		kv[keyUpdateChannel] = *req.Channel
	}
	if req.AutoCheck != nil {
		kv[keyUpdateAutoChk] = strconv.FormatBool(*req.AutoCheck)
	}
	if req.OnNew != nil {
		if *req.OnNew != "notify" && *req.OnNew != "off" {
			return infraError(c, http.StatusBadRequest, "on_new must be notify or off")
		}
		kv[keyUpdateOnNew] = *req.OnNew
	}
	if req.AutoTime != nil {
		if !backupTimePattern.MatchString(*req.AutoTime) {
			return infraError(c, http.StatusBadRequest, "auto_time must be HH:MM in 24-hour format")
		}
		kv[keyUpdateAutoTime] = *req.AutoTime
	}
	if req.KeepVersions != nil {
		if *req.KeepVersions < 1 || *req.KeepVersions > 10 {
			return infraError(c, http.StatusBadRequest, "keep_versions must be 1 to 10")
		}
		kv[keyUpdateKeep] = strconv.Itoa(*req.KeepVersions)
	}
	ctx := c.Request().Context()
	if len(kv) > 0 {
		if err := settings.Set(ctx, infraActor(c), kv); err != nil {
			return settingsWriteError(c, err)
		}
	}
	return c.JSON(http.StatusOK, updateSettingsView(c))
}

// updateError maps an update package error to a response.
func updateError(c echo.Context, err error) error {
	var rte *update.RuntimeError
	switch {
	case errors.As(err, &rte):
		return c.JSON(http.StatusConflict, map[string]string{
			"error":   "This runtime cannot replace its own executable. Use the upgrade commands instead.",
			"runtime": rte.Kind,
		})
	case errors.Is(err, update.ErrNotWritable):
		return c.JSON(http.StatusConflict, map[string]string{
			"error":   "The executable directory is not writable",
			"runtime": version.KindBinary,
		})
	case errors.Is(err, update.ErrNotNewer):
		return infraError(c, http.StatusConflict, "Target version is not newer. Use rollback to go back.")
	case errors.Is(err, update.ErrBusy):
		return infraError(c, http.StatusConflict, "Another update operation is running")
	case errors.Is(err, update.ErrNoPrevious):
		return infraError(c, http.StatusConflict, "No previous version to roll back to")
	case errors.Is(err, update.ErrNoRelease):
		return infraError(c, http.StatusNotFound, "No release matches that version")
	case errors.Is(err, update.ErrNoSigningKey):
		return infraError(c, http.StatusServiceUnavailable, "Update signing key is not configured")
	case errors.Is(err, update.ErrHealthcheck):
		return infraError(c, http.StatusUnprocessableEntity, "The new version failed its healthcheck. The previous version was restored.")
	case errors.Is(err, update.ErrVerification), errors.Is(err, update.ErrArchive):
		return infraError(c, http.StatusUnprocessableEntity, "The release failed verification and was not installed")
	}
	log.Printf("update: %v", err)
	return infraError(c, http.StatusBadGateway, "Update failed")
}
