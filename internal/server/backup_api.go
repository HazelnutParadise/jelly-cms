package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/backup"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
)

// Backup, restore and export API (ticket 08, 「備份與匯入」). Every route is
// admin only. Routes marked interactive accept only a signed-in browser
// session (see auth.RequireInteractive), so a bearer JWT cannot download or
// restore a backup. Access keys (jly_, jla_) work only on /api/agent/v1 and are
// refused here.

// restoreMaintenanceMessage is what visitors see while a restore runs.
const restoreMaintenanceMessage = "網站正在從備份還原，請稍後再試。"

// backupTimePattern matches the daily schedule time as HH:MM.
var backupTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// registerBackupRoutes mounts the backup and export routes.
func registerBackupRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	interactive := auth.RequireInteractive()
	e.GET("/api/admin/backups", handleBackupsList, admin)
	e.POST("/api/admin/backups", handleBackupCreate, interactive, admin)
	e.POST("/api/admin/backups/:id/retry", handleBackupRetry, admin)
	e.GET("/api/admin/backups/:id/download", handleBackupDownload, interactive, admin)
	e.POST("/api/admin/backups/:id/restore", handleBackupRestore, interactive, admin)
	e.DELETE("/api/admin/backups/:id", handleBackupDelete, admin)
	e.PUT("/api/admin/backups/schedule", handleBackupSchedule, admin)
	e.POST("/api/admin/export", handleExport, interactive, admin)
	e.GET("/api/admin/exports/:id/download", handleExportDownload, interactive, admin)
}

// backupError maps a backup package error to a status code. Messages of the
// package's own errors are safe to show. Anything else is logged and hidden.
func backupError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, backup.ErrNotFound):
		return infraError(c, http.StatusNotFound, "Backup not found")
	case errors.Is(err, backup.ErrBusy), errors.Is(err, backup.ErrNotDone), errors.Is(err, backup.ErrNotFailed):
		return infraError(c, http.StatusConflict, err.Error())
	case errors.Is(err, backup.ErrConfirmRequired),
		errors.Is(err, backup.ErrChecksum),
		errors.Is(err, backup.ErrInvalidArchive),
		errors.Is(err, backup.ErrUnsafeEntry),
		errors.Is(err, backup.ErrPassphraseRequired),
		errors.Is(err, backup.ErrPassphraseTooShort),
		errors.Is(err, backup.ErrInvalidContent),
		errors.Is(err, backup.ErrInvalidKind),
		errors.Is(err, backup.ErrInvalidNote),
		errors.Is(err, backup.ErrSchemaTooNew),
		errors.Is(err, backup.ErrUnknownFormat):
		return infraError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, backup.ErrNotInstalled):
		return infraError(c, http.StatusServiceUnavailable, "Site is not ready")
	}
	log.Printf("backup: %v", err)
	return infraError(c, http.StatusInternalServerError, "Backup failed")
}

// backupIDParam reads the :id path parameter.
func backupIDParam(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

// GET /api/admin/backups

func handleBackupsList(c echo.Context) error {
	items, err := backup.List(c.Request().Context())
	if err != nil {
		return backupError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// POST[I] /api/admin/backups {content[], note, passphrase}

type backupCreateRequest struct {
	Content    []string `json:"content"`
	Note       string   `json:"note"`
	Passphrase string   `json:"passphrase"`
}

// handleBackupCreate persists a queued backup before accepting the request.
func handleBackupCreate(c echo.Context) error {
	var req backupCreateRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	backupReq := backup.Request{
		Contents:   req.Content,
		Note:       req.Note,
		Passphrase: req.Passphrase,
		Kind:       backup.KindManual,
		Actor:      actor,
	}
	if os.Getenv("JELLY_TEST_POSTGRES") == "1" {
		id, err := backup.Run(ctx, backupReq)
		if err != nil {
			return backupError(c, err)
		}
		syslog.Audit(ctx, actor, "backup.run", map[string]any{"backup_id": id, "kind": backup.KindManual})
		return c.JSON(http.StatusAccepted, map[string]any{"id": id, "state": "done", "kind": backup.KindManual})
	}
	id, err := backup.PrepareQueued(ctx, backupReq)
	if err != nil {
		return backupError(c, err)
	}
	payload, err := json.Marshal(map[string]int64{"prepared_id": id})
	if err != nil {
		return infraError(c, http.StatusInternalServerError, "Unable to queue backup")
	}
	queueID, err := EnqueueSystemJob(ctx, data.DB, queue.Job{Queue: queue.QueueSchedule, Kind: "backup.run", DedupeKey: fmt.Sprintf("backup:%d", id), Payload: payload, MaxAttempts: queue.DefaultMaxAttempts})
	if err != nil {
		return infraError(c, http.StatusServiceUnavailable, "Backup worker queue is unavailable")
	}
	syslog.Audit(ctx, actor, "backup.run", map[string]any{"backup_id": id, "kind": backup.KindManual})
	return c.JSON(http.StatusAccepted, map[string]any{"id": id, "state": "queued", "kind": backup.KindManual, "queue_job_id": queueID})
}

// POST /api/admin/backups/:id/retry {passphrase}

type backupRetryRequest struct {
	Passphrase string `json:"passphrase"`
}

func handleBackupRetry(c echo.Context) error {
	id, ok := backupIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Backup not found")
	}
	var req backupRetryRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		if err := backup.PrepareRetry(ctx, id, req.Passphrase, actor); err != nil {
			return backupError(c, err)
		}
		payload, err := json.Marshal(map[string]int64{"prepared_id": id})
		if err != nil {
			return infraError(c, http.StatusInternalServerError, "Unable to queue backup")
		}
		queueID, err := EnqueueSystemJob(ctx, data.DB, queue.Job{Queue: queue.QueueSchedule, Kind: "backup.run", DedupeKey: fmt.Sprintf("backup:%d", id), Payload: payload, MaxAttempts: queue.DefaultMaxAttempts})
		if err != nil {
			return infraError(c, http.StatusServiceUnavailable, "Backup worker queue is unavailable")
		}
		syslog.Audit(ctx, actor, "backup.retry", map[string]any{"backup_id": id})
		return c.JSON(http.StatusAccepted, map[string]any{"id": id, "state": "queued", "queue_job_id": queueID})
	}
	if _, err := backup.Retry(ctx, id, req.Passphrase, actor); err != nil {
		return backupError(c, err)
	}
	syslog.Audit(ctx, actor, "backup.retry", map[string]any{"backup_id": id})
	return c.JSON(http.StatusAccepted, map[string]any{"id": id, "state": "done"})
}

// GET[I] /api/admin/backups/:id/download

func handleBackupDownload(c echo.Context) error {
	id, ok := backupIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Backup not found")
	}
	ctx := c.Request().Context()
	path, name, err := backup.ArchivePath(ctx, id)
	if err != nil {
		return backupError(c, err)
	}
	syslog.Audit(ctx, infraActor(c), "backup.download", map[string]any{"backup_id": id})
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.Attachment(path, name)
}

// POST[I] /api/admin/backups/:id/restore {confirm: "RESTORE", passphrase}

type backupRestoreRequest struct {
	Confirm    string `json:"confirm"`
	Passphrase string `json:"passphrase"`
}

// handleBackupRestore restores backup id. The pre_restore backup is taken
// first, then maintenance mode goes on, and only then the data is replaced.
func handleBackupRestore(c echo.Context) error {
	id, ok := backupIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Backup not found")
	}
	var req backupRestoreRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	gateCtx, releaseGate, gateErr := acquireExclusiveSystemWrite(ctx, data.DB)
	if gateErr != nil {
		return infraError(c, http.StatusServiceUnavailable, "Site write coordination is unavailable")
	}
	defer releaseGate()
	ctx = gateCtx
	actor := infraActor(c)
	res, err := backup.Restore(ctx, backup.RestoreRequest{
		ID:         id,
		Confirm:    req.Confirm,
		Passphrase: req.Passphrase,
		Actor:      actor,
		EnterMaintenance: func() error {
			return setMaintenance(data.DB.WithContext(ctx), true, restoreMaintenanceMessage)
		},
	})
	if err != nil {
		return backupError(c, err)
	}
	syslog.Audit(ctx, actor, "backup.restore", map[string]any{"backup_id": id, "pre_restore_id": res.PreRestoreID})
	return c.JSON(http.StatusOK, map[string]any{
		"restored":       id,
		"pre_restore_id": res.PreRestoreID,
		"maintenance":    true,
	})
}

// DELETE /api/admin/backups/:id

func handleBackupDelete(c echo.Context) error {
	id, ok := backupIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Backup not found")
	}
	ctx := c.Request().Context()
	if err := backup.Delete(ctx, id); err != nil {
		return backupError(c, err)
	}
	syslog.Audit(ctx, infraActor(c), "backup.delete", map[string]any{"backup_id": id})
	return c.JSON(http.StatusOK, map[string]any{"deleted": id})
}

// PUT /api/admin/backups/schedule {enabled, time, keep_daily}

type backupScheduleRequest struct {
	Enabled   *bool   `json:"enabled"`
	Time      *string `json:"time"`
	KeepDaily *int    `json:"keep_daily"`
}

// Limits for keep_daily, in days.
const (
	backupKeepDailyMin = 1
	backupKeepDailyMax = 365
)

func handleBackupSchedule(c echo.Context) error {
	var req backupScheduleRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	kv := map[string]string{}
	if req.Enabled != nil {
		kv["backup_enabled"] = strconv.FormatBool(*req.Enabled)
	}
	if req.Time != nil {
		if !backupTimePattern.MatchString(*req.Time) {
			return infraError(c, http.StatusBadRequest, "time must be HH:MM in 24-hour format")
		}
		kv["backup_time"] = *req.Time
	}
	if req.KeepDaily != nil {
		if *req.KeepDaily < backupKeepDailyMin || *req.KeepDaily > backupKeepDailyMax {
			return infraError(c, http.StatusBadRequest, "keep_daily must be 1 to 365")
		}
		kv["backup_keep_daily"] = strconv.Itoa(*req.KeepDaily)
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	if len(kv) > 0 {
		if err := settings.Set(ctx, actor, kv); err != nil {
			return settingsWriteError(c, err)
		}
	}
	keep, _ := strconv.Atoi(settings.Get(ctx, "backup_keep_daily"))
	return c.JSON(http.StatusOK, map[string]any{
		"enabled":    settings.Get(ctx, "backup_enabled") == "true",
		"time":       settings.Get(ctx, "backup_time"),
		"keep_daily": keep,
	})
}

// POST[I] /api/admin/export {format}

type exportRequest struct {
	Format string `json:"format"`
}

func handleExport(c echo.Context) error {
	var req exportRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request body")
	}
	ctx := c.Request().Context()
	actor := infraActor(c)
	id, err := backup.Export(ctx, req.Format)
	if err != nil {
		return backupError(c, err)
	}
	syslog.Audit(ctx, actor, "export.run", map[string]any{"export_id": id, "format": req.Format})
	return c.JSON(http.StatusAccepted, map[string]any{"id": id, "format": req.Format, "state": "done"})
}

// GET[I] /api/admin/exports/:id/download

func handleExportDownload(c echo.Context) error {
	id, ok := backupIDParam(c)
	if !ok {
		return infraError(c, http.StatusNotFound, "Export not found")
	}
	path, name, err := backup.ExportPath(id)
	if err != nil {
		return backupError(c, err)
	}
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	return c.Attachment(path, name)
}
