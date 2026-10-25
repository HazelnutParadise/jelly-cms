package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"github.com/HazelnutParadise/jelly-cms/internal/store"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Plugin store and plugin installs from URLs (docs/tickets/08-system.md, 08d).
//
// Official items must carry a minisign signature over the archive. Other items
// install only when the admin accepts exactly the permissions the catalog
// lists, identified by PermissionNamesHash. Installs are interactive only, the
// same rule as injection_api.go (isInteractiveRequest).

const (
	storeListPageSize      = 20
	storeMaxBatch          = 100
	storeMaxCatalogBytes   = 1 << 20
	storeMaxSigBytes       = 4 << 10
	storeMaxArchiveBytes   = 50 << 20
	storeFetchTimeout      = 30 * time.Second
	storeDownloadTimeout   = 2 * time.Minute
	storeCatalogLockKey    = 731984202
	storeSourceEmbedded    = "embedded"
	storeSourceRemote      = "remote"
	storeSourceCommunity   = "community"
	storeSourceOfficial    = "official"
	storeSourceURL         = "url"
	storeSourceUpload      = "upload"
	storeSignatureOfficial = "official"
	storeSignatureNone     = "unsigned"
)

var (
	storeSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	storeLikeEscaper   = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	// errPluginRunning means the plugin is loaded, so its files must not change.
	errPluginRunning = errors.New("plugin is running")
	// pluginFilesMu serializes changes to data/plugins/<id> and their plugins rows.
	pluginFilesMu sync.Mutex
)

// storeError carries the status a store request answers with.
type storeError struct {
	status int
	msg    string
}

func (e *storeError) Error() string { return e.msg }

func storeFail(status int, msg string) error {
	return &storeError{status: status, msg: msg}
}

// storeErrorResponse answers err. Unexpected errors are logged and hidden.
func storeErrorResponse(c echo.Context, err error) error {
	var se *storeError
	if errors.As(err, &se) {
		return infraError(c, se.status, se.msg)
	}
	log.Printf("store request failed: %v", err)

	return infraError(c, http.StatusInternalServerError, "Store request failed")
}

type storeAPI struct {
	runtime *plugin.Runtime
}

// registerStoreRoutes mounts the store and the install-from-URL endpoints. Every
// route is admin only; the install and update routes are interactive only.
func registerStoreRoutes(e *echo.Echo, runtime *plugin.Runtime) {
	api := &storeAPI{runtime: runtime}
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/store", api.list, admin)
	e.POST("/api/admin/store/refresh", api.refresh, admin)
	e.POST("/api/admin/store/install", api.install, interactiveOnly, admin)
	e.POST("/api/admin/store/update", api.update, interactiveOnly, admin)
	e.POST("/api/admin/plugins/install-url", api.installURL, interactiveOnly, admin)
}

// storeListItem is one entry of the store listing.
type storeListItem struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Category         string   `json:"category"`
	Version          string   `json:"version"`
	Description      string   `json:"description"`
	Official         bool     `json:"official"`
	Permissions      []string `json:"permissions"`
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	UpdateAvailable  bool     `json:"update_available"`
}

// list returns one page of the catalog. Filters: kind, category, q (id or name),
// installed, updates (installed and behind the catalog).
func (a *storeAPI) list(c echo.Context) error {
	ctx := c.Request().Context()
	if err := ensureStoreCatalog(ctx); err != nil {
		return storeErrorResponse(c, err)
	}

	page := 1
	if raw := c.QueryParam("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return infraError(c, http.StatusBadRequest, "page must be a positive number")
		}
		page = n
	}

	where := []string{"TRUE"}
	var args []any
	if kind := strings.TrimSpace(c.QueryParam("kind")); kind != "" {
		where = append(where, "c.kind = ?")
		args = append(args, kind)
	}
	if category := strings.TrimSpace(c.QueryParam("category")); category != "" {
		where = append(where, "c.category = ?")
		args = append(args, category)
	}
	if q := strings.TrimSpace(c.QueryParam("q")); q != "" {
		pattern := "%" + storeLikeEscaper.Replace(q) + "%"
		where = append(where, "(c.id ILIKE ? OR c.name ILIKE ?)")
		args = append(args, pattern, pattern)
	}
	for _, flag := range []string{"installed", "updates"} {
		raw := c.QueryParam(flag)
		if raw == "" {
			continue
		}
		on, err := strconv.ParseBool(raw)
		if err != nil {
			return infraError(c, http.StatusBadRequest, flag+" must be true or false")
		}
		switch {
		case flag == "installed" && on:
			where = append(where, "p.id IS NOT NULL")
		case flag == "installed":
			where = append(where, "p.id IS NULL")
		case on:
			where = append(where, "p.id IS NOT NULL AND p.version <> c.version")
		}
	}
	cond := strings.Join(where, " AND ")

	var total int64
	countSQL := `SELECT COUNT(*) FROM store_catalog c LEFT JOIN plugins p ON p.id = c.id WHERE ` + cond
	if err := data.DB.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return storeErrorResponse(c, err)
	}

	type row struct {
		ID               string  `gorm:"column:id"`
		Payload          []byte  `gorm:"column:payload"`
		Installed        bool    `gorm:"column:installed"`
		InstalledVersion *string `gorm:"column:installed_version"`
	}
	var rows []row
	listSQL := `SELECT c.id, c.payload, (p.id IS NOT NULL) AS installed, p.version AS installed_version
		FROM store_catalog c LEFT JOIN plugins p ON p.id = c.id
		WHERE ` + cond + ` ORDER BY c.name, c.id LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), storeListPageSize, (page-1)*storeListPageSize)
	if err := data.DB.WithContext(ctx).Raw(listSQL, listArgs...).Scan(&rows).Error; err != nil {
		return storeErrorResponse(c, err)
	}

	items := make([]storeListItem, 0, len(rows))
	for _, r := range rows {
		var item store.Item
		if err := json.Unmarshal(r.Payload, &item); err != nil {
			return storeErrorResponse(c, fmt.Errorf("decode catalog item %s: %w", r.ID, err))
		}
		view := storeListItem{
			ID:          item.ID,
			Name:        item.Name,
			Kind:        item.Kind,
			Category:    item.Category,
			Version:     item.Version,
			Description: item.Description,
			Official:    item.Official,
			Permissions: nonNilStrings(item.Permissions),
			Installed:   r.Installed,
		}
		if r.InstalledVersion != nil {
			view.InstalledVersion = *r.InstalledVersion
			view.UpdateAvailable = *r.InstalledVersion != item.Version
		}
		items = append(items, view)
	}

	return c.JSON(http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "per_page": storeListPageSize})
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

// refresh fetches the remote catalog, or reloads the built-in list when no
// remote is configured. A catalog whose signature does not verify is refused and
// the stored catalog stays as it was.
func (a *storeAPI) refresh(c echo.Context) error {
	ctx := c.Request().Context()
	if os.Getenv("JELLY_TEST_POSTGRES") != "1" {
		jobID, err := EnqueueManualStoreRefresh(ctx)
		if err != nil {
			return infraError(c, http.StatusServiceUnavailable, "Store refresh queue is unavailable")
		}
		syslog.Audit(ctx, pluginActorID(c), "store.refresh", map[string]any{"state": "queued", "job_id": jobID})
		return c.JSON(http.StatusAccepted, map[string]any{"state": "queued", "job_id": jobID})
	}
	source := store.CatalogURL()
	label := storeSourceEmbedded
	if source != "" {
		label = storeSourceRemote
	}

	items, err := loadStoreCatalog(ctx, source)
	if err != nil {
		log.Printf("store catalog refresh: %v", err)
		if errors.Is(err, store.ErrNoPublicKey) {
			return infraError(c, http.StatusServiceUnavailable, "Store signing key is not configured")
		}

		return infraError(c, http.StatusBadGateway, "Store catalog refresh failed")
	}
	if err := writeStoreCatalog(ctx, items, label, false); err != nil {
		return storeErrorResponse(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "store.refresh", map[string]any{"items": len(items), "source": label})

	return c.JSON(http.StatusOK, map[string]any{"items": len(items), "source": label, "refreshed_at": time.Now().UTC().Format(time.RFC3339)})
}

// loadStoreCatalog returns the items of the remote catalog after its signature
// verifies, or the built-in list when source is empty.
func loadStoreCatalog(ctx context.Context, source string) ([]store.Item, error) {
	if source == "" {
		return store.Embedded()
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && store.TestSeamsActive())) {
		return nil, fmt.Errorf("catalog URL must be https: %q", source)
	}

	body, err := fetchStoreBytes(ctx, source, storeMaxCatalogBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	sig, err := fetchStoreBytes(ctx, source+".minisig", storeMaxSigBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog signature: %w", err)
	}
	if err := update.VerifyMinisign(store.PublicKeyLine(), body, sig); err != nil {
		return nil, fmt.Errorf("catalog signature: %w", err)
	}

	return store.ParseCatalog(body)
}

// fetchStoreBytes downloads rawURL through safehttp and returns at most limit bytes.
func fetchStoreBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	client := safehttp.Client(safehttp.Options{Timeout: storeFetchTimeout, MaxBytes: limit})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, safehttp.ErrBodyTooLarge
	}

	return raw, nil
}

// writeStoreCatalog replaces the stored catalog. With seedOnly it does nothing
// when a catalog is already stored. A transaction lock keeps two writers from
// interleaving.
func writeStoreCatalog(ctx context.Context, items []store.Item, source string, seedOnly bool) error {
	return data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, int64(storeCatalogLockKey)).Error; err != nil {
			return err
		}
		if seedOnly {
			var stored int64
			if err := tx.Raw(`SELECT COUNT(*) FROM store_meta WHERE key = 'source'`).Scan(&stored).Error; err != nil {
				return err
			}
			if stored > 0 {
				return nil
			}
		}
		if err := tx.Exec(`DELETE FROM store_catalog`).Error; err != nil {
			return err
		}
		for _, item := range items {
			payload, err := json.Marshal(item)
			if err != nil {
				return err
			}
			signature := storeSignatureNone
			if item.Official {
				signature = storeSignatureOfficial
			}
			itemSource := storeSourceCommunity
			if item.Official {
				itemSource = storeSourceOfficial
			}
			if err := tx.Exec(`INSERT INTO store_catalog (id, kind, name, version, category, description, source, signature, payload, fetched_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, CAST(? AS jsonb), now())`,
				item.ID, item.Kind, item.Name, item.Version, item.Category, item.Description, itemSource, signature, string(payload)).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec(`INSERT INTO store_meta (key, value) VALUES ('source', ?), ('refreshed_at', ?)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
			source, time.Now().UTC().Format(time.RFC3339)).Error; err != nil {
			return err
		}

		return nil
	})
}

// ensureStoreCatalog seeds the built-in list the first time the store is used.
func ensureStoreCatalog(ctx context.Context) error {
	items, err := store.Embedded()
	if err != nil {
		return err
	}

	return writeStoreCatalog(ctx, items, storeSourceEmbedded, true)
}

// storeCatalogItem reads one catalog entry.
func storeCatalogItem(ctx context.Context, id string) (store.Item, error) {
	var rows []struct {
		Payload []byte `gorm:"column:payload"`
	}
	if err := data.DB.WithContext(ctx).Raw(`SELECT payload FROM store_catalog WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return store.Item{}, err
	}
	if len(rows) == 0 {
		return store.Item{}, storeFail(http.StatusNotFound, "Store item not found")
	}
	var item store.Item
	if err := json.Unmarshal(rows[0].Payload, &item); err != nil {
		return store.Item{}, fmt.Errorf("decode catalog item %s: %w", id, err)
	}

	return item, nil
}

type storeInstallRequest struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	AcceptPermissions string `json:"accept_permissions"`
}

// install installs one catalog item. accept_permissions must equal the
// permissions hash of a non-official item.
func (a *storeAPI) install(c echo.Context) error {
	ctx := c.Request().Context()
	var req storeInstallRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request")
	}
	if err := ensureStoreCatalog(ctx); err != nil {
		return storeErrorResponse(c, err)
	}
	item, err := storeCatalogItem(ctx, req.ID)
	if err != nil {
		return storeErrorResponse(c, err)
	}
	if req.Version != "" && req.Version != item.Version {
		return infraError(c, http.StatusConflict, "That version is not in the catalog")
	}

	res, err := a.installItem(ctx, item, req.AcceptPermissions)
	if err != nil {
		return storeErrorResponse(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "store.install", map[string]any{"plugin": item.ID, "version": item.Version, "official": item.Official})

	return c.JSON(http.StatusOK, res)
}

type storeInstallResult struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	Enabled         bool   `json:"enabled"`
}

// installItem downloads, verifies and installs item. The plugin stays disabled
// until an admin enables it.
func (a *storeAPI) installItem(ctx context.Context, item store.Item, accepted string) (storeInstallResult, error) {
	if pluginRunning(a.runtime, item.ID) {
		return storeInstallResult{}, storeFail(http.StatusConflict, "Plugin is running; disable it before installing")
	}
	signature := storeSignatureNone
	if item.Official {
		signature = storeSignatureOfficial
		if store.PublicKeyLine() == "" {
			return storeInstallResult{}, storeFail(http.StatusServiceUnavailable, "Official signing key is not configured")
		}
	} else if accepted != plugin.PermissionNamesHash(item.Permissions) {
		return storeInstallResult{}, storeFail(http.StatusBadRequest, "Accept the plugin permissions before installing")
	}
	if item.URL == "" {
		return storeInstallResult{}, storeFail(http.StatusConflict, "Store item has no download")
	}

	archive, err := downloadPluginArchive(ctx, item.URL)
	if err != nil {
		log.Printf("store download %s: %v", item.ID, err)
		return storeInstallResult{}, storeFail(http.StatusBadGateway, "Download failed")
	}
	defer os.Remove(archive.path)
	if item.SHA256 != "" && archive.sha256 != item.SHA256 {
		return storeInstallResult{}, storeFail(http.StatusBadGateway, "Download does not match the catalog checksum")
	}
	if item.Official {
		sig, err := fetchStoreBytes(ctx, item.URL+".minisig", storeMaxSigBytes)
		if err != nil {
			log.Printf("store signature %s: %v", item.ID, err)
			return storeInstallResult{}, storeFail(http.StatusBadGateway, "Signature is unavailable")
		}
		raw, err := os.ReadFile(archive.path)
		if err != nil {
			return storeInstallResult{}, fmt.Errorf("read archive: %w", err)
		}
		if err := update.VerifyMinisign(store.PublicKeyLine(), raw, sig); err != nil {
			return storeInstallResult{}, storeFail(http.StatusBadGateway, "Plugin signature does not verify")
		}
	}

	source := storeSourceURL
	if item.Official {
		source = storeSourceOfficial
	}
	written, err := installPluginFile(ctx, a.runtime, archive.path, pluginFileInstall{
		Source:    source,
		Signature: signature,
		Want: plugin.ArchiveExpectation{
			ID:              item.ID,
			Version:         item.Version,
			PermissionsHash: plugin.PermissionNamesHash(item.Permissions),
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, errPluginRunning):
			return storeInstallResult{}, storeFail(http.StatusConflict, "Plugin is running; disable it before installing")
		case errors.Is(err, errPluginRecoveryPending):
			return storeInstallResult{}, storeFail(http.StatusConflict, "Plugin recovery is pending")
		case plugin.IsArchiveError(err):
			log.Printf("store archive %s: %v", item.ID, err)
			return storeInstallResult{}, storeFail(http.StatusBadGateway, "Plugin archive does not match the catalog entry")
		}

		return storeInstallResult{}, err
	}

	return storeInstallResult{ID: written.ID, Version: written.Version, PreviousVersion: written.PreviousVersion, Enabled: false}, nil
}

type storeUpdateRequest struct {
	IDs []string `json:"ids"`
	All bool     `json:"all"`
}

type storeUpdateFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// update installs the catalog version of each installed plugin that is behind.
// A third-party plugin whose permissions did not change keeps its granted
// permissions without a new accept.
func (a *storeAPI) update(c echo.Context) error {
	ctx := c.Request().Context()
	var req storeUpdateRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request")
	}
	if !req.All && len(req.IDs) == 0 {
		return infraError(c, http.StatusBadRequest, "Pass ids or all")
	}
	if len(req.IDs) > storeMaxBatch {
		return infraError(c, http.StatusBadRequest, "At most 100 ids per request")
	}
	if err := ensureStoreCatalog(ctx); err != nil {
		return storeErrorResponse(c, err)
	}

	type installed struct {
		ID                 string `gorm:"column:id"`
		Version            string `gorm:"column:installed_version"`
		PermissionsGranted string `gorm:"column:permissions_granted_hash"`
		Payload            []byte `gorm:"column:payload"`
	}
	var rows []installed
	if err := data.DB.WithContext(ctx).Raw(`SELECT p.id, p.version AS installed_version, p.permissions_granted_hash, c.payload
		FROM plugins p JOIN store_catalog c ON c.id = p.id`).Scan(&rows).Error; err != nil {
		return storeErrorResponse(c, err)
	}
	byID := make(map[string]installed, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	targets := make([]string, 0)
	if req.All {
		for _, r := range rows {
			targets = append(targets, r.ID)
		}
	} else {
		targets = append(targets, req.IDs...)
	}

	updated := []storeInstallResult{}
	failed := []storeUpdateFailure{}
	for _, id := range targets {
		r, ok := byID[id]
		if !ok {
			if !req.All {
				failed = append(failed, storeUpdateFailure{ID: id, Error: "Plugin is not installed from the store"})
			}
			continue
		}
		var item store.Item
		if err := json.Unmarshal(r.Payload, &item); err != nil {
			failed = append(failed, storeUpdateFailure{ID: id, Error: "Catalog entry is unreadable"})
			continue
		}
		if r.Version == item.Version {
			if !req.All {
				failed = append(failed, storeUpdateFailure{ID: id, Error: "Already up to date"})
			}
			continue
		}

		accepted := ""
		if !item.Official && r.PermissionsGranted == plugin.PermissionNamesHash(item.Permissions) {
			accepted = r.PermissionsGranted
		}
		res, err := a.installItem(ctx, item, accepted)
		if err != nil {
			var se *storeError
			msg := "Update failed"
			if errors.As(err, &se) {
				msg = se.msg
			} else {
				log.Printf("store update %s: %v", id, err)
			}
			failed = append(failed, storeUpdateFailure{ID: id, Error: msg})
			continue
		}
		updated = append(updated, res)
	}
	syslog.Audit(ctx, pluginActorID(c), "store.update", map[string]any{"updated": len(updated), "failed": len(failed)})

	return c.JSON(http.StatusOK, map[string]any{"updated": updated, "failed": failed})
}

type installURLRequest struct {
	URL               string `json:"url"`
	SHA256            string `json:"sha256"`
	PreviewSHA256     string `json:"preview_sha256"`
	AcceptPermissions string `json:"accept_permissions"`
}

// installURL downloads a plugin archive from an http or https address through
// safehttp, which refuses private and loopback addresses. When sha256 is given
// the download must match it.
func (a *storeAPI) installURL(c echo.Context) error {
	ctx := c.Request().Context()
	var req installURLRequest
	if err := c.Bind(&req); err != nil {
		return infraError(c, http.StatusBadRequest, "Invalid request")
	}
	if err := validateDownloadURL(req.URL); err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}
	want := strings.ToLower(strings.TrimSpace(req.SHA256))
	if want != "" && !storeSHA256Pattern.MatchString(want) {
		return infraError(c, http.StatusBadRequest, "sha256 must be 64 hex characters")
	}

	archive, err := downloadPluginArchive(ctx, req.URL)
	if err != nil {
		if errors.Is(err, safehttp.ErrBlockedAddress) {
			return infraError(c, http.StatusBadRequest, "That address is not allowed")
		}
		log.Printf("plugin url download: %v", err)

		return infraError(c, http.StatusBadGateway, "Download failed")
	}
	defer os.Remove(archive.path)
	if want != "" && archive.sha256 != want {
		return infraError(c, http.StatusBadRequest, "sha256 does not match the downloaded file")
	}
	meta, err := plugin.ValidateArchive(archive.path)
	if err != nil {
		return infraError(c, http.StatusBadRequest, err.Error())
	}
	permissions := plugin.ManifestPermissionNames(meta)
	permissionsHash := plugin.PermissionNamesHash(permissions)
	if c.QueryParam("dry") == "1" {
		return c.JSON(http.StatusOK, map[string]any{"id": meta.ID, "version": meta.Version, "permissions": permissions, "permissions_hash": permissionsHash, "sha256": archive.sha256})
	}
	previewSHA256 := strings.ToLower(strings.TrimSpace(req.PreviewSHA256))
	if previewSHA256 == "" {
		previewSHA256 = want
	}
	if len(permissions) > 0 && (req.AcceptPermissions != permissionsHash || previewSHA256 != archive.sha256) {
		return infraError(c, http.StatusConflict, "Preview this exact archive and accept its permissions before installing")
	}

	res, err := installPluginFile(ctx, a.runtime, archive.path, pluginFileInstall{Source: storeSourceURL, Signature: storeSignatureNone, Want: plugin.ArchiveExpectation{ID: meta.ID, Version: meta.Version, PermissionsHash: permissionsHash}})
	if err != nil {
		switch {
		case errors.Is(err, errPluginRunning):
			return infraError(c, http.StatusConflict, "Plugin is running; disable it before installing")
		case errors.Is(err, errPluginRecoveryPending):
			return infraError(c, http.StatusConflict, "Plugin recovery is pending")
		case plugin.IsArchiveError(err):
			return infraError(c, http.StatusBadRequest, err.Error())
		}

		return storeErrorResponse(c, err)
	}
	syslog.Audit(ctx, pluginActorID(c), "plugin.install", map[string]any{"plugin": res.ID, "version": res.Version, "source": storeSourceURL})

	return c.JSON(http.StatusOK, res)
}

func validateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("url must be an absolute http or https address")
	}

	return nil
}

// downloadedArchive is a plugin archive saved to a temporary file.
type downloadedArchive struct {
	path   string
	sha256 string
}

// downloadPluginArchive saves the archive at rawURL to a temporary file and
// returns its path and sha256. The caller removes the file.
func downloadPluginArchive(ctx context.Context, rawURL string) (downloadedArchive, error) {
	client := safehttp.Client(safehttp.Options{Timeout: storeDownloadTimeout, MaxBytes: storeMaxArchiveBytes})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return downloadedArchive{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return downloadedArchive{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return downloadedArchive{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "jelly-plugin-*.zip")
	if err != nil {
		return downloadedArchive{}, err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, digest), resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())

		return downloadedArchive{}, fmt.Errorf("save archive: %w", errors.Join(copyErr, closeErr))
	}

	return downloadedArchive{path: tmp.Name(), sha256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// pluginFileInstall describes how an extracted archive is recorded.
type pluginFileInstall struct {
	Source    string
	Signature string
	Want      plugin.ArchiveExpectation
}

// pluginFileResult is the outcome of installPluginFile.
type pluginFileResult struct {
	ID              string `json:"id"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	Enabled         bool   `json:"enabled"`
}

// installPluginFile replaces data/plugins/<id> with the archive and records the
// plugins row. A running plugin is refused with errPluginRunning, and nothing on
// disk changes. A new row starts disabled.
func installPluginFile(ctx context.Context, runtime *plugin.Runtime, zipPath string, in pluginFileInstall) (pluginFileResult, error) {
	meta, err := plugin.InspectArchive(zipPath)
	if err != nil {
		return pluginFileResult{}, err
	}
	if pluginRunning(runtime, meta.ID) {
		return pluginFileResult{}, errPluginRunning
	}
	var result pluginFileResult
	err = withPluginInstallLock(ctx, meta.ID, func() error {
		if pluginRunning(runtime, meta.ID) {
			return errPluginRunning
		}
		stamp, err := newPluginCommitStamp(ctx, meta.ID)
		if err != nil {
			return err
		}
		result, err = installPluginFileLocked(ctx, zipPath, in, stamp)
		return err
	})
	return result, err
}

func installPluginFileLocked(ctx context.Context, zipPath string, in pluginFileInstall, stamp time.Time) (pluginFileResult, error) {
	staged, err := plugin.StageArchive(zipPath, pluginDirRoot, in.Want)
	if err != nil {
		return pluginFileResult{}, err
	}
	defer staged.Cleanup()
	installed, err := plugin.ReadManifest(staged.Stage)
	if err != nil {
		return pluginFileResult{}, fmt.Errorf("read staged manifest: %w", err)
	}
	permissionsHash := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(installed))
	j, err := commitPluginArchive(staged, installed.Version, permissionsHash, stamp)
	if err != nil {
		return pluginFileResult{}, errors.Join(fmt.Errorf("activate plugin files: %w", err), rollbackPluginArchive(j))
	}
	previous, err := writePluginRow(ctx, pluginRowWrite{
		ID:              staged.ID,
		Version:         installed.Version,
		Source:          in.Source,
		Signature:       in.Signature,
		PermissionsHash: permissionsHash,
		CommitStamp:     stamp,
	})
	if err != nil {
		committed, checkErr := pluginInstallRowCommitted(ctx, staged.ID, installed.Version, permissionsHash, stamp, j.TreeSHA256)
		if checkErr == nil && committed {
			if cleanupErr := finishPluginArchiveCommit(j); cleanupErr != nil {
				log.Printf("plugin %s: post-commit cleanup deferred: %v", staged.ID, cleanupErr)
			}
			return pluginFileResult{ID: staged.ID, Version: installed.Version, Enabled: false}, nil
		}
		if checkErr == nil {
			return pluginFileResult{}, errors.Join(fmt.Errorf("record plugin %s: %w", staged.ID, err), rollbackPluginArchive(j))
		}
		return pluginFileResult{}, errors.Join(fmt.Errorf("record plugin %s: %w", staged.ID, err), fmt.Errorf("verify plugin row commit: %w", checkErr))
	}
	if cleanupErr := finishPluginArchiveCommit(j); cleanupErr != nil {
		log.Printf("plugin %s: post-commit cleanup deferred: %v", staged.ID, cleanupErr)
	}

	return pluginFileResult{ID: staged.ID, Version: installed.Version, PreviousVersion: previous, Enabled: false}, nil
}

func pluginInstallRowCommitted(ctx context.Context, id, version, permissionsHash string, stamp time.Time, treeSHA256 string) (bool, error) {
	var rows []struct {
		Version         string    `gorm:"column:version"`
		PermissionsHash string    `gorm:"column:permissions_granted_hash"`
		UpdatedAt       time.Time `gorm:"column:updated_at"`
	}
	if err := data.DB.WithContext(ctx).Raw(`SELECT version, permissions_granted_hash, updated_at FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return false, err
	}
	if len(rows) != 1 || rows[0].Version != version || rows[0].PermissionsHash != permissionsHash || !rows[0].UpdatedAt.Equal(stamp) {
		return false, nil
	}
	digest, err := pluginTreeDigest(filepath.Join(pluginDirRoot, id))
	if err != nil {
		return false, err
	}
	return digest == treeSHA256, nil
}

func pluginInstallRowAbsent(ctx context.Context, id string) (bool, error) {
	var rows []struct {
		ID string `gorm:"column:id"`
	}
	if err := data.DB.WithContext(ctx).Raw(`SELECT id FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return false, err
	}
	return len(rows) == 0, nil
}

func newPluginCommitStamp(ctx context.Context, id string) (time.Time, error) {
	var rows []struct {
		UpdatedAt time.Time `gorm:"column:updated_at"`
	}
	if err := data.DB.WithContext(ctx).Raw(`SELECT updated_at FROM plugins WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return time.Time{}, err
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	if len(rows) > 0 && !stamp.After(rows[0].UpdatedAt.UTC()) {
		stamp = rows[0].UpdatedAt.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	return stamp, nil
}

type pluginRowWrite struct {
	ID              string
	Version         string
	Source          string
	Signature       string
	PermissionsHash string
	CommitStamp     time.Time
}

// writePluginRow inserts or updates the plugins row. An existing row keeps its
// enabled flag. It returns the version the row replaced, or "" when the version
// did not change.
func writePluginRow(ctx context.Context, w pluginRowWrite) (string, error) {
	var replaced string
	err := data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			Version string `gorm:"column:version"`
		}
		if err := tx.Raw(`SELECT version FROM plugins WHERE id = ? FOR UPDATE`, w.ID).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return tx.Exec(`INSERT INTO plugins (id, kind, version, source, enabled, signature, permissions_granted_hash, installed_at, updated_at)
				VALUES (?, 'plugin', ?, ?, FALSE, ?, ?, ?, ?)`,
				w.ID, w.Version, w.Source, w.Signature, w.PermissionsHash, w.CommitStamp, w.CommitStamp).Error
		}
		replaced = rows[0].Version

		return tx.Exec(`UPDATE plugins SET
				previous_version = CASE WHEN version <> ? THEN version ELSE previous_version END,
			version = ?, source = ?, signature = ?, permissions_granted_hash = ?, updated_at = ?
			WHERE id = ?`,
			w.Version, w.Version, w.Source, w.Signature, w.PermissionsHash, w.CommitStamp, w.ID).Error
	})
	if err != nil {
		return "", err
	}
	if replaced == w.Version {
		return "", nil
	}

	return replaced, nil
}

// pluginRunning reports whether the runtime has id loaded.
func pluginRunning(runtime *plugin.Runtime, id string) bool {
	return runtime != nil && runtime.IsLoaded(id)
}
