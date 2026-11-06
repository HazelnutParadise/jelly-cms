package server

import (
	"github.com/labstack/echo/v4"
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
	panic("ticket 27: integrationPageParams not implemented")
}

// integrationPaged builds the paged envelope. A nil items slice is written as [].
func integrationPaged(items any, total int64, p integrationPage) map[string]any {
	panic("ticket 27: integrationPaged not implemented")
}

// integrationCollection builds the collection envelope. A nil items slice is written as [].
func integrationCollection(items any) map[string]any {
	panic("ticket 27: integrationCollection not implemented")
}

// integrationContentSummary is the shape of a post or page inside a list. The
// json tags match core.Post. content, source and author are never part of it.
type integrationContentSummary struct {
	// Fields are filled in by the implementation (ticket 27, 名詞「內容摘要」).
}

// integrationContentSummaryFields lists the JSON keys of integrationContentSummary
// in struct order; the OpenAPI schema is generated from it.
func integrationContentSummaryFields() []string {
	panic("ticket 27: integrationContentSummaryFields not implemented")
}

// integrationListContent lists posts or pages (kind is "post" or "page") with
// the paged envelope, the status and q filters, created_at DESC ordering, the
// author scope and the trash excluded (ticket 27 R2).
func integrationListContent(kind string) echo.HandlerFunc {
	return func(c echo.Context) error { panic("ticket 27: integrationListContent not implemented") }
}

func integrationListCategories(c echo.Context) error {
	panic("ticket 27: integrationListCategories not implemented")
}

func integrationListTags(c echo.Context) error {
	panic("ticket 27: integrationListTags not implemented")
}

func integrationListPlugins(c echo.Context) error {
	panic("ticket 27: integrationListPlugins not implemented")
}

func integrationListSettings(c echo.Context) error {
	panic("ticket 27: integrationListSettings not implemented")
}

// integrationListMediaFiles validates page and per_page strictly, then defers to
// handleMediaFileList whose response shape is already the paged envelope.
func integrationListMediaFiles(c echo.Context) error {
	panic("ticket 27: integrationListMediaFiles not implemented")
}
