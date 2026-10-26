package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	indexNowJobKind          = "sitemap.indexnow"
	indexNowEndpoint         = "https://api.indexnow.org/indexnow"
	indexNowTimeout          = 8 * time.Second
	indexNowMaxBody          = 4096
	indexNowBatchMax         = 100
	indexNowPendingKeyPrefix = "system_indexnow_pending:"
)

var (
	indexNowKeyPattern        = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)
	indexNowGenerationPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type indexNowItem struct {
	Kind string `json:"kind"`
	ID   uint   `json:"id"`
	Slug string `json:"slug"`
}

type indexNowJob struct {
	Item    indexNowItem `json:"item"`
	SiteURL string       `json:"site_url"`
	URL     string       `json:"url"`
}

type indexNowIntent struct {
	Version    int       `json:"version"`
	Generation string    `json:"generation"`
	Kind       string    `json:"kind"`
	ID         uint      `json:"id"`
	SiteURL    string    `json:"site_url"`
	NotBefore  time.Time `json:"not_before"`
}

type indexNowPendingDispatchState struct {
	sync.Mutex
	cursor string
}

var indexNowPendingDispatchStates sync.Map // map[*gorm.DB]*indexNowPendingDispatchState

type indexNowRequest struct {
	Host        string   `json:"host"`
	Key         string   `json:"key"`
	KeyLocation string   `json:"keyLocation"`
	URLList     []string `json:"urlList"`
}

func init() {
	RegisterSystemJobHandler(indexNowJobKind, runIndexNowJob)
}

// recordIndexNowIntent keeps the content write and its notification intent in
// the same transaction. Queue availability is handled after commit.
func recordIndexNowIntent(ctx context.Context, tx *gorm.DB, item indexNowItem, rawSiteURL string) error {
	if tx == nil || item.ID == 0 || !validIndexNowKind(item.Kind) {
		return errors.New("indexnow: invalid intent item")
	}
	enabled, err := indexNowOption(ctx, tx, "seo_indexnow_enabled")
	if err != nil {
		return fmt.Errorf("indexnow: read enabled setting: %w", err)
	}
	if enabled != "true" {
		return nil
	}
	siteURL, err := normalizedSiteURL(rawSiteURL)
	if err != nil {
		return fmt.Errorf("indexnow: invalid site URL: %w", err)
	}
	notBefore := time.Now().UTC()
	if item.Kind == "post" || item.Kind == "page" {
		var post core.Post
		if err := tx.WithContext(ctx).Select("published_at", "created_at").Where("id = ? AND type = ?", item.ID, item.Kind).First(&post).Error; err != nil {
			return fmt.Errorf("indexnow: load saved post schedule: %w", err)
		}
		if post.PublishedAt != nil && post.PublishedAt.After(notBefore) {
			notBefore = post.PublishedAt.UTC()
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("indexnow: generate intent generation: %w", err)
	}
	payload, err := json.Marshal(indexNowIntent{
		Version: 1, Generation: hex.EncodeToString(random[:]), Kind: item.Kind,
		ID: item.ID, SiteURL: siteURL, NotBefore: notBefore,
	})
	if err != nil {
		return fmt.Errorf("indexnow: encode intent: %w", err)
	}
	key := indexNowPendingOptionKey(item.Kind, item.ID)
	row := core.Option{Key: key, Value: string(payload)}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("indexnow: persist intent: %w", err)
	}
	return nil
}

func dispatchIndexNowPending(ctx context.Context, db *gorm.DB, limit int) error {
	if db == nil {
		return errors.New("indexnow: pending dispatcher database is unavailable")
	}
	if limit <= 0 {
		return nil
	}
	enabled, err := indexNowOption(ctx, db, "seo_indexnow_enabled")
	if err != nil {
		return fmt.Errorf("indexnow: read enabled setting: %w", err)
	}
	if enabled != "true" {
		return nil
	}

	type pendingDispatch struct {
		option core.Option
		item   indexNowItem
		intent indexNowIntent
		url    string
	}
	stateValue, _ := indexNowPendingDispatchStates.LoadOrStore(db, &indexNowPendingDispatchState{})
	state := stateValue.(*indexNowPendingDispatchState)
	state.Lock()
	cursor := state.cursor
	state.Unlock()
	var candidates []pendingDispatch
	var deleted []core.Option
	var invalid []error
	snapshot := db.WithContext(ctx).Begin(&sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if snapshot.Error != nil {
		return fmt.Errorf("indexnow: begin pending snapshot: %w", snapshot.Error)
	}
	scanned := 0
	exhausted := false
	for scanned < limit {
		var rows []core.Option
		pageSize := min(100, limit-scanned)
		if err := snapshot.Where("key LIKE ? ESCAPE E'\\\\' AND key > ?", indexNowPendingOptionLikePattern(), cursor).
			Order("key ASC").Limit(pageSize).Find(&rows).Error; err != nil {
			_ = snapshot.Rollback().Error
			return fmt.Errorf("indexnow: list pending intents: %w", err)
		}
		if len(rows) == 0 {
			if cursor != "" {
				cursor = ""
				continue
			}
			exhausted = true
			break
		}
		for _, row := range rows {
			scanned++
			cursor = row.Key
			kind, id, ok := parseIndexNowPendingOptionKey(row.Key)
			if !ok {
				invalid = append(invalid, fmt.Errorf("indexnow: malformed pending key %q", row.Key))
				continue
			}
			var intent indexNowIntent
			if err := json.Unmarshal([]byte(row.Value), &intent); err != nil ||
				intent.Version != 1 || !indexNowGenerationPattern.MatchString(intent.Generation) ||
				intent.Kind != kind || intent.ID != id || intent.NotBefore.IsZero() {
				invalid = append(invalid, fmt.Errorf("indexnow: malformed pending intent for %s %d", kind, id))
				continue
			}
			siteURL, err := normalizedSiteURL(intent.SiteURL)
			if err != nil || siteURL != intent.SiteURL {
				invalid = append(invalid, fmt.Errorf("indexnow: invalid site URL in pending intent for %s %d", kind, id))
				continue
			}
			item, deletedItem, err := currentIndexNowItemByID(ctx, snapshot, kind, id)
			if err != nil {
				_ = snapshot.Rollback().Error
				return fmt.Errorf("indexnow: read current %s %d: %w", kind, id, err)
			}
			if deletedItem {
				deleted = append(deleted, row)
				continue
			}
			if intent.NotBefore.After(time.Now().UTC()) {
				continue
			}
			path, eligible, err := currentIndexNowPath(ctx, snapshot, item)
			if err != nil {
				_ = snapshot.Rollback().Error
				return fmt.Errorf("indexnow: check current %s %d: %w", kind, id, err)
			}
			// Private, draft, future, and noindex content keeps its intent so a
			// later edit or policy change can make it eligible.
			if !eligible {
				continue
			}
			if !safeIndexNowPath(path) {
				invalid = append(invalid, fmt.Errorf("indexnow: invalid current path for %s %d", kind, id))
				continue
			}
			currentURL := strings.TrimRight(siteURL, "/") + path
			if !sameSiteURL(siteURL, currentURL) {
				invalid = append(invalid, fmt.Errorf("indexnow: current URL is outside site for %s %d", kind, id))
				continue
			}
			candidates = append(candidates, pendingDispatch{option: row, item: item, intent: intent, url: currentURL})
			if scanned >= limit {
				break
			}
		}
		if len(rows) < pageSize || scanned >= limit {
			exhausted = len(rows) < pageSize
			break
		}
	}
	if err := snapshot.Commit().Error; err != nil {
		return fmt.Errorf("indexnow: close pending snapshot: %w", err)
	}
	state.Lock()
	if exhausted {
		state.cursor = ""
	} else {
		state.cursor = cursor
	}
	state.Unlock()
	for _, row := range deleted {
		if err := deleteIndexNowPendingGeneration(ctx, db, row); err != nil {
			return err
		}
	}
	for _, candidate := range candidates {
		payload, err := json.Marshal(indexNowJob{Item: candidate.item, SiteURL: candidate.intent.SiteURL, URL: candidate.url})
		if err != nil {
			return fmt.Errorf("indexnow: encode job for %s %d: %w", candidate.item.Kind, candidate.item.ID, err)
		}
		_, err = EnqueueSystemJob(ctx, db, queue.Job{
			Queue: queue.QueueWebhook, Kind: indexNowJobKind,
			DedupeKey: fmt.Sprintf("indexnow:%s:%d:%s", candidate.item.Kind, candidate.item.ID, candidate.intent.Generation),
			Payload:   payload, MaxAttempts: queue.DefaultMaxAttempts,
		})
		if err != nil {
			state.Lock()
			state.cursor = ""
			state.Unlock()
			return fmt.Errorf("indexnow: dispatch %s %d: %w", candidate.item.Kind, candidate.item.ID, err)
		}
		if err := deleteIndexNowPendingGeneration(ctx, db, candidate.option); err != nil {
			return err
		}
	}
	if len(invalid) > 0 {
		return errors.Join(invalid...)
	}
	return nil
}

func deleteIndexNowPendingGeneration(ctx context.Context, db *gorm.DB, row core.Option) error {
	result := db.WithContext(ctx).Where("key = ? AND value = ?", row.Key, row.Value).Delete(&core.Option{})
	if result.Error != nil {
		return fmt.Errorf("indexnow: clear dispatched intent: %w", result.Error)
	}
	return nil
}

func indexNowPendingOptionKey(kind string, id uint) string {
	return fmt.Sprintf("%s%s:%d", indexNowPendingKeyPrefix, kind, id)
}

func indexNowPendingOptionLikePattern() string {
	slash := string(rune(92))
	return strings.NewReplacer(slash, slash+slash, "%", slash+"%", "_", slash+"_").Replace(indexNowPendingKeyPrefix) + "%"
}

func parseIndexNowPendingOptionKey(key string) (string, uint, bool) {
	if !strings.HasPrefix(key, indexNowPendingKeyPrefix) {
		return "", 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, indexNowPendingKeyPrefix), ":")
	if len(parts) != 2 || !validIndexNowKind(parts[0]) {
		return "", 0, false
	}
	id64, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || id64 == 0 || uint64(uint(id64)) != id64 {
		return "", 0, false
	}
	return parts[0], uint(id64), true
}

func validIndexNowKind(kind string) bool {
	return kind == "post" || kind == "page" || kind == "product"
}

func currentIndexNowItemByID(ctx context.Context, db *gorm.DB, kind string, id uint) (indexNowItem, bool, error) {
	switch kind {
	case "post", "page":
		var post core.Post
		err := db.WithContext(ctx).Select("id", "type", "slug").Where("id = ? AND type = ?", id, kind).First(&post).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return indexNowItem{}, true, nil
		}
		if err != nil {
			return indexNowItem{}, false, err
		}
		return indexNowItem{Kind: kind, ID: id, Slug: post.Slug}, false, nil
	case "product":
		var product core.Product
		err := db.WithContext(ctx).Select("id", "slug").Where("id = ? AND deleted_by_id IS NULL AND deleted_at IS NULL", id).First(&product).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return indexNowItem{}, true, nil
		}
		if err != nil {
			return indexNowItem{}, false, err
		}
		return indexNowItem{Kind: kind, ID: id, Slug: product.Slug}, false, nil
	default:
		return indexNowItem{}, false, errors.New("indexnow: unknown pending content kind")
	}
}

func reportIndexNowEnqueueFailure(c echo.Context, item indexNowItem, err error) {
	requestID, _ := c.Get(syslog.RequestIDContextKey).(string)
	syslog.Emit(c.Request().Context(), syslog.Entry{
		Level: syslog.LevelError, Source: syslog.SourceJob,
		Message:   "IndexNow pending dispatch failed after content was saved",
		Fields:    map[string]any{"trigger_content_kind": item.Kind, "trigger_content_id": item.ID, "error": err.Error()},
		RequestID: requestID,
	})
}

func runIndexNowJob(ctx context.Context, db *gorm.DB, job queue.Job) error {
	return runIndexNowJobWithClient(ctx, db, job, indexNowHTTPClient())
}

// runIndexNowJobWithClient is the transport seam used by integration tests to
// exercise worker rechecks without contacting the IndexNow service.
func runIndexNowJobWithClient(ctx context.Context, db *gorm.DB, job queue.Job, client *http.Client) error {
	var payload indexNowJob
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("indexnow: decode job: %w", err)
	}
	if db == nil || payload.Item.ID == 0 || payload.Item.Slug == "" {
		return errors.New("indexnow: incomplete job")
	}
	enabled, err := indexNowOption(ctx, db, "seo_indexnow_enabled")
	if err != nil {
		return fmt.Errorf("indexnow: read enabled setting: %w", err)
	}
	if enabled != "true" {
		return nil
	}
	if data.DB != db {
		return errors.New("indexnow: active settings database does not match job database")
	}
	key := settings.Get(ctx, "seo_indexnow_key")
	if !indexNowKeyPattern.MatchString(key) {
		return errors.New("indexnow: current key is missing or invalid")
	}
	siteURL, err := normalizedSiteURL(payload.SiteURL)
	if err != nil || siteURL != payload.SiteURL {
		return errors.New("indexnow: invalid saved site URL")
	}
	path, eligible, err := currentIndexNowPath(ctx, db, payload.Item)
	if err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	if !safeIndexNowPath(path) {
		return errors.New("indexnow: current content path is invalid")
	}
	currentURL := strings.TrimRight(siteURL, "/") + path
	if !sameSiteURL(siteURL, currentURL) || currentURL != payload.URL {
		// The URL changed after the job was queued; a later update can enqueue its
		// current address, while this stale job must never submit it.
		return nil
	}
	return submitIndexNow(ctx, indexNowEndpoint, siteURL, key, []string{currentURL}, client)
}

func submitIndexNow(ctx context.Context, endpoint, siteURL, key string, urls []string, client *http.Client) error {
	if !indexNowKeyPattern.MatchString(key) {
		return errors.New("indexnow: invalid key")
	}
	base, err := normalizedSiteURL(siteURL)
	if err != nil {
		return err
	}
	baseURL, _ := url.Parse(base)
	valid := make([]string, 0, min(len(urls), indexNowBatchMax))
	seen := make(map[string]struct{}, min(len(urls), indexNowBatchMax))
	for _, raw := range urls {
		if len(valid) >= indexNowBatchMax {
			break
		}
		if !safeIndexNowURL(base, raw) {
			return errors.New("indexnow: refusing URL outside the configured site")
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		valid = append(valid, raw)
	}
	if len(valid) == 0 {
		return nil
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Scheme != "https" || endpointURL.Hostname() != "api.indexnow.org" || endpointURL.User != nil {
		return errors.New("indexnow: endpoint must be the official HTTPS API")
	}
	keyLocation := baseURL.Scheme + "://" + baseURL.Host + "/" + key + ".txt"
	body, err := json.Marshal(indexNowRequest{Host: baseURL.Hostname(), Key: key, KeyLocation: keyLocation, URLList: valid})
	if err != nil {
		return fmt.Errorf("indexnow: encode request: %w", err)
	}
	if client == nil {
		client = indexNowHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("indexnow: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("indexnow: submit request: %w", err)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, indexNowMaxBody+1))
	if err != nil {
		return fmt.Errorf("indexnow: read response: %w", err)
	}
	if len(response) > indexNowMaxBody {
		return errors.New("indexnow: response exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("indexnow: API returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func indexNowHTTPClient() *http.Client {
	client := safehttp.Client(safehttp.Options{
		Timeout: indexNowTimeout, MaxBytes: indexNowMaxBody,
		AllowHosts: []string{"api.indexnow.org"},
	})
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func currentIndexNowPath(ctx context.Context, db *gorm.DB, item indexNowItem) (string, bool, error) {
	indexable, err := indexNowItemIndexable(ctx, db, item.Kind, item.Slug, item.ID)
	if err != nil {
		return "", false, err
	}
	if !indexable {
		return "", false, nil
	}
	switch item.Kind {
	case "post", "page":
		var post core.Post
		err := db.WithContext(ctx).Where("id = ? AND type = ? AND slug = ?", item.ID, item.Kind, item.Slug).
			Where("status = ? AND (visibility = ? OR visibility = '' OR visibility IS NULL)", "published", "public").
			Where("COALESCE(published_at, created_at) <= ?", time.Now().UTC()).First(&post).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		path, err := indexNowPostPath(ctx, db, post)
		return path, err == nil, err
	case "product":
		var product core.Product
		err := db.WithContext(ctx).Where("id = ? AND slug = ? AND status = ? AND deleted_by_id IS NULL AND deleted_at IS NULL", item.ID, item.Slug, "active").First(&product).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return "/shop/p/" + url.PathEscape(product.Slug), true, nil
	default:
		return "", false, errors.New("indexnow: unknown content kind")
	}
}

func indexNowItemIndexable(ctx context.Context, db *gorm.DB, kind, slug string, id uint) (bool, error) {
	if db == nil || id == 0 || slug == "" {
		return false, nil
	}
	options, err := indexNowOptions(ctx, db, "seo_index_site", "seo_index_types")
	if err != nil {
		return false, err
	}
	if valueOr(options, "seo_index_site") == "false" {
		return false, nil
	}
	types := map[string]bool{}
	if raw := valueOr(options, "seo_index_types"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &types); err != nil {
			return false, fmt.Errorf("indexnow: read index type policy: %w", err)
		}
	}
	if allowed, set := types[kind]; set && !allowed {
		return false, nil
	}
	switch kind {
	case "post", "page":
		var post core.Post
		err := db.WithContext(ctx).Select("seo_index").Where("id = ? AND type = ? AND slug = ?", id, kind, slug).First(&post).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return post.SEOIndex != "noindex", nil
	case "product":
		var product core.Product
		err := db.WithContext(ctx).Select("seo_index").Where("id = ? AND slug = ? AND deleted_by_id IS NULL AND deleted_at IS NULL", id, slug).First(&product).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return product.SEOIndex != "noindex", nil
	default:
		return false, nil
	}
}

func indexNowPostPath(ctx context.Context, db *gorm.DB, post core.Post) (string, error) {
	keys := []string{keyPermalinkPost, keySiteTimezone, keySlugStrategy}
	for _, prefix := range permalinkPrefixes {
		keys = append(keys, prefix.key)
	}
	options, err := indexNowOptions(ctx, db, keys...)
	if err != nil {
		return "", err
	}
	cfg := permalinkConfig{prefixes: map[string]string{}, slugStrat: valueOr(options, keySlugStrategy), loc: time.UTC}
	pattern, err := parsePermalinkPattern(valueOr(options, keyPermalinkPost))
	if err != nil {
		pattern, _ = parsePermalinkPattern(defaultOption(keyPermalinkPost))
	}
	cfg.post = pattern
	if name := valueOr(options, keySiteTimezone); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			cfg.loc = loc
		}
	}
	for _, prefix := range permalinkPrefixes {
		cfg.prefixes[prefix.route] = valueOr(options, prefix.key)
	}
	return permalinkPathFor(db.WithContext(ctx), cfg, post), nil
}

func indexNowOption(ctx context.Context, db *gorm.DB, key string) (string, error) {
	values, err := indexNowOptions(ctx, db, key)
	if err != nil {
		return "", err
	}
	return valueOr(values, key), nil
}

func indexNowOptions(ctx context.Context, db *gorm.DB, keys ...string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = defaultOption(key)
	}
	if db == nil {
		return values, gorm.ErrInvalidDB
	}
	var rows []core.Option
	if err := db.WithContext(ctx).Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values, nil
}

func normalizedSiteURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("site URL must be absolute HTTP or HTTPS without userinfo, query, or fragment")
	}
	if strings.ContainsAny(parsed.Host, "\r\n\t ") {
		return "", errors.New("site URL host is invalid")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func safeIndexNowPath(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n\\") {
		return false
	}
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	decoded, err := url.PathUnescape(parsed.Path)
	if err != nil || strings.ContainsAny(decoded, "\r\n\\") {
		return false
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func safeIndexNowURL(siteURL, candidate string) bool {
	if !sameSiteURL(siteURL, candidate) {
		return false
	}
	target, err := url.Parse(candidate)
	if err != nil || target.RawQuery != "" || target.Fragment != "" {
		return false
	}
	return safeIndexNowPath(target.EscapedPath())
}

func sameSiteURL(siteURL, candidate string) bool {
	base, err := url.Parse(siteURL)
	if err != nil || base.Hostname() == "" {
		return false
	}
	target, err := url.Parse(candidate)
	if err != nil || target.Hostname() == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return false
	}
	return strings.EqualFold(base.Scheme, target.Scheme) && strings.EqualFold(base.Host, target.Host)
}
