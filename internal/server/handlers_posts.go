package server

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

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
	query := data.DB.Model(&core.Post{}).Where("type = ?", postType)
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
	post := core.Post{AuthorID: user.ID, Type: postType}
	_, err = applyPostInput(&post, input)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if err := core.NewPostService(data.DB).Create(&post); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to create post"})
	}
	return c.JSON(http.StatusOK, post)
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
	query := data.DB.Where("id = ?", id)
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
	write := data.DB.Model(&core.Post{}).Where("id = ?", id)
	if user.Role == "author" {
		write = write.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	result := write.Updates(updates)
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to update post"})
	}
	if result.RowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	read := data.DB.Where("id = ?", id)
	if user.Role == "author" {
		read = read.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	if err := read.First(&existing).Error; err != nil {
		return postQueryError(c, err)
	}
	return c.JSON(http.StatusOK, existing)
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
	query := data.DB.Where("id = ?", id)
	if user.Role == "author" {
		query = query.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	result := query.Delete(&core.Post{})
	if result.Error != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to delete post"})
	}
	if result.RowsAffected == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
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
	} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		switch key {
		case "title", "slug", "content", "type", "status", "template":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			if key == "type" && value != "post" && value != "page" {
				return nil, errors.New("invalid content type")
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
