package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errPostConflict = errors.New("post changed")
var errReservedSlug = errors.New("slug is reserved")
var errRenderDiagnostics = errors.New("render diagnostics")

// Ticket 09 G0 (docs/tickets/09-editor.md): source writes are rendered and
// sanitized on the server, while legacy content-only writes keep the HTML path
// and are sanitized under the caller's trust policy. Both paths return render
// diagnostics, and content-only writes clear stale source state.
func registerPostRoutes(api, reads *echo.Group) {
	reads.GET("/posts", listPosts, requirePostReadPermission)
	api.POST("/posts", createPost)
	reads.GET("/posts/:id", getPost, requirePostReadPermission)
	api.PUT("/posts/:id", updatePost)
	api.DELETE("/posts/:id", deletePost)
}

func requirePostReadPermission(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		principal, err := auth.GetCurrentPrincipal(c)
		if err != nil || principal == nil || principal.User == nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		}
		return auth.RequirePermission("content_read")(next)(c)
	}
}

func listPosts(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	postType := c.QueryParam("type")
	if scopedType, ok := c.Get("agent.content_type").(string); ok && scopedType != "" {
		postType = scopedType
	}
	if postType == "" {
		postType = "post"
	}
	query := data.DB.Model(&core.Post{}).Where("type = ?", postType).Preload("Categories").Preload("Tags")
	if user.Role == "author" {
		query = query.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	var posts []core.Post
	if err := query.Find(&posts).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load posts"})
	}
	return c.JSON(http.StatusOK, posts)
}

func createPost(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	input, err := decodePostInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	postType := "post"
	if value, ok := input["type"]; ok {
		if err := json.Unmarshal(value, &postType); err != nil || (postType != "post" && postType != "page") {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid content type"})
		}
	}
	if postType != "post" && !roleListContains(user.Role, editorRoles) {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}
	post := core.Post{AuthorID: user.ID, Type: postType, Visibility: "public"}
	_, err = applyPostInput(&post, input)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	cfg := loadPermalinkConfig(c.Request().Context())
	diagnostics, err := preparePostContent(c, &post, input, user)
	if err != nil {
		if errors.Is(err, errRenderDiagnostics) {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error(), "diagnostics": diagnostics})
		}
		return renderInputError(c, err)
	}
	if _, explicit := input["published_at"]; !explicit && post.Status == "published" && post.PublishedAt == nil {
		now := time.Now()
		post.PublishedAt = &now
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if post.Slug == "" {
			slug, err := newContentSlug(tx, cfg, post)
			if err != nil {
				return err
			}
			post.Slug = slug
		}
		if core.IsReservedSlug(post.Slug, cfg.currentPrefixNames()...) {
			return errReservedSlug
		}
		if err := core.NewPostService(tx).Create(&post); err != nil {
			return err
		}
		if err := applyPostTerms(tx, &post, input); err != nil {
			return err
		}
		// A new post must be reachable at its own URL, so drop any automatic
		// redirect that still sends that path elsewhere after a slug change.
		if err := redirects.ReleaseAutoPath(tx, permalinkPathFor(tx, cfg, post)); err != nil {
			return err
		}
		kind := "manual"
		if post.Status == "published" {
			kind = "publish"
		}
		_, err := RecordRevision(tx, post, user.ID, kind)
		if err != nil {
			return err
		}
		return recordIndexNowIntent(c.Request().Context(), tx, indexNowItem{Kind: post.Type, ID: post.ID, Slug: post.Slug}, accountBaseURL(c))
	})
	if errors.Is(err, errReservedSlug) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Slug is reserved"})
	}
	if err != nil {
		return termWriteError(c, err, "Unable to create post")
	}
	var created core.Post
	if err := data.DB.Preload("Categories").Preload("Tags").First(&created, post.ID).Error; err != nil {
		return postQueryError(c, err)
	}
	item := indexNowItem{Kind: created.Type, ID: created.ID, Slug: created.Slug}
	if err := dispatchIndexNowPending(c.Request().Context(), data.DB, 1); err != nil {
		reportIndexNowEnqueueFailure(c, item, err)
	}
	return postResponse(c, created, diagnostics)
}

func getPost(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parsePostID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	query := data.DB.Where("id = ?", id).Preload("Categories").Preload("Tags")
	if contentType, ok := c.Get("agent.content_type").(string); ok && contentType != "" {
		query = query.Where("type = ?", contentType)
	}
	if user.Role == "author" {
		query = query.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	var post core.Post
	if err := query.First(&post).Error; err != nil {
		return postQueryError(c, err)
	}
	var latest core.Revision
	if err := data.DB.Where("post_id = ? AND kind = ?", post.ID, "autosave").Order("id DESC").First(&latest).Error; err == nil && latest.CreatedAt.After(post.UpdatedAt) {
		b, _ := json.Marshal(post)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		body["autosave"] = map[string]any{"id": latest.ID, "title": latest.Title, "source": latest.Source, "excerpt": latest.Excerpt, "created_at": latest.CreatedAt}
		return c.JSON(http.StatusOK, body)
	}
	return c.JSON(http.StatusOK, post)
}

func updatePost(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parsePostID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	input, err := decodePostInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	var existing core.Post
	visible := data.DB.Where("id = ?", id)
	if contentType, ok := c.Get("agent.content_type").(string); ok && contentType != "" {
		visible = visible.Where("type = ?", contentType)
	}
	if user.Role == "author" {
		visible = visible.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	if err := visible.First(&existing).Error; err != nil {
		return postQueryError(c, err)
	}
	updates, err := postUpdates(input)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if raw, hasFormat := input["format"]; hasFormat {
		_, hasSource := input["source"]
		_, hasContent := input["content"]
		var requested string
		if json.Unmarshal(raw, &requested) != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid format"})
		}
		if !hasSource && !hasContent && requested != existing.Format {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Format changes require source or content"})
		}
	}
	var baseUpdatedAt *time.Time
	if raw, ok := input["base_updated_at"]; ok {
		var base time.Time
		if json.Unmarshal(raw, &base) != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid base_updated_at"})
		}
		baseUpdatedAt = &base
		if !base.Equal(existing.UpdatedAt) {
			if c.Get("agent.identity") != nil {
				return writeAPIError(c, http.StatusConflict, "version_conflict", "Post changed since it was read", map[string]any{"current_updated_at": existing.UpdatedAt.Format(time.RFC3339Nano)})
			}
			return c.JSON(http.StatusConflict, map[string]any{"error": "Post changed", "updated_at": existing.UpdatedAt})
		}
	}
	nextForRender := existing
	if _, err := applyPostInput(&nextForRender, input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	cfg := loadPermalinkConfig(c.Request().Context())
	_, slugInput := input["slug"]
	_, typeInput := input["type"]
	if (slugInput || typeInput) && core.IsReservedSlug(nextForRender.Slug, cfg.currentPrefixNames()...) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Slug is reserved"})
	}
	diagnostics, err := preparePostContent(c, &nextForRender, input, user)
	if err != nil {
		if errors.Is(err, errRenderDiagnostics) {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error(), "diagnostics": diagnostics})
		}
		return renderInputError(c, err)
	}
	for _, key := range []string{"content", "source", "format", "render_version", "render_trust", "meta"} {
		updates[key] = fieldValue(nextForRender, key)
	}
	if nextType, ok := updates["type"].(string); ok && nextType != "post" && !roleListContains(user.Role, editorRoles) {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}
	_, seoInput := input["seo_index"]
	_, seoAIInput := input["seo_ai"]
	if (seoInput || seoAIInput) && user.Role == "author" {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}
	updates["updated_at"] = time.Now()
	if _, explicit := input["published_at"]; !explicit && existing.PublishedAt == nil {
		if status, ok := updates["status"].(string); ok && status == "published" {
			updates["published_at"] = updates["updated_at"]
		}
	}
	// The old URL is built before the write; the new one after the categories
	// are saved, because a {category} pattern depends on them.
	oldPath := permalinkPathFor(data.DB, cfg, existing)
	slugRedirectSkipped := ""
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		write := tx.Model(&core.Post{}).Where("id = ?", id)
		if contentType, ok := c.Get("agent.content_type").(string); ok && contentType != "" {
			write = write.Where("type = ?", contentType)
		}
		if user.Role == "author" {
			write = write.Where("author_id = ? AND type = ?", user.ID, "post")
		}
		if baseUpdatedAt != nil {
			write = write.Where("updated_at = ?", *baseUpdatedAt)
		}
		result := write.Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if baseUpdatedAt != nil {
				return errPostConflict
			}
			return gorm.ErrRecordNotFound
		}
		if err := applyPostTerms(tx, &existing, input); err != nil {
			return err
		}
		var saved core.Post
		if err := tx.Select("id", "type", "slug", "published_at", "created_at").First(&saved, id).Error; err != nil {
			return err
		}
		newPath := permalinkPathFor(tx, cfg, saved)
		next := existing
		next = nextForRender
		if stamped, ok := updates["published_at"].(time.Time); ok && next.PublishedAt == nil {
			next.PublishedAt = &stamped
		}
		if kind, ok := revisionKindFor(existing, next, input); ok {
			if _, err := RecordRevision(tx, next, user.ID, kind); err != nil {
				return err
			}
		}
		// Changing the slug or type moves the public URL. The old URL gets an
		// automatic 301 (ticket 08b).
		if oldPath != newPath && existing.Slug != "" {
			skipped, err := redirects.SlugChangeRedirect(tx, oldPath, newPath, user.ID)
			if err != nil {
				return err
			}
			slugRedirectSkipped = skipped
		}
		return recordIndexNowIntent(c.Request().Context(), tx, indexNowItem{Kind: saved.Type, ID: saved.ID, Slug: saved.Slug}, accountBaseURL(c))
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	if errors.Is(err, errPostConflict) {
		if c.Get("agent.identity") != nil {
			var current core.Post
			if readErr := data.DB.Select("updated_at").First(&current, id).Error; readErr == nil {
				return writeAPIError(c, http.StatusConflict, "version_conflict", "Post changed since it was read", map[string]any{"current_updated_at": current.UpdatedAt.Format(time.RFC3339Nano)})
			}
		}
		return c.JSON(http.StatusConflict, map[string]string{"error": "Post changed"})
	}
	if err != nil {
		return termWriteError(c, err, "Unable to update post")
	}
	if slugRedirectSkipped != "" {
		syslog.Emit(c.Request().Context(), syslog.Entry{
			Level:   syslog.LevelWarn,
			Source:  syslog.SourceApp,
			Message: "slug redirect not created",
			Fields:  map[string]any{"reason": slugRedirectSkipped, "post_id": id},
		})
	}
	var updated core.Post
	read := data.DB.Where("id = ?", id).Preload("Categories").Preload("Tags")
	if user.Role == "author" {
		read = read.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	if err := read.First(&updated).Error; err != nil {
		return postQueryError(c, err)
	}
	item := indexNowItem{Kind: updated.Type, ID: updated.ID, Slug: updated.Slug}
	if err := dispatchIndexNowPending(c.Request().Context(), data.DB, 1); err != nil {
		reportIndexNowEnqueueFailure(c, item, err)
	}
	return postResponse(c, updated, diagnostics)
}

func postResponse(c echo.Context, post core.Post, diagnostics []markup.Diagnostic) error {
	b, _ := json.Marshal(post)
	var result map[string]any
	_ = json.Unmarshal(b, &result)
	result["diagnostics"] = diagnostics
	return c.JSON(http.StatusOK, result)
}

func fieldValue(p core.Post, key string) any {
	switch key {
	case "content":
		return p.Content
	case "source":
		return p.Source
	case "format":
		return p.Format
	case "render_version":
		return p.RenderVersion
	case "render_trust":
		return p.RenderTrust
	case "meta":
		return p.Meta
	}
	return nil
}

func preparePostContent(c echo.Context, post *core.Post, input map[string]json.RawMessage, user *core.User) ([]markup.Diagnostic, error) {
	if raw, supplied := input["format"]; supplied {
		var format string
		if err := json.Unmarshal(raw, &format); err != nil || (format != "jmd" && format != "md" && format != "html") {
			return nil, errors.New("invalid format")
		}
	}
	if raw, supplied := input["source"]; supplied {
		if err := json.Unmarshal(raw, &post.Source); err != nil {
			return nil, err
		}
		format := "jmd"
		if v, ok := input["format"]; ok && json.Unmarshal(v, &format) != nil {
			return nil, errors.New("invalid format")
		}
		if format != "jmd" && format != "md" {
			return nil, errors.New("invalid format")
		}
		post.Format = format
		trust := renderTrustFor(user)
		widgets, err := publishedWidgetSpecs(data.DB.WithContext(c.Request().Context()))
		if err != nil {
			return nil, err
		}
		result, err := markup.Render(c.Request().Context(), post.Source, markup.Options{Trust: trust, Format: format, Widgets: widgets})
		if err != nil {
			return nil, err
		}
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" {
				return result.Diagnostics, errRenderDiagnostics
			}
		}
		post.Content, post.RenderTrust, post.RenderVersion = result.HTML, trust, 1
		post.Meta = writeLegacyMarkdown(post.Meta, post.Source)
		return result.Diagnostics, nil
	}
	if raw, exists := input["content"]; exists {
		var content string
		if err := json.Unmarshal(raw, &content); err != nil {
			return nil, err
		}
		post.Content = markup.Policy(renderTrustFor(user)).Sanitize(content)
		post.Source = ""
		post.Format = "html"
		post.RenderTrust = renderTrustFor(user)
		post.RenderVersion = 1
		var suppliedMarkdown bool
		if metaRaw, ok := input["meta"]; ok {
			var metaFields map[string]json.RawMessage
			if json.Unmarshal(metaRaw, &metaFields) == nil {
				_, suppliedMarkdown = metaFields["markdown"]
			}
		}
		if !suppliedMarkdown {
			post.Meta = clearLegacyMarkdown(post.Meta)
		}
	}
	return []markup.Diagnostic{}, nil
}

func writeLegacyMarkdown(meta json.RawMessage, source string) json.RawMessage {
	// Only object metadata mirrors the source; other valid JSON, including null, stays untouched.
	if len(bytes.TrimSpace(meta)) > 0 && json.Valid(meta) && bytes.TrimSpace(meta)[0] != '{' {
		return meta
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(meta, &fields) != nil || fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	raw, _ := json.Marshal(source)
	fields["markdown"] = raw
	out, _ := json.Marshal(fields)
	return out
}

func clearLegacyMarkdown(meta json.RawMessage) json.RawMessage {
	// Only object metadata can carry the legacy markdown key; other valid JSON stays untouched.
	if len(bytes.TrimSpace(meta)) > 0 && json.Valid(meta) && bytes.TrimSpace(meta)[0] != '{' {
		return meta
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(meta, &fields) != nil || fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	delete(fields, "markdown")
	out, _ := json.Marshal(fields)
	return out
}

func renderInputError(c echo.Context, err error) error {
	status := http.StatusBadRequest
	if errors.Is(err, markup.ErrSourceTooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	return c.JSON(status, map[string]any{"error": err.Error(), "diagnostics": []markup.Diagnostic{}})
}

func deletePost(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parsePostID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	// Lock and re-check the type and visibility inside the same transaction as
	// the soft delete so a concurrent type change cannot trash the wrong item.
	var post core.Post
	err = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		query := visiblePostQuery(tx, user, id).Clauses(clause.Locking{Strength: "UPDATE"})
		contentType, isAgent := c.Get("agent.content_type").(string)
		if isAgent && contentType != "" {
			query = query.Where("type = ?", contentType)
		}
		if err := query.First(&post).Error; err != nil {
			return err
		}
		write := tx.Model(&core.Post{}).Where("id = ? AND deleted_at IS NULL", post.ID)
		delete := tx.Where("id = ? AND deleted_at IS NULL", post.ID)
		if isAgent && contentType != "" {
			write = write.Where("type = ?", contentType)
			delete = delete.Where("type = ?", contentType)
		}
		result := write.Updates(map[string]any{
			"slug":          trashedSlug(post.Slug, id),
			"deleted_by_id": user.ID,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		result = delete.Delete(&core.Post{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete post"})
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Deleted"})
}

func decodePostInput(c echo.Context) (map[string]json.RawMessage, error) {
	defer c.Request().Body.Close()
	decoder := json.NewDecoder(c.Request().Body)
	var input map[string]json.RawMessage
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, errors.New("expected JSON object")
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("unexpected trailing data")
	}
	return input, nil
}

func postUpdates(input map[string]json.RawMessage) (map[string]interface{}, error) {
	updates := make(map[string]interface{})
	for key := range map[string]struct{}{
		"title": {}, "slug": {}, "content": {}, "type": {}, "status": {},
		"source": {}, "format": {},
		"template": {}, "meta": {}, "published_at": {},
		"excerpt": {}, "cover": {}, "visibility": {}, "allow_comments": {}, "pinned": {},
		"seo_index": {}, "seo_ai": {},
	} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		switch key {
		case "title", "slug", "content", "source", "format", "type", "status", "template", "excerpt", "cover", "visibility", "seo_index", "seo_ai":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			if key == "seo_index" && value != "inherit" && value != "index" && value != "noindex" {
				return nil, errors.New("invalid seo_index")
			}
			if key == "seo_ai" && value != "inherit" && value != "allow" && value != "noai" {
				return nil, errors.New("invalid seo_ai")
			}
			if key == "type" && value != "post" && value != "page" {
				return nil, errors.New("invalid content type")
			}
			if key == "visibility" && value != "public" && value != "private" {
				return nil, errors.New("invalid visibility")
			}
			updates[key] = value
		case "allow_comments", "pinned":
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			updates[key] = value
		case "meta":
			var value json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			updates[key] = value
		case "published_at":
			if string(raw) == "null" {
				updates[key] = nil
			} else {
				var value time.Time
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, err
				}
				updates[key] = value
			}
		}
	}
	return updates, nil
}

func applyPostInput(post *core.Post, input map[string]json.RawMessage) (map[string]interface{}, error) {
	updates, err := postUpdates(input)
	if err != nil {
		return nil, err
	}
	for key, value := range updates {
		switch key {
		case "title":
			post.Title = value.(string)
		case "slug":
			post.Slug = value.(string)
		case "content":
			post.Content = value.(string)
		case "source":
			post.Source = value.(string)
		case "format":
			post.Format = value.(string)
		case "type":
			post.Type = value.(string)
		case "status":
			post.Status = value.(string)
		case "template":
			post.Template = value.(string)
		case "excerpt":
			post.Excerpt = value.(string)
		case "cover":
			post.Cover = value.(string)
		case "visibility":
			post.Visibility = value.(string)
		case "seo_index":
			post.SEOIndex = value.(string)
		case "seo_ai":
			post.SEOAI = value.(string)
		case "pinned":
			post.Pinned = value.(bool)
		case "allow_comments":
			allow := value.(bool)
			post.AllowComments = &allow
		case "meta":
			post.Meta = value.(json.RawMessage)
		case "published_at":
			if value == nil {
				post.PublishedAt = nil
			} else {
				published := value.(time.Time)
				post.PublishedAt = &published
			}
		}
	}
	return updates, nil
}

// applyPostTerms replaces the post's categories and/or tags. Only the relation
// whose keys the input carries changes: category_ids leaves tags alone, and
// tag_ids or tags leaves categories alone. Unknown ids are a termError; a tag
// name that matches no tag creates one. Call it inside the write's transaction.
func applyPostTerms(db *gorm.DB, post *core.Post, input map[string]json.RawMessage) error {
	categoryRaw, hasCategories := input["category_ids"]
	tagIDRaw, hasTagIDs := input["tag_ids"]
	tagNameRaw, hasTagNames := input["tags"]

	if hasCategories {
		ids, err := decodeTermIDs(categoryRaw, "category_ids")
		if err != nil {
			return err
		}
		categories := make([]core.Category, 0, len(ids))
		if len(ids) > 0 {
			if err := db.Where("id IN ?", ids).Find(&categories).Error; err != nil {
				return err
			}
			if len(categories) != len(ids) {
				return termError("category_ids contains an unknown category")
			}
		}
		if err := db.Model(post).Association("Categories").Replace(categories); err != nil {
			return err
		}
	}

	if hasTagIDs || hasTagNames {
		var found []core.Tag
		if hasTagIDs {
			ids, err := decodeTermIDs(tagIDRaw, "tag_ids")
			if err != nil {
				return err
			}
			if len(ids) > 0 {
				if err := db.Where("id IN ?", ids).Find(&found).Error; err != nil {
					return err
				}
				if len(found) != len(ids) {
					return termError("tag_ids contains an unknown tag")
				}
			}
		}
		if hasTagNames {
			var names []string
			if err := json.Unmarshal(tagNameRaw, &names); err != nil {
				return termError("tags must be a list of names")
			}
			for _, name := range names {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if utf8.RuneCountInString(name) > 120 {
					return termError("tag name is too long")
				}
				tag, err := findOrCreateTag(db, name)
				if err != nil {
					return err
				}
				found = append(found, tag)
			}
		}
		tags := make([]core.Tag, 0, len(found))
		seen := make(map[uint]bool, len(found))
		for _, tag := range found {
			if !seen[tag.ID] {
				seen[tag.ID] = true
				tags = append(tags, tag)
			}
		}
		if err := db.Model(post).Association("Tags").Replace(tags); err != nil {
			return err
		}
	}
	return nil
}

// decodeTermIDs reads a JSON list of ids and drops repeats.
func decodeTermIDs(raw json.RawMessage, key string) ([]uint, error) {
	var ids []uint
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, termError(key + " must be a list of ids")
	}
	seen := make(map[uint]bool, len(ids))
	unique := make([]uint, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique, nil
}

func parsePostID(value string) (uint, bool) {
	id, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint(id), true
}

func postQueryError(c echo.Context, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load post"})
}
