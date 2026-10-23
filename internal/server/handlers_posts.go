package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Ticket 09 G0 (docs/tickets/09-editor.md): these handlers still store the
// request's content as sent. G0 changes them to render source through
// markup.Render, ignore content when source is present, sanitize content-only
// writes with the policy from renderTrustFor (editor_routes.go), and return
// diagnostics. Until then a content-only write from an author can store
// script; that is the G0 security fix.
func registerPostRoutes(api *echo.Group) {
	api.GET("/posts", listPosts)
	api.POST("/posts", createPost)
	api.GET("/posts/:id", getPost)
	api.PUT("/posts/:id", updatePost)
	api.DELETE("/posts/:id", deletePost)
}

func listPosts(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	postType := c.QueryParam("type")
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
	if user.Role == "author" && postType != "post" {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}
	post := core.Post{AuthorID: user.ID, Type: postType, Visibility: "public"}
	_, err = applyPostInput(&post, input)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if _, explicit := input["published_at"]; !explicit && post.Status == "published" && post.PublishedAt == nil {
		now := time.Now()
		post.PublishedAt = &now
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := core.NewPostService(tx).Create(&post); err != nil {
			return err
		}
		if err := applyPostTerms(tx, &post, input); err != nil {
			return err
		}
		kind := "manual"
		if post.Status == "published" {
			kind = "publish"
		}
		_, err := RecordRevision(tx, post, user.ID, kind)
		return err
	})
	if err != nil {
		return termWriteError(c, err, "Unable to create post")
	}
	var created core.Post
	if err := data.DB.Preload("Categories").Preload("Tags").First(&created, post.ID).Error; err != nil {
		return postQueryError(c, err)
	}
	return c.JSON(http.StatusOK, created)
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
	if user.Role == "author" {
		query = query.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	var post core.Post
	if err := query.First(&post).Error; err != nil {
		return postQueryError(c, err)
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
	if nextType, ok := updates["type"].(string); ok && user.Role == "author" && nextType != "post" {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}
	updates["updated_at"] = time.Now()
	if _, explicit := input["published_at"]; !explicit && existing.PublishedAt == nil {
		if status, ok := updates["status"].(string); ok && status == "published" {
			updates["published_at"] = updates["updated_at"]
		}
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		write := tx.Model(&core.Post{}).Where("id = ?", id)
		if user.Role == "author" {
			write = write.Where("author_id = ? AND type = ?", user.ID, "post")
		}
		result := write.Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := applyPostTerms(tx, &existing, input); err != nil {
			return err
		}
		next := existing
		if _, err := applyPostInput(&next, input); err != nil {
			return err
		}
		if stamped, ok := updates["published_at"].(time.Time); ok && next.PublishedAt == nil {
			next.PublishedAt = &stamped
		}
		if kind, ok := revisionKindFor(existing, next, input); ok {
			if _, err := RecordRevision(tx, next, user.ID, kind); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	if err != nil {
		return termWriteError(c, err, "Unable to update post")
	}
	var updated core.Post
	read := data.DB.Where("id = ?", id).Preload("Categories").Preload("Tags")
	if user.Role == "author" {
		read = read.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	if err := read.First(&updated).Error; err != nil {
		return postQueryError(c, err)
	}
	return c.JSON(http.StatusOK, updated)
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
	// Soft delete: the post moves to the recycle bin with its slug suffixed so the slug is free again.
	var post core.Post
	if err := visiblePostQuery(data.DB, user, id).First(&post).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete post"})
	}
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&post).Updates(map[string]any{
			"slug":          trashedSlug(post.Slug, id),
			"deleted_by_id": user.ID,
		}).Error; err != nil {
			return err
		}
		return tx.Delete(&post).Error
	})
	if err != nil {
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
		"template": {}, "meta": {}, "published_at": {},
		"excerpt": {}, "cover": {}, "visibility": {}, "allow_comments": {}, "pinned": {},
	} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		switch key {
		case "title", "slug", "content", "type", "status", "template", "excerpt", "cover", "visibility":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
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
