package server

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Ticket 27: the machine API owns its list contracts. Every collection on
// /api/v1 answers an object envelope; paged lists add page and per_page.
//
// Paged envelope:      {"items": [...], "total": N, "page": P, "per_page": S}
// Collection envelope: {"items": [...], "total": N}
// Event envelope (unchanged, /me/events only): {"items": [...], "next_cursor": ..., "total": N}

const (
	integrationDefaultPerPage = 20
	integrationMaxPerPage     = 100
	integrationMaxPage        = 1_000_000
)

// integrationPage is the validated page and per_page pair of a paged list.
type integrationPage struct {
	Page    int
	PerPage int
}

// integrationParamError reports one invalid query parameter. It is written as
// 400 bad_request with details {"param": <name>}.
type integrationParamError struct {
	Param string
}

func (e *integrationParamError) Error() string { return "invalid " + e.Param }

// write answers the request with the ticket 27 parameter error envelope.
func (e *integrationParamError) write(c echo.Context) error {
	return writeAPIError(c, 400, "bad_request", "Invalid "+e.Param, map[string]any{"param": e.Param})
}

// integrationPageParams reads page and per_page. Missing or empty values take
// the defaults; any other non-integer, < 1 or over-limit value is an error.
func integrationPageParams(c echo.Context) (integrationPage, *integrationParamError) {
	p := integrationPage{Page: 1, PerPage: integrationDefaultPerPage}
	if raw := c.QueryParam("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > integrationMaxPage {
			return integrationPage{}, &integrationParamError{Param: "page"}
		}
		p.Page = n
	}
	if raw := c.QueryParam("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > integrationMaxPerPage {
			return integrationPage{}, &integrationParamError{Param: "per_page"}
		}
		p.PerPage = n
	}
	return p, nil
}

// integrationListItems turns a nil slice (or nil) into an empty array so that
// the JSON never carries null for items.
func integrationListItems(items any) any {
	if items == nil {
		return []any{}
	}
	if v := reflect.ValueOf(items); v.Kind() == reflect.Slice && v.IsNil() {
		return []any{}
	}
	return items
}

// integrationPaged builds the paged envelope. A nil items slice is written as [].
func integrationPaged(items any, total int64, p integrationPage) map[string]any {
	return map[string]any{"items": integrationListItems(items), "total": total, "page": p.Page, "per_page": p.PerPage}
}

// integrationCollection builds the collection envelope. A nil items slice is written as [].
func integrationCollection(items any) map[string]any {
	list := integrationListItems(items)
	return map[string]any{"items": list, "total": reflect.ValueOf(list).Len()}
}

// integrationJSONFields lists the JSON keys of a struct value in field order.
func integrationJSONFields(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		names = append(names, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return names
}

// integrationContentSummary is the shape of a post or page inside a list. The
// json tags match core.Post. content, source and author are never part of it.
type integrationContentSummary struct {
	ID            uint            `json:"id"`
	Title         string          `json:"title"`
	Slug          string          `json:"slug"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Visibility    string          `json:"visibility"`
	AuthorID      uint            `json:"author_id"`
	Excerpt       string          `json:"excerpt"`
	Cover         string          `json:"cover"`
	Pinned        bool            `json:"pinned"`
	Format        string          `json:"format"`
	Template      string          `json:"template"`
	PublishedAt   *time.Time      `json:"published_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	AllowComments *bool           `json:"allow_comments"`
	SEOIndex      string          `json:"seo_index"`
	SEOAI         string          `json:"seo_ai"`
	Meta          json.RawMessage `json:"meta"`
	Categories    []core.Category `json:"categories"`
	Tags          []core.Tag      `json:"tags"`
}

// integrationContentSummaryFields lists the JSON keys of integrationContentSummary
// in struct order; the OpenAPI schema is generated from it.
func integrationContentSummaryFields() []string {
	return integrationJSONFields(integrationContentSummary{})
}

// integrationRawOrNull keeps raw JSON encodable: an empty value is written as null.
func integrationRawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func newIntegrationContentSummary(p core.Post) integrationContentSummary {
	categories, tags := p.Categories, p.Tags
	if categories == nil {
		categories = []core.Category{}
	}
	if tags == nil {
		tags = []core.Tag{}
	}
	return integrationContentSummary{
		ID: p.ID, Title: p.Title, Slug: p.Slug, Type: p.Type, Status: p.Status, Visibility: p.Visibility,
		AuthorID: p.AuthorID, Excerpt: p.Excerpt, Cover: p.Cover, Pinned: p.Pinned, Format: p.Format,
		Template: p.Template, PublishedAt: p.PublishedAt, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		AllowComments: p.AllowComments, SEOIndex: p.SEOIndex, SEOAI: p.SEOAI,
		Meta: integrationRawOrNull(p.Meta), Categories: categories, Tags: tags,
	}
}

// integrationListContent lists posts or pages (kind is "post" or "page") with
// the paged envelope, the status and q filters, created_at DESC ordering, the
// author scope and the trash excluded (ticket 27 R2).
func integrationListContent(kind string) echo.HandlerFunc {
	return func(c echo.Context) error {
		p, perr := integrationPageParams(c)
		if perr != nil {
			return perr.write(c)
		}
		user, err := currentUser(c)
		if err != nil {
			return err
		}
		if kind == "page" && user.Role == "author" {
			return c.JSON(http.StatusOK, integrationPaged([]integrationContentSummary{}, 0, p))
		}
		scope := func() *gorm.DB {
			query := data.DB.Model(&core.Post{}).Where("type = ?", kind)
			if user.Role == "author" {
				query = query.Where("author_id = ?", user.ID)
			}
			if status := c.QueryParam("status"); status != "" {
				query = query.Where("status = ?", status)
			}
			if q := c.QueryParam("q"); q != "" {
				query = query.Where("title ILIKE ?", "%"+likeEscaper.Replace(q)+"%")
			}
			return query
		}
		var total int64
		if err := scope().Count(&total).Error; err != nil {
			return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load content", nil)
		}
		var posts []core.Post
		if err := scope().Preload("Categories").Preload("Tags").Order("created_at DESC, id DESC").
			Limit(p.PerPage).Offset((p.Page - 1) * p.PerPage).Find(&posts).Error; err != nil {
			return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load content", nil)
		}
		items := make([]integrationContentSummary, 0, len(posts))
		for _, post := range posts {
			items = append(items, newIntegrationContentSummary(post))
		}
		return c.JSON(http.StatusOK, integrationPaged(items, total, p))
	}
}

func integrationListCategories(c echo.Context) error {
	rows, err := loadCategoryRows(data.DB)
	if err != nil {
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load categories", nil)
	}
	return c.JSON(http.StatusOK, integrationCollection(rows))
}

func integrationListTags(c echo.Context) error {
	tags, err := queryTags(c.QueryParam("q"))
	if err != nil {
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load tags", nil)
	}
	return c.JSON(http.StatusOK, integrationCollection(tags))
}

func integrationListThemes(c echo.Context) error {
	names, err := queryThemes()
	if err != nil {
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load themes", nil)
	}
	return c.JSON(http.StatusOK, integrationCollection(names))
}

func integrationListPlugins(c echo.Context) error {
	plugins, err := queryPlugins()
	if err != nil {
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Unable to load plugins", nil)
	}
	return c.JSON(http.StatusOK, integrationCollection(plugins))
}

func integrationListSettings(c echo.Context) error {
	options, err := querySettings(c)
	if err != nil {
		return writeAPIError(c, http.StatusServiceUnavailable, "service_unavailable", "Service unavailable", nil)
	}
	return c.JSON(http.StatusOK, integrationCollection(options))
}

// integrationListMediaFiles validates page and per_page strictly (ticket 27 R2),
// then answers the shared media list with those values. Passing the defaults as
// arguments keeps the query untouched: the i18n middleware has already cached the
// query parameters of this request, so rewriting the URL here would not be seen.
func integrationListMediaFiles(c echo.Context) error {
	p, perr := integrationPageParams(c)
	if perr != nil {
		return perr.write(c)
	}
	return listMediaFiles(c, p.Page, p.PerPage)
}
