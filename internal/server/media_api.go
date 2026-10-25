package server

// Attachment API (docs/tickets/08-system.md, 08d): storage policies, groups,
// files and remote fetch. Bytes go through internal/storage. A local file is
// public at /uploads/<path>, which uploads.go serves. Policy, group and fetch
// endpoints are admin only. File endpoints also accept editors and authors,
// as the legacy /api/admin/media routes do.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // registers the GIF decoder for DecodeConfig
	_ "image/jpeg" // registers the JPEG decoder for DecodeConfig
	_ "image/png"  // registers the PNG decoder for DecodeConfig
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

const (
	mediaDefaultMaxBytes    int64 = 20 << 20
	mediaPurposeMedia             = "media"
	mediaFetchMaxURLs             = 10
	mediaFetchTimeout             = 30 * time.Second
	mediaNameMaxBytes             = 200
	mediaAltMaxRunes              = 500
	mediaGroupDescMaxRunes        = 500
	mediaFolderLayout             = "2006/01"
	mediaCollisionLimit           = 1000
	mediaSniffBytes               = 4096
	mediaListDefaultPerPage       = 50
	mediaListMaxPerPage           = 200
	mediaDefaultPolicyName        = "本機"
)

var (
	errMediaDriverUnavailable = errors.New("media driver is not available")
	errMediaInvalidImage      = errors.New("media image is malformed")
	mediaPluginDriver         = regexp.MustCompile(`^plugin:[a-z0-9][a-z0-9._-]{0,79}$`)
	mediaReservedName         = regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$`)
	mediaLikeEscaper          = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
)

// registerMediaRoutes mounts the attachment API and runs the startup steps
// that need the database: the default local policy and the legacy import of
// data/uploads. Tests reach these through RegisterRoutes.
func registerMediaRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	staff := auth.RequireRole("admin", "editor", "author")

	e.GET("/api/admin/media/policies", handleMediaPolicyList, admin)
	e.POST("/api/admin/media/policies", handleMediaPolicyCreate, admin)
	e.PUT("/api/admin/media/policies/:id", handleMediaPolicyUpdate, admin)
	e.DELETE("/api/admin/media/policies/:id", handleMediaPolicyDelete, admin)
	e.POST("/api/admin/media/policies/:id/test", handleMediaPolicyTest, admin)
	e.POST("/api/admin/media/policies/:id/default", handleMediaPolicyDefault, admin)
	e.POST("/api/admin/media/policies/:id/move", handleMediaPolicyMove, admin)

	e.GET("/api/admin/media/groups", handleMediaGroupList, staff)
	e.POST("/api/admin/media/groups", handleMediaGroupCreate, admin)
	e.PUT("/api/admin/media/groups/:id", handleMediaGroupUpdate, admin)
	e.DELETE("/api/admin/media/groups/:id", handleMediaGroupDelete, admin)

	e.GET("/api/admin/media/files", handleMediaFileList, staff)
	e.POST("/api/admin/media/files", handleMediaFileUpload, staff, middleware.BodyLimit(uploadBodyLimit))
	e.PATCH("/api/admin/media/files/:id", handleMediaFileUpdate, staff)
	e.DELETE("/api/admin/media/files/:id", handleMediaFileTrash, staff)
	e.POST("/api/admin/media/files/:id/replace", handleMediaFileReplace, staff, middleware.BodyLimit(uploadBodyLimit))
	e.POST("/api/admin/media/fetch", handleMediaFetch, admin)

	ctx := context.Background()
	if err := ensureDefaultMediaPolicy(ctx); err != nil {
		log.Printf("media: ensure default policy: %v", err)
	}
	if n, err := importLegacyUploads(ctx); err != nil {
		log.Printf("media: legacy import: %v", err)
	} else if n > 0 {
		log.Printf("media: imported %d legacy upload(s) into the default policy", n)
	}
}

// ---- rows ----

type mediaPolicyRow struct {
	ID        int64
	Name      string
	Driver    string
	Purpose   string
	Config    string
	SecretEnc []byte
	IsDefault bool
	DeletedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type mediaGroupRow struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type mediaFileRow struct {
	ID           int64
	PolicyID     int64
	GroupID      *int64
	OwnerID      *int64
	Path         string
	Filename     string
	Mime         string
	Size         int64
	Width        *int
	Height       *int
	Alt          string
	Sha256       string
	Source       string
	OriginalOfID *int64
	ExifStripped bool
	DeletedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// mediaFailure is a client-facing error. Handlers and helpers return it so the
// status, code and message stay together.
type mediaFailure struct {
	Status int
	Code   string
	Msg    string
}

func (f *mediaFailure) send(c echo.Context) error {
	return c.JSON(f.Status, map[string]any{"code": f.Code, "error": f.Msg})
}

func mediaDB(ctx context.Context) *gorm.DB {
	return data.DB.WithContext(ctx)
}

func mediaServerError(c echo.Context, op string, err error) error {
	log.Printf("media: %s: %v", op, err)
	return c.JSON(http.StatusInternalServerError, map[string]any{"code": "internal", "error": "Internal error"})
}

func mediaIDParam(c echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

// ---- settings ----

func mediaMaxBytes(ctx context.Context) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(settings.Get(ctx, "media_max_bytes")), 10, 64)
	if err != nil || n <= 0 {
		return mediaDefaultMaxBytes
	}
	return n
}

func mediaStripEXIF(ctx context.Context) bool {
	return strings.TrimSpace(settings.Get(ctx, "media_strip_exif")) != "false"
}

// mediaAllowedTypes returns the admin's extra allowlist. An empty list means
// the built-in types in detectMediaType apply unchanged.
func mediaAllowedTypes(ctx context.Context) []string {
	var list []string
	if err := json.Unmarshal([]byte(settings.Get(ctx, "media_allowed_types")), &list); err != nil {
		return nil
	}
	return list
}

// ---- policies ----

// mediaPolicyJSON is the policy as the admin screen reads it. The secret is
// never returned, only whether one is stored.
func mediaPolicyJSON(p mediaPolicyRow, files int64) map[string]any {
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(p.Config), &cfg)
	return map[string]any{
		"id":         p.ID,
		"name":       p.Name,
		"driver":     p.Driver,
		"purpose":    p.Purpose,
		"config":     cfg,
		"has_secret": len(p.SecretEnc) > 0,
		"is_default": p.IsDefault,
		"file_count": files,
		"created_at": p.CreatedAt,
		"updated_at": p.UpdatedAt,
	}
}

type mediaPolicyInput struct {
	Name    *string        `json:"name"`
	Driver  *string        `json:"driver"`
	Purpose *string        `json:"purpose"`
	Config  map[string]any `json:"config"`
	Secret  *string        `json:"secret"`
}

func loadMediaPolicy(ctx context.Context, id int64) (mediaPolicyRow, error) {
	var row mediaPolicyRow
	err := mediaDB(ctx).Table("media_policies").Where("id = ? AND deleted_at IS NULL", id).Take(&row).Error
	return row, err
}

func defaultMediaPolicy(ctx context.Context) (mediaPolicyRow, error) {
	var row mediaPolicyRow
	err := mediaDB(ctx).Table("media_policies").
		Where("purpose = ? AND is_default = TRUE AND deleted_at IS NULL", mediaPurposeMedia).
		Take(&row).Error
	return row, err
}

// mediaFileCounts returns how many file rows each policy holds, trashed rows
// included, because the foreign key blocks deleting a policy while they exist.
func mediaFileCounts(ctx context.Context) (map[int64]int64, error) {
	var rows []struct {
		PolicyID int64
		N        int64
	}
	if err := mediaDB(ctx).Raw("SELECT policy_id, COUNT(*) AS n FROM media_files GROUP BY policy_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.PolicyID] = r.N
	}
	return out, nil
}

// loadPolicyDrivers maps every policy id, deleted ones included, to its driver,
// so file rows can be given a public address.
func loadPolicyDrivers(ctx context.Context) (map[int64]string, error) {
	var rows []struct {
		ID     int64
		Driver string
	}
	if err := mediaDB(ctx).Table("media_policies").Select("id, driver").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Driver
	}
	return out, nil
}

func validatePolicyFields(name, driver, purpose string) *mediaFailure {
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return &mediaFailure{http.StatusBadRequest, "invalid_name", "name must be 1 to 64 characters"}
	}
	if driver != storage.DriverLocal && driver != storage.DriverS3 && !mediaPluginDriver.MatchString(driver) {
		return &mediaFailure{http.StatusBadRequest, "invalid_driver", "driver must be local, s3 or plugin:<id>"}
	}
	if purpose != "media" && purpose != "backup" {
		return &mediaFailure{http.StatusBadRequest, "invalid_purpose", "purpose must be media or backup"}
	}
	return nil
}

// mediaPolicyConfig keeps only the keys a driver reads. Local policies store no
// config because their root is fixed to uploadsRoot.
func mediaPolicyConfig(driver string, cfg map[string]any) (string, *mediaFailure) {
	if driver != storage.DriverS3 {
		return "{}", nil
	}
	out := map[string]string{}
	for _, key := range []string{"endpoint", "bucket", "region", "prefix"} {
		if v, ok := cfg[key].(string); ok {
			out[key] = strings.TrimSpace(v)
		}
	}
	if out["bucket"] == "" {
		return "", &mediaFailure{http.StatusBadRequest, "invalid_config", "bucket is required for s3"}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", &mediaFailure{http.StatusBadRequest, "invalid_config", "config is invalid"}
	}
	return string(b), nil
}

func handleMediaPolicyList(c echo.Context) error {
	ctx := c.Request().Context()
	var rows []mediaPolicyRow
	if err := mediaDB(ctx).Table("media_policies").Where("deleted_at IS NULL").Order("id").Find(&rows).Error; err != nil {
		return mediaServerError(c, "list policies", err)
	}
	counts, err := mediaFileCounts(ctx)
	if err != nil {
		return mediaServerError(c, "count files", err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, mediaPolicyJSON(r, counts[r.ID]))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func handleMediaPolicyCreate(c echo.Context) error {
	ctx := c.Request().Context()
	var in mediaPolicyInput
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_json", "error": "Invalid request"})
	}
	name, driver, purpose := "", "", mediaPurposeMedia
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if in.Driver != nil {
		driver = strings.TrimSpace(*in.Driver)
	}
	if in.Purpose != nil && *in.Purpose != "" {
		purpose = *in.Purpose
	}
	if fail := validatePolicyFields(name, driver, purpose); fail != nil {
		return fail.send(c)
	}
	cfg, fail := mediaPolicyConfig(driver, in.Config)
	if fail != nil {
		return fail.send(c)
	}
	row := mediaPolicyRow{Name: name, Driver: driver, Purpose: purpose, Config: cfg}
	if in.Secret != nil && *in.Secret != "" {
		sealed, err := settings.SealBytes("media-policy", []byte(*in.Secret))
		if err != nil {
			return mediaServerError(c, "seal policy secret", err)
		}
		row.SecretEnc = sealed
	}
	if err := mediaDB(ctx).Table("media_policies").Create(&row).Error; err != nil {
		return mediaServerError(c, "create policy", err)
	}
	syslog.Audit(ctx, infraActor(c), "media.policy.create", map[string]any{"policy_id": row.ID, "driver": driver, "purpose": purpose})
	return c.JSON(http.StatusCreated, mediaPolicyJSON(row, 0))
}

func handleMediaPolicyUpdate(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Policy not found"})
	}
	row, err := loadMediaPolicy(ctx, id)
	if err != nil {
		return policyLoadError(c, err)
	}
	var in mediaPolicyInput
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_json", "error": "Invalid request"})
	}
	if in.Driver != nil && *in.Driver != row.Driver {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "driver_change", "error": "driver cannot change; move the files to another policy"})
	}
	if in.Purpose != nil && *in.Purpose != row.Purpose {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "purpose_change", "error": "purpose cannot change"})
	}
	updates := map[string]any{"updated_at": time.Now()}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if fail := validatePolicyFields(name, row.Driver, row.Purpose); fail != nil {
			return fail.send(c)
		}
		updates["name"] = name
	}
	if in.Config != nil {
		cfg, fail := mediaPolicyConfig(row.Driver, in.Config)
		if fail != nil {
			return fail.send(c)
		}
		updates["config"] = cfg
	}
	if in.Secret != nil {
		if *in.Secret == "" {
			updates["secret_enc"] = nil
		} else {
			sealed, err := settings.SealBytes("media-policy", []byte(*in.Secret))
			if err != nil {
				return mediaServerError(c, "seal policy secret", err)
			}
			updates["secret_enc"] = sealed
		}
	}
	if err := mediaDB(ctx).Table("media_policies").Where("id = ? AND deleted_at IS NULL", id).Updates(updates).Error; err != nil {
		return mediaServerError(c, "update policy", err)
	}
	row, err = loadMediaPolicy(ctx, id)
	if err != nil {
		return policyLoadError(c, err)
	}
	counts, err := mediaFileCounts(ctx)
	if err != nil {
		return mediaServerError(c, "count files", err)
	}
	syslog.Audit(ctx, infraActor(c), "media.policy.update", map[string]any{"policy_id": id})
	return c.JSON(http.StatusOK, mediaPolicyJSON(row, counts[id]))
}

func policyLoadError(c echo.Context, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Policy not found"})
	}
	return mediaServerError(c, "load policy", err)
}

func handleMediaPolicyDelete(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Policy not found"})
	}
	row, err := loadMediaPolicy(ctx, id)
	if err != nil {
		return policyLoadError(c, err)
	}
	if row.IsDefault {
		return c.JSON(http.StatusConflict, map[string]any{"code": "policy_is_default", "error": "Set another default policy before deleting this one"})
	}
	counts, err := mediaFileCounts(ctx)
	if err != nil {
		return mediaServerError(c, "count files", err)
	}
	if counts[id] > 0 {
		return c.JSON(http.StatusConflict, map[string]any{"code": "policy_in_use", "error": "Move or delete the files in this policy first"})
	}
	now := time.Now()
	if err := mediaDB(ctx).Table("media_policies").Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
		return mediaServerError(c, "delete policy", err)
	}
	syslog.Audit(ctx, infraActor(c), "media.policy.delete", map[string]any{"policy_id": id})
	return c.JSON(http.StatusOK, map[string]any{"deleted": true})
}

// handleMediaPolicyTest reports the driver check as data. A driver that is
// off or unreachable answers 200 with ok false, so the screen can show why.
func handleMediaPolicyTest(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Policy not found"})
	}
	row, err := loadMediaPolicy(ctx, id)
	if err != nil {
		return policyLoadError(c, err)
	}
	drv, err := mediaDriver(row)
	if err == nil {
		err = drv.Test(ctx)
	}
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func handleMediaPolicyDefault(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Policy not found"})
	}
	row, err := loadMediaPolicy(ctx, id)
	if err != nil {
		return policyLoadError(c, err)
	}
	now := time.Now()
	err = mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("media_policies").Where("purpose = ? AND is_default = TRUE AND deleted_at IS NULL", row.Purpose).
			Updates(map[string]any{"is_default": false, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Table("media_policies").Where("id = ?", id).
			Updates(map[string]any{"is_default": true, "updated_at": now}).Error
	})
	if err != nil {
		return mediaServerError(c, "set default policy", err)
	}
	syslog.Audit(ctx, infraActor(c), "media.policy.default", map[string]any{"policy_id": id})
	row.IsDefault = true
	counts, err := mediaFileCounts(ctx)
	if err != nil {
		return mediaServerError(c, "count files", err)
	}
	return c.JSON(http.StatusOK, mediaPolicyJSON(row, counts[id]))
}

// handleMediaPolicyMove moves every file row of a policy to another policy of
// the same purpose. Only local to local moves are supported for now: both
// policies share the uploads root, so the bytes and public addresses stay put.
// Moves across drivers need the S3 driver or a plugin driver, and content
// references would have to be rewritten too.
func handleMediaPolicyMove(c echo.Context) error {
	ctx := c.Request().Context()
	from, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Policy not found"})
	}
	var in struct {
		ToPolicyID int64 `json:"to_policy_id"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil || in.ToPolicyID <= 0 {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_request", "error": "to_policy_id is required"})
	}
	if in.ToPolicyID == from {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "same_policy", "error": "Choose a different target policy"})
	}
	src, err := loadMediaPolicy(ctx, from)
	if err != nil {
		return policyLoadError(c, err)
	}
	dst, err := loadMediaPolicy(ctx, in.ToPolicyID)
	if err != nil {
		return policyLoadError(c, err)
	}
	if src.Purpose != dst.Purpose {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "purpose_mismatch", "error": "Both policies must have the same purpose"})
	}
	if src.Driver != storage.DriverLocal || dst.Driver != storage.DriverLocal {
		return c.JSON(http.StatusConflict, map[string]any{"code": "driver_unavailable", "error": "Only moves between local policies are supported"})
	}

	var moved int64
	err = mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		var clashes int64
		if err := tx.Raw(`SELECT COUNT(*) FROM media_files f JOIN media_files t ON t.path = f.path AND t.policy_id = ?
			WHERE f.policy_id = ?`, dst.ID, src.ID).Scan(&clashes).Error; err != nil {
			return err
		}
		if clashes > 0 {
			return errMediaPathClash
		}
		res := tx.Table("media_files").Where("policy_id = ?", src.ID).
			Updates(map[string]any{"policy_id": dst.ID, "updated_at": time.Now()})
		if res.Error != nil {
			return res.Error
		}
		moved = res.RowsAffected
		return nil
	})
	if errors.Is(err, errMediaPathClash) {
		return c.JSON(http.StatusConflict, map[string]any{"code": "path_conflict", "error": "The target policy already has files with the same paths"})
	}
	if err != nil {
		return mediaServerError(c, "move policy files", err)
	}
	syslog.Audit(ctx, infraActor(c), "media.policy.move", map[string]any{"from_policy_id": src.ID, "to_policy_id": dst.ID, "count": moved})
	return c.JSON(http.StatusOK, map[string]any{"moved": moved, "from_policy_id": src.ID, "to_policy_id": dst.ID})
}

var errMediaPathClash = errors.New("media: path clash")

// mediaDriver returns the storage driver for a policy. Plugin drivers are
// registered on the media.storage extension point with the id plugin:<id>.
func mediaDriver(p mediaPolicyRow) (storage.Driver, error) {
	switch {
	case p.Driver == storage.DriverLocal:
		return storage.NewLocal(uploadsRoot), nil
	case p.Driver == storage.DriverS3:
		var cfg map[string]string
		_ = json.Unmarshal([]byte(p.Config), &cfg)
		return storage.NewS3(cfg["endpoint"], cfg["bucket"]), nil
	case mediaPluginDriver.MatchString(p.Driver):
		for _, prov := range ext.Resolve(ext.PointMediaStorage) {
			if "plugin:"+prov.ID != p.Driver {
				continue
			}
			if drv, ok := prov.Provider.(storage.Driver); ok {
				return drv, nil
			}
		}
	}
	return nil, errMediaDriverUnavailable
}

// mediaPublicURL is the address a file is served at. Only the local driver
// has one today. Other drivers return "" until they serve their own URLs.
func mediaPublicURL(driver, p string) string {
	if driver == storage.DriverLocal {
		return storage.NewLocal(uploadsRoot).URL(p)
	}
	return ""
}

// ---- groups ----

func handleMediaGroupList(c echo.Context) error {
	ctx := c.Request().Context()
	var rows []mediaGroupRow
	if err := mediaDB(ctx).Table("media_groups").Order("name").Find(&rows).Error; err != nil {
		return mediaServerError(c, "list groups", err)
	}
	var counts []struct {
		GroupID int64
		N       int64
	}
	if err := mediaDB(ctx).Raw(`SELECT group_id, COUNT(*) AS n FROM media_files
		WHERE group_id IS NOT NULL AND deleted_at IS NULL GROUP BY group_id`).Scan(&counts).Error; err != nil {
		return mediaServerError(c, "count group files", err)
	}
	byGroup := map[int64]int64{}
	for _, r := range counts {
		byGroup[r.GroupID] = r.N
	}
	items := make([]map[string]any, 0, len(rows))
	for _, g := range rows {
		items = append(items, map[string]any{"id": g.ID, "name": g.Name, "description": g.Description, "file_count": byGroup[g.ID]})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type mediaGroupInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// validateGroupName checks length and that no other group uses the name. The
// database also enforces uniqueness, so a race still fails on save.
func validateGroupName(ctx context.Context, name string, exceptID int64) *mediaFailure {
	if n := utf8.RuneCountInString(name); n < 1 || n > 128 {
		return &mediaFailure{http.StatusBadRequest, "invalid_name", "name must be 1 to 128 characters"}
	}
	var n int64
	if err := mediaDB(ctx).Table("media_groups").Where("name = ? AND id <> ?", name, exceptID).Count(&n).Error; err != nil {
		return &mediaFailure{http.StatusInternalServerError, "internal", "Internal error"}
	}
	if n > 0 {
		return &mediaFailure{http.StatusConflict, "group_exists", "A group with this name already exists"}
	}
	return nil
}

func handleMediaGroupCreate(c echo.Context) error {
	ctx := c.Request().Context()
	var in mediaGroupInput
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil || in.Name == nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_request", "error": "name is required"})
	}
	name := strings.TrimSpace(*in.Name)
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	if utf8.RuneCountInString(desc) > mediaGroupDescMaxRunes {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_description", "error": "description is too long"})
	}
	if fail := validateGroupName(ctx, name, 0); fail != nil {
		return fail.send(c)
	}
	row := mediaGroupRow{Name: name, Description: desc}
	if err := mediaDB(ctx).Table("media_groups").Create(&row).Error; err != nil {
		return mediaServerError(c, "create group", err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"id": row.ID, "name": row.Name, "description": row.Description, "file_count": 0})
}

func handleMediaGroupUpdate(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Group not found"})
	}
	var exists int64
	if err := mediaDB(ctx).Table("media_groups").Where("id = ?", id).Count(&exists).Error; err != nil {
		return mediaServerError(c, "load group", err)
	}
	if exists == 0 {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Group not found"})
	}
	var in mediaGroupInput
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_json", "error": "Invalid request"})
	}
	updates := map[string]any{"updated_at": time.Now()}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if fail := validateGroupName(ctx, name, id); fail != nil {
			return fail.send(c)
		}
		updates["name"] = name
	}
	if in.Description != nil {
		desc := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(desc) > mediaGroupDescMaxRunes {
			return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_description", "error": "description is too long"})
		}
		updates["description"] = desc
	}
	if err := mediaDB(ctx).Table("media_groups").Where("id = ?", id).Updates(updates).Error; err != nil {
		return mediaServerError(c, "update group", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"id": id, "updated": true})
}

// handleMediaGroupDelete removes a group. ?files=ungroup (the default) keeps
// its files outside any group. ?files=trash moves them to the trash.
func handleMediaGroupDelete(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Group not found"})
	}
	mode := c.QueryParam("files")
	if mode == "" {
		mode = "ungroup"
	}
	if mode != "ungroup" && mode != "trash" {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_mode", "error": "files must be ungroup or trash"})
	}
	now := time.Now()
	var deleted int64
	err := mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		if mode == "trash" {
			if err := tx.Table("media_files").Where("group_id = ? AND deleted_at IS NULL", id).
				Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Table("media_files").Where("group_id = ?", id).
				Updates(map[string]any{"group_id": nil, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		res := tx.Exec("DELETE FROM media_groups WHERE id = ?", id)
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return nil
	})
	if err != nil {
		return mediaServerError(c, "delete group", err)
	}
	if deleted == 0 {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "Group not found"})
	}
	syslog.Audit(ctx, infraActor(c), "media.group.delete", map[string]any{"group_id": id, "files": mode})
	return c.JSON(http.StatusOK, map[string]any{"deleted": true, "files": mode})
}

// ---- files ----

func mediaFileJSON(f mediaFileRow, driver string) map[string]any {
	return map[string]any{
		"id":            f.ID,
		"policy_id":     f.PolicyID,
		"group_id":      f.GroupID,
		"filename":      f.Filename,
		"path":          f.Path,
		"url":           mediaPublicURL(driver, f.Path),
		"mime":          f.Mime,
		"size":          f.Size,
		"width":         f.Width,
		"height":        f.Height,
		"alt":           f.Alt,
		"sha256":        f.Sha256,
		"source":        f.Source,
		"exif_stripped": f.ExifStripped,
		"trashed":       f.DeletedAt != nil,
		"created_at":    f.CreatedAt,
	}
}

func loadMediaFile(ctx context.Context, id int64) (mediaFileRow, error) {
	var row mediaFileRow
	err := mediaDB(ctx).Table("media_files").Where("id = ? AND deleted_at IS NULL", id).Take(&row).Error
	return row, err
}

func fileLoadError(c echo.Context, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "File not found"})
	}
	return mediaServerError(c, "load file", err)
}

// handleMediaFileList filters by policy, group, file name and trash state.
// trashed=1 lists only trashed files. group_id=none lists the ungrouped ones.
func handleMediaFileList(c echo.Context) error {
	ctx := c.Request().Context()
	apply := func(tx *gorm.DB) (*gorm.DB, *mediaFailure) {
		tx = tx.Table("media_files")
		if c.QueryParam("trashed") == "1" {
			tx = tx.Where("deleted_at IS NOT NULL")
		} else {
			tx = tx.Where("deleted_at IS NULL")
		}
		if v := c.QueryParam("policy_id"); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return tx, &mediaFailure{http.StatusBadRequest, "invalid_policy", "policy_id must be a number"}
			}
			tx = tx.Where("policy_id = ?", id)
		}
		switch v := c.QueryParam("group_id"); v {
		case "":
		case "none":
			tx = tx.Where("group_id IS NULL")
		default:
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return tx, &mediaFailure{http.StatusBadRequest, "invalid_group", "group_id must be a number or none"}
			}
			tx = tx.Where("group_id = ?", id)
		}
		if q := strings.TrimSpace(c.QueryParam("q")); q != "" {
			tx = tx.Where(`filename ILIKE ? ESCAPE '\'`, "%"+mediaLikeEscaper.Replace(q)+"%")
		}
		return tx, nil
	}
	page, perPage := 1, mediaListDefaultPerPage
	if n, err := strconv.Atoi(c.QueryParam("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(c.QueryParam("per_page")); err == nil && n > 0 {
		perPage = min(n, mediaListMaxPerPage)
	}
	countQ, fail := apply(mediaDB(ctx))
	if fail != nil {
		return fail.send(c)
	}
	var total int64
	if err := countQ.Count(&total).Error; err != nil {
		return mediaServerError(c, "count files", err)
	}
	listQ, _ := apply(mediaDB(ctx))
	var rows []mediaFileRow
	if err := listQ.Order("id DESC").Limit(perPage).Offset((page - 1) * perPage).Find(&rows).Error; err != nil {
		return mediaServerError(c, "list files", err)
	}
	drivers, err := loadPolicyDrivers(ctx)
	if err != nil {
		return mediaServerError(c, "load policies", err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, f := range rows {
		items = append(items, mediaFileJSON(f, drivers[f.PolicyID]))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "per_page": perPage})
}

// readMediaUpload reads the multipart field "file" and enforces the size limit
// before and while reading, so a lying header cannot exceed it.
func readMediaUpload(ctx context.Context, c echo.Context) (string, []byte, *mediaFailure) {
	header, err := c.FormFile("file")
	if err != nil {
		return "", nil, &mediaFailure{http.StatusBadRequest, "file_required", "file is required"}
	}
	limit := mediaMaxBytes(ctx)
	tooLarge := &mediaFailure{http.StatusRequestEntityTooLarge, "file_too_large", "file exceeds the size limit"}
	if header.Size > limit {
		return "", nil, tooLarge
	}
	src, err := header.Open()
	if err != nil {
		return "", nil, &mediaFailure{http.StatusBadRequest, "unreadable", "file cannot be read"}
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, limit+1))
	if err != nil {
		return "", nil, &mediaFailure{http.StatusBadRequest, "unreadable", "file cannot be read"}
	}
	if int64(len(data)) > limit {
		return "", nil, tooLarge
	}
	return header.Filename, data, nil
}

// mediaPrepared is an upload after validation and metadata removal, ready to
// store.
type mediaPrepared struct {
	Name         string
	Mime         string
	Data         []byte
	Sha256       string
	Width        *int
	Height       *int
	ExifStripped bool
}

// prepareMedia sanitizes the name, checks the type against the content and the
// admin's allowlist, removes JPEG metadata when enabled, and reads dimensions.
func prepareMedia(ctx context.Context, rawName string, data []byte) (mediaPrepared, *mediaFailure) {
	name := sanitizeFilename(rawName)
	mime, ok := detectMediaType(name, data)
	if !ok {
		return mediaPrepared{}, &mediaFailure{http.StatusUnsupportedMediaType, "unsupported_type", "file type is not allowed"}
	}
	if allowed := mediaAllowedTypes(ctx); len(allowed) > 0 && !slices.Contains(allowed, mime) {
		return mediaPrepared{}, &mediaFailure{http.StatusUnsupportedMediaType, "unsupported_type", "file type is not allowed"}
	}
	out := mediaPrepared{Name: name, Mime: mime, Data: data}
	if mime == "image/jpeg" && mediaStripEXIF(ctx) {
		stripped, flagged, err := stripJPEGMetadata(data)
		if err != nil {
			return mediaPrepared{}, &mediaFailure{http.StatusUnsupportedMediaType, "invalid_image", "image is malformed"}
		}
		out.Data, out.ExifStripped = stripped, flagged
	}
	if strings.HasPrefix(mime, "image/") {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(out.Data)); err == nil {
			w, h := cfg.Width, cfg.Height
			out.Width, out.Height = &w, &h
		}
	}
	out.Sha256 = mediaSHA256(out.Data)
	return out, nil
}

// mediaSHA256 returns the hex digest of the stored bytes.
func mediaSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// detectMediaType returns the MIME type for a file whose stored name ends in an
// allowed extension and whose bytes match that type. The extension alone is
// never trusted.
func detectMediaType(name string, data []byte) (string, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png", bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case ".jpg", ".jpeg":
		return "image/jpeg", bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF})
	case ".gif":
		return "image/gif", bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
	case ".webp":
		return "image/webp", len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	case ".avif":
		return "image/avif", len(data) >= 12 && string(data[4:8]) == "ftyp" && (string(data[8:12]) == "avif" || string(data[8:12]) == "avis")
	case ".svg":
		head := data[:min(len(data), mediaSniffBytes)]
		return "image/svg+xml", strings.Contains(strings.ToLower(string(head)), "<svg")
	case ".pdf":
		return "application/pdf", bytes.HasPrefix(data, []byte("%PDF-"))
	}
	return "", false
}

// stripJPEGMetadata drops every APPn segment except JFIF (APP0), ICC colour
// profile (APP2) and Adobe colour flags (APP14). That removes Exif, GPS, XMP
// and IPTC data. The second result reports whether anything was dropped.
func stripJPEGMetadata(data []byte) ([]byte, bool, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, false, errMediaInvalidImage
	}
	out := []byte{0xFF, 0xD8}
	stripped := false
	pos := 2
	for {
		if pos+1 >= len(data) || data[pos] != 0xFF {
			return nil, false, errMediaInvalidImage
		}
		marker := data[pos+1]
		switch {
		case marker == 0xFF:
			pos++ // fill byte
			continue
		case marker == 0xD9: // EOI
			return append(out, 0xFF, 0xD9), stripped, nil
		case marker == 0xDA: // SOS: entropy-coded data runs to the end
			return append(out, data[pos:]...), stripped, nil
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7): // standalone markers
			out = append(out, 0xFF, marker)
			pos += 2
			continue
		}
		if pos+4 > len(data) {
			return nil, false, errMediaInvalidImage
		}
		segLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		end := pos + 2 + segLen
		if segLen < 2 || end > len(data) {
			return nil, false, errMediaInvalidImage
		}
		isAPP := marker >= 0xE0 && marker <= 0xEF
		keep := !isAPP || marker == 0xE0 || marker == 0xE2 || marker == 0xEE
		if keep {
			out = append(out, data[pos:end]...)
		} else {
			stripped = true
		}
		pos = end
	}
}

// sanitizeFilename keeps only the last path element and removes control,
// format and bidirectional characters. Spaces become hyphens and characters
// that are unsafe in paths become underscores. Windows device names and leading
// dots are neutralised, and the name is capped at mediaNameMaxBytes.
func sanitizeFilename(raw string) string {
	name := raw
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = norm.NFC.String(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError:
			continue
		case unicode.IsSpace(r):
			b.WriteByte('-')
		case strings.ContainsRune(`:*?"<>|#%`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimLeft(b.String(), ".")
	ext := strings.ToLower(path.Ext(name))
	if len(ext) > 16 {
		ext = ""
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	if stem == "" {
		stem = "file"
	}
	if mediaReservedName.MatchString(strings.ToUpper(stem)) {
		stem = "_" + stem
	}
	for len(stem)+len(ext) > mediaNameMaxBytes {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	return stem + ext
}

// mediaStoreInput is one file to save with its metadata.
type mediaStoreInput struct {
	Policy   mediaPolicyRow
	Prepared mediaPrepared
	Source   string
	GroupID  *int64
	Alt      string
	Actor    uint
}

// storeMediaFile writes the bytes through the policy's driver and records the
// row. A failed insert removes the bytes again.
func storeMediaFile(ctx context.Context, in mediaStoreInput) (mediaFileRow, error) {
	drv, err := mediaDriver(in.Policy)
	if err != nil {
		return mediaFileRow{}, err
	}
	p, err := freeMediaPath(ctx, drv, in.Policy.ID, in.Prepared.Name)
	if err != nil {
		return mediaFileRow{}, err
	}
	if err := drv.Put(ctx, p, bytes.NewReader(in.Prepared.Data), int64(len(in.Prepared.Data)), in.Prepared.Mime); err != nil {
		return mediaFileRow{}, err
	}
	row := mediaFileRow{
		PolicyID:     in.Policy.ID,
		GroupID:      in.GroupID,
		OwnerID:      actorPtr(in.Actor),
		Path:         p,
		Filename:     in.Prepared.Name,
		Mime:         in.Prepared.Mime,
		Size:         int64(len(in.Prepared.Data)),
		Width:        in.Prepared.Width,
		Height:       in.Prepared.Height,
		Alt:          in.Alt,
		Sha256:       in.Prepared.Sha256,
		Source:       in.Source,
		ExifStripped: in.Prepared.ExifStripped,
	}
	if err := mediaDB(ctx).Table("media_files").Create(&row).Error; err != nil {
		if rmErr := drv.Delete(ctx, p); rmErr != nil {
			log.Printf("media: remove %s after failed insert: %v", p, rmErr)
		}
		return mediaFileRow{}, err
	}
	return row, nil
}

func actorPtr(id uint) *int64 {
	if id == 0 {
		return nil
	}
	v := int64(id)
	return &v
}

// freeMediaPath picks YYYY/MM/<name> and adds -2, -3 and so on when the name is
// taken in the policy or already exists on disk, so no file is overwritten.
func freeMediaPath(ctx context.Context, drv storage.Driver, policyID int64, name string) (string, error) {
	folder := time.Now().UTC().Format(mediaFolderLayout)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n <= mediaCollisionLimit; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		p := folder + "/" + candidate
		var taken int64
		if err := mediaDB(ctx).Raw("SELECT COUNT(*) FROM media_files WHERE policy_id = ? AND path = ?", policyID, p).Scan(&taken).Error; err != nil {
			return "", err
		}
		if taken > 0 {
			continue
		}
		if _, err := drv.Stat(ctx, p); err == nil {
			continue
		}
		return p, nil
	}
	return "", errors.New("media: no free file name")
}

// uploadPolicy resolves the policy for an upload. An empty id means the default.
func uploadPolicy(ctx context.Context, raw string) (mediaPolicyRow, *mediaFailure) {
	if raw == "" {
		p, err := defaultMediaPolicy(ctx)
		if err != nil {
			return mediaPolicyRow{}, &mediaFailure{http.StatusServiceUnavailable, "no_default_policy", "No default media policy is set"}
		}
		return p, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return mediaPolicyRow{}, &mediaFailure{http.StatusBadRequest, "invalid_policy", "policy_id must be a number"}
	}
	p, err := loadMediaPolicy(ctx, id)
	if err != nil {
		return mediaPolicyRow{}, &mediaFailure{http.StatusBadRequest, "invalid_policy", "policy does not exist"}
	}
	if p.Purpose != mediaPurposeMedia {
		return mediaPolicyRow{}, &mediaFailure{http.StatusBadRequest, "invalid_policy", "policy is not a media policy"}
	}
	return p, nil
}

// formGroupID parses an optional group id from the form and checks it exists.
func formGroupID(ctx context.Context, raw string) (*int64, *mediaFailure) {
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, &mediaFailure{http.StatusBadRequest, "invalid_group", "group_id must be a number"}
	}
	var n int64
	if err := mediaDB(ctx).Table("media_groups").Where("id = ?", id).Count(&n).Error; err != nil || n == 0 {
		return nil, &mediaFailure{http.StatusBadRequest, "invalid_group", "group does not exist"}
	}
	return &id, nil
}

func mediaStoreError(c echo.Context, err error) error {
	if errors.Is(err, storage.ErrNotImplemented) || errors.Is(err, errMediaDriverUnavailable) {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{"code": "driver_unavailable", "error": "The storage driver is not enabled"})
	}
	return mediaServerError(c, "store file", err)
}

func handleMediaFileUpload(c echo.Context) error {
	ctx := c.Request().Context()
	name, raw, fail := readMediaUpload(ctx, c)
	if fail != nil {
		return fail.send(c)
	}
	policy, fail := uploadPolicy(ctx, c.FormValue("policy_id"))
	if fail != nil {
		return fail.send(c)
	}
	groupID, fail := formGroupID(ctx, c.FormValue("group_id"))
	if fail != nil {
		return fail.send(c)
	}
	prepared, fail := prepareMedia(ctx, name, raw)
	if fail != nil {
		return fail.send(c)
	}
	alt := strings.TrimSpace(c.FormValue("alt"))
	if utf8.RuneCountInString(alt) > mediaAltMaxRunes {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_alt", "error": "alt is too long"})
	}
	actor := infraActor(c)
	row, err := storeMediaFile(ctx, mediaStoreInput{Policy: policy, Prepared: prepared, Source: "upload", GroupID: groupID, Alt: alt, Actor: actor})
	if err != nil {
		return mediaStoreError(c, err)
	}
	return c.JSON(http.StatusCreated, mediaFileJSON(row, policy.Driver))
}

// handleMediaFileUpdate changes alt text and group. A group_id of null
// ungroups the file. Absent fields are left as they are.
func handleMediaFileUpdate(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "File not found"})
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_json", "error": "Invalid request"})
	}
	updates := map[string]any{}
	if raw, present := body["alt"]; present {
		var alt string
		if err := json.Unmarshal(raw, &alt); err != nil || utf8.RuneCountInString(alt) > mediaAltMaxRunes {
			return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_alt", "error": "alt must be text of 500 characters or fewer"})
		}
		updates["alt"] = alt
	}
	if raw, present := body["group_id"]; present {
		if string(raw) == "null" {
			updates["group_id"] = nil
		} else {
			var gid int64
			if err := json.Unmarshal(raw, &gid); err != nil {
				return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_group", "error": "group_id must be a number or null"})
			}
			if _, fail := formGroupID(ctx, strconv.FormatInt(gid, 10)); fail != nil {
				return fail.send(c)
			}
			updates["group_id"] = gid
		}
	}
	if len(updates) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "nothing_to_update", "error": "No changes given"})
	}
	if _, err := loadMediaFile(ctx, id); err != nil {
		return fileLoadError(c, err)
	}
	updates["updated_at"] = time.Now()
	if err := mediaDB(ctx).Table("media_files").Where("id = ? AND deleted_at IS NULL", id).Updates(updates).Error; err != nil {
		return mediaServerError(c, "update file", err)
	}
	row, err := loadMediaFile(ctx, id)
	if err != nil {
		return fileLoadError(c, err)
	}
	drivers, err := loadPolicyDrivers(ctx)
	if err != nil {
		return mediaServerError(c, "load policies", err)
	}
	return c.JSON(http.StatusOK, mediaFileJSON(row, drivers[row.PolicyID]))
}

// handleMediaFileTrash moves a file to the trash. Its bytes stay until the
// trash purge job removes them.
func handleMediaFileTrash(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "File not found"})
	}
	now := time.Now()
	res := mediaDB(ctx).Table("media_files").Where("id = ? AND deleted_at IS NULL", id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now})
	if res.Error != nil {
		return mediaServerError(c, "trash file", res.Error)
	}
	if res.RowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "File not found"})
	}
	syslog.Audit(ctx, infraActor(c), "media.file.trash", map[string]any{"file_id": id})
	return c.JSON(http.StatusOK, map[string]any{"trashed": true})
}

// handleMediaFileReplace overwrites a file's bytes in place. The id, path and
// public address stay the same. The new file must have the same type.
func handleMediaFileReplace(c echo.Context) error {
	ctx := c.Request().Context()
	id, ok := mediaIDParam(c)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]any{"code": "not_found", "error": "File not found"})
	}
	row, err := loadMediaFile(ctx, id)
	if err != nil {
		return fileLoadError(c, err)
	}
	policy, err := loadMediaPolicy(ctx, row.PolicyID)
	if err != nil {
		return policyLoadError(c, err)
	}
	_, raw, fail := readMediaUpload(ctx, c)
	if fail != nil {
		return fail.send(c)
	}
	prepared, fail := prepareMedia(ctx, row.Filename, raw)
	if fail != nil {
		return fail.send(c)
	}
	if prepared.Mime != row.Mime {
		return c.JSON(http.StatusUnsupportedMediaType, map[string]any{"code": "type_mismatch", "error": "The replacement must have the same type"})
	}
	drv, err := mediaDriver(policy)
	if err != nil {
		return mediaStoreError(c, err)
	}
	if err := drv.Put(ctx, row.Path, bytes.NewReader(prepared.Data), int64(len(prepared.Data)), prepared.Mime); err != nil {
		return mediaStoreError(c, err)
	}
	updates := map[string]any{
		"size":          int64(len(prepared.Data)),
		"sha256":        prepared.Sha256,
		"width":         prepared.Width,
		"height":        prepared.Height,
		"exif_stripped": prepared.ExifStripped,
		"updated_at":    time.Now(),
	}
	if err := mediaDB(ctx).Table("media_files").Where("id = ?", id).Updates(updates).Error; err != nil {
		return mediaServerError(c, "update replaced file", err)
	}
	row, err = loadMediaFile(ctx, id)
	if err != nil {
		return fileLoadError(c, err)
	}
	syslog.Audit(ctx, infraActor(c), "media.file.replace", map[string]any{"file_id": id})
	return c.JSON(http.StatusOK, mediaFileJSON(row, policy.Driver))
}

// ---- remote fetch ----

type mediaFetchResult struct {
	URL    string
	Status string
	Error  string
	File   *mediaFileRow
	Driver string
}

func (r mediaFetchResult) json() map[string]any {
	out := map[string]any{"url": r.URL, "status": r.Status}
	if r.Error != "" {
		out["error"] = r.Error
	}
	if r.File != nil {
		out["file"] = mediaFileJSON(*r.File, r.Driver)
	}
	return out
}

// handleMediaFetch downloads up to mediaFetchMaxURLs files into the default
// policy. Every request goes through safehttp, which refuses private and
// loopback addresses at dial time. Blocked URLs are listed in "blocked".
func handleMediaFetch(c echo.Context) error {
	ctx := c.Request().Context()
	var in struct {
		URLs []string `json:"urls"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_json", "error": "Invalid request"})
	}
	if len(in.URLs) == 0 || len(in.URLs) > mediaFetchMaxURLs {
		return c.JSON(http.StatusBadRequest, map[string]any{"code": "invalid_urls", "error": fmt.Sprintf("urls must hold 1 to %d entries", mediaFetchMaxURLs)})
	}
	policy, err := defaultMediaPolicy(ctx)
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]any{"code": "no_default_policy", "error": "No default media policy is set"})
	}
	maxBytes := mediaMaxBytes(ctx)
	actor := infraActor(c)
	results := make([]map[string]any, 0, len(in.URLs))
	blocked := []string{}
	for _, raw := range in.URLs {
		res := fetchMediaURL(ctx, policy, raw, maxBytes, actor)
		if res.Status == "blocked" {
			blocked = append(blocked, raw)
		}
		results = append(results, res.json())
	}
	return c.JSON(http.StatusOK, map[string]any{"results": results, "blocked": blocked})
}

func fetchMediaURL(ctx context.Context, policy mediaPolicyRow, raw string, maxBytes int64, actor uint) mediaFetchResult {
	res := mediaFetchResult{URL: raw, Driver: policy.Driver}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		res.Status, res.Error = "rejected", "only absolute http and https URLs are accepted"
		return res
	}
	client := safehttp.Client(safehttp.Options{Timeout: mediaFetchTimeout, MaxBytes: maxBytes})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.Status, res.Error = "rejected", "URL cannot be requested"
		return res
	}
	req.Header.Set("User-Agent", "jelly-cms-media-fetch")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, safehttp.ErrBlockedAddress) {
			res.Status, res.Error = "blocked", "destination address is not allowed"
			return res
		}
		res.Status, res.Error = "failed", "request failed"
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		res.Status, res.Error = "failed", fmt.Sprintf("upstream returned %d", resp.StatusCode)
		return res
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		res.Status, res.Error = "failed", "response cannot be read"
		return res
	}
	if int64(len(data)) > maxBytes {
		res.Status, res.Error = "rejected", "file exceeds the size limit"
		return res
	}
	name := path.Base(u.Path)
	if name == "/" || name == "." {
		name = "fetched"
	}
	prepared, fail := prepareMedia(ctx, name, data)
	if fail != nil {
		res.Status, res.Error = "rejected", fail.Msg
		return res
	}
	row, err := storeMediaFile(ctx, mediaStoreInput{Policy: policy, Prepared: prepared, Source: "fetch", Actor: actor})
	if err != nil {
		res.Status, res.Error = "failed", "file cannot be stored"
		log.Printf("media: store fetched file: %v", err)
		return res
	}
	res.Status = "stored"
	res.File = &row
	return res
}
