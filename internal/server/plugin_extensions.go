package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"gorm.io/gorm"
)

var extensionRuntimeState struct {
	sync.RWMutex
	runtime *plugin.Runtime
}

func setExtensionRuntime(runtime *plugin.Runtime) {
	extensionRuntimeState.Lock()
	extensionRuntimeState.runtime = runtime
	extensionRuntimeState.Unlock()
}

func currentExtensionRuntime() *plugin.Runtime {
	extensionRuntimeState.RLock()
	defer extensionRuntimeState.RUnlock()
	return extensionRuntimeState.runtime
}

func extensionDatabase(ctx context.Context) *gorm.DB {
	if ctx == nil {
		ctx = context.Background()
	}
	if db := mediaDB(ctx); db != nil {
		return db
	}
	return data.DB
}

// extensionPluginMeta validates the live runtime snapshot against the current
// database row. The manifest in the worker is trusted only while the row is
// enabled and its explicit permission approval still matches that manifest.
func extensionPluginMeta(ctx context.Context, db *gorm.DB, id, point string) (*plugin.Runtime, plugin.PluginMetadata, error) {
	if db == nil || id == "" || point == "" {
		return nil, plugin.PluginMetadata{}, errors.New("extension provider is unavailable")
	}
	runtime := currentExtensionRuntime()
	if runtime == nil || !runtime.IsLoaded(id) || !runtime.HasExtension(id, point) {
		return nil, plugin.PluginMetadata{}, errors.New("extension provider is unavailable")
	}
	meta, err := runtime.GetPlugin(id)
	if err != nil || meta == nil || meta.ID != id {
		return nil, plugin.PluginMetadata{}, errors.New("extension provider is unavailable")
	}
	var row struct {
		Enabled bool   `gorm:"column:enabled"`
		Granted string `gorm:"column:permissions_granted_hash"`
	}
	if err := db.WithContext(ctx).Table("plugins").Select("enabled, permissions_granted_hash").Where("id = ?", id).Take(&row).Error; err != nil || !row.Enabled {
		return nil, plugin.PluginMetadata{}, errors.New("extension provider is unavailable")
	}
	want := plugin.PermissionNamesHash(plugin.ManifestPermissionNames(*meta))
	if row.Granted == "" || row.Granted != want {
		return nil, plugin.PluginMetadata{}, errors.New("extension provider approval is stale")
	}
	return runtime, *meta, nil
}

func liveExtensionPluginIDs(ctx context.Context, db *gorm.DB, point string) []string {
	if db == nil {
		return nil
	}
	var ids []string
	if err := db.WithContext(ctx).Table("plugins").Where("enabled = ?", true).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return nil
	}
	runtime := currentExtensionRuntime()
	if runtime == nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, _, err := extensionPluginMeta(ctx, db, id, point); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func extensionSelection(ctx context.Context, db *gorm.DB, point string) (string, bool) {
	var active []string
	if db == nil || db.WithContext(ctx).Table("extension_bindings").Where("point = ?", point).
		Order("position ASC, provider_id ASC").Pluck("provider_id", &active).Error != nil {
		return extensionBuiltinID, false
	}
	live := make(map[string]bool)
	for _, id := range liveExtensionPluginIDs(ctx, db, point) {
		live[id] = true
	}
	goProviders := make(map[string]bool)
	for _, provider := range ext.Resolve(point) {
		goProviders[provider.ID] = true
	}
	for _, id := range active {
		if id == extensionBuiltinID {
			return id, true
		}
		if live[id] {
			return id, true
		}
		if goProviders[id] {
			return id, false
		}
	}
	if ext.Shipped(point) {
		return extensionBuiltinID, true
	}
	return "", false
}

// storagePolicyProviderAllowed keeps attachment strategies independent when
// several storage providers are active. An explicit plugin:<id> policy may use
// any live provider in a non-empty active set; with an empty set it is itself
// the explicit selection and remains usable while enabled and approved.
func storagePolicyProviderAllowed(ctx context.Context, db *gorm.DB, id string) bool {
	if db == nil || id == "" {
		return false
	}
	var active []string
	if err := db.WithContext(ctx).Table("extension_bindings").Where("point = ?", ext.PointMediaStorage).
		Order("position ASC, provider_id ASC").Pluck("provider_id", &active).Error; err != nil {
		return false
	}
	if len(active) == 0 {
		return true
	}
	for _, providerID := range active {
		if providerID == id {
			return true
		}
	}
	return false
}

func extensionSearchPosts(ctx context.Context, db *gorm.DB, query string, page, size int) ([]searchResult, int64, bool) {
	id, isJS := extensionSelection(ctx, db, ext.PointSearchEngine)
	if !isJS || id == extensionBuiltinID {
		return nil, 0, false
	}
	runtime, meta, err := extensionPluginMeta(ctx, db, id, ext.PointSearchEngine)
	if err != nil {
		return nil, 0, false
	}
	input, _ := json.Marshal(map[string]any{"query": query, "max_candidates": 10000})
	output, err := runtime.CallExtension(ctx, meta.ID, ext.PointSearchEngine, input)
	if err != nil {
		return nil, 0, false
	}
	var payload struct {
		IDs      json.RawMessage `json:"ids"`
		Complete *bool           `json:"complete"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Complete == nil || !*payload.Complete || len(payload.IDs) == 0 || string(payload.IDs) == "null" {
		return nil, 0, false
	}
	var rawIDs []json.Number
	if err := json.Unmarshal(payload.IDs, &rawIDs); err != nil || len(rawIDs) > 10000 {
		return nil, 0, false
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, 0, false
	}
	ids := make([]uint, 0, len(rawIDs))
	seen := make(map[uint]struct{}, len(rawIDs))
	for _, raw := range rawIDs {
		parsed, err := strconv.ParseUint(string(raw), 10, strconv.IntSize)
		if err != nil || parsed == 0 {
			return nil, 0, false
		}
		id := uint(parsed)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	results, total, err := searchPublicPostsByIDs(db, query, ids, page, size)
	if err != nil {
		return nil, 0, false
	}
	return results, total, true
}

func searchPublicPostsByIDs(db *gorm.DB, query string, ids []uint, page, size int) ([]searchResult, int64, error) {
	if len(ids) == 0 {
		return []searchResult{}, 0, nil
	}
	var publicIDs []uint
	if err := archivePublicPosts(db).Where("posts.id IN ?", ids).Pluck("posts.id", &publicIDs).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = publicPostsPageSize
	}
	start := ^uint64(0)
	if uint64(page-1) <= ^uint64(0)/uint64(size) {
		start = uint64(page-1) * uint64(size)
	}
	public := make(map[uint]struct{}, len(publicIDs))
	for _, id := range publicIDs {
		public[id] = struct{}{}
	}
	total := int64(0)
	pageCapacity := size
	if pageCapacity > len(public) {
		pageCapacity = len(public)
	}
	pageIDs := make([]uint, 0, pageCapacity)
	for _, id := range ids {
		if _, ok := public[id]; !ok {
			continue
		}
		if uint64(total) >= start && len(pageIDs) < size {
			pageIDs = append(pageIDs, id)
		}
		total++
	}
	if len(pageIDs) == 0 {
		return []searchResult{}, total, nil
	}
	var posts []core.Post
	if err := archivePublicPosts(db).Select("posts.id, posts.title, posts.slug, posts.content, posts.excerpt, posts.author_id, posts.cover, posts.published_at, posts.created_at, posts.updated_at").
		Preload("Author").Where("posts.id IN ?", pageIDs).Find(&posts).Error; err != nil {
		return nil, 0, err
	}
	byID := make(map[uint]core.Post, len(posts))
	for _, post := range posts {
		byID[post.ID] = post
	}
	results := make([]searchResult, 0, len(posts))
	for _, id := range pageIDs {
		post, ok := byID[id]
		if !ok {
			continue
		}
		source := post.Excerpt
		if plainText(source) == "" || !containsFold(plainText(source), query) {
			source = post.Content
		}
		results = append(results, searchResult{Post: post, Title: theme.Highlight(post.Title, query), Snippet: theme.Snippet(source, query, 100)})
	}
	return results, total, nil
}

func extensionRandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
