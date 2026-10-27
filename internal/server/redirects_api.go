package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"github.com/labstack/echo/v4"
)

// Limits for the redirect and 404 API (ticket 08b).
const (
	notFoundDefaultDays    = 30
	notFoundMaxDays        = 365
	redirectImportMaxBytes = 1 << 20
)

// registerRedirectRoutes mounts the admin redirect and 404 API. Every route is
// admin only. The import is also interactive only.
func registerRedirectRoutes(e *echo.Echo) {
	admin := auth.RequireRole("admin")
	e.GET("/api/admin/redirects", handleRedirectList, admin)
	e.POST("/api/admin/redirects", handleRedirectCreate, admin)
	e.PUT("/api/admin/redirects/:id", handleRedirectUpdate, admin)
	e.DELETE("/api/admin/redirects/:id", handleRedirectDelete, admin)
	e.POST("/api/admin/redirects/test", handleRedirectTest, admin)
	e.POST("/api/admin/redirects/import", handleRedirectImport, auth.RequireInteractive(), admin)
	e.GET("/api/admin/redirects/export.csv", handleRedirectExport, admin)
	e.GET("/api/admin/not-found", handleNotFoundList, admin)
	e.POST("/api/admin/not-found/:id/ignore", handleNotFoundIgnore, admin)
	e.DELETE("/api/admin/not-found", handleNotFoundClear, admin)
}

// redirectInput is the JSON a rule is written from. Server-owned fields such as
// id, source and hits are not read from the request.
type redirectInput struct {
	FromPath  string     `json:"from_path"`
	ToURL     string     `json:"to_url"`
	Kind      int        `json:"kind"`
	IsRule    bool       `json:"is_rule"`
	KeepQuery bool       `json:"keep_query"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func decodeRedirectInput(c echo.Context) (redirects.Redirect, error) {
	var in redirectInput
	if err := json.NewDecoder(c.Request().Body).Decode(&in); err != nil {
		return redirects.Redirect{}, err
	}
	return redirects.Redirect{
		FromPath:  in.FromPath,
		ToURL:     in.ToURL,
		Kind:      in.Kind,
		IsRule:    in.IsRule,
		KeepQuery: in.KeepQuery,
		ExpiresAt: in.ExpiresAt,
	}, nil
}

func redirectQueryInt(c echo.Context, key string, fallback int) int {
	v := c.QueryParam(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func parseRedirectID(raw string) (uint, bool) {
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint(n), true
}

// redirectWriteError maps a save failure to a status. Rule problems are the
// caller's fault and are shown; anything else is logged and hidden.
func redirectWriteError(c echo.Context, err error) error {
	var ruleErr *redirects.RuleError
	switch {
	case errors.As(err, &ruleErr):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": ruleErr.Msg})
	case errors.Is(err, redirects.ErrDuplicate):
		return c.JSON(http.StatusConflict, map[string]string{"error": "A redirect already uses this path"})
	case errors.Is(err, redirects.ErrNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Redirect not found"})
	}
	log.Printf("redirects: save failed: %v", err)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to save redirect"})
}

func handleRedirectList(c echo.Context) error {
	query := redirects.ListQuery{
		Source:  c.QueryParam("source"),
		Query:   strings.TrimSpace(c.QueryParam("q")),
		Page:    redirectQueryInt(c, "page", 1),
		PerPage: redirectQueryInt(c, "per_page", 50),
	}
	if v := c.QueryParam("kind"); v != "" {
		kind, err := strconv.Atoi(v)
		if err != nil || (kind != redirects.Moved && kind != redirects.Found && kind != redirects.Gone) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "kind must be 301, 302 or 410"})
		}
		query.Kind = kind
	}
	switch query.Source {
	case "", "manual", "auto", "import":
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "source must be manual, auto or import"})
	}
	result, err := redirects.ListRedirects(data.DB, query)
	if err != nil {
		log.Printf("redirects: list failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load redirects"})
	}
	return c.JSON(http.StatusOK, result)
}

func handleRedirectCreate(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	in, err := decodeRedirectInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	saved, err := redirects.CreateRule(data.DB, in, "manual", user.ID)
	if err != nil {
		return redirectWriteError(c, err)
	}
	syslog.Audit(c.Request().Context(), user.ID, "redirect.create", map[string]any{
		"id": saved.ID, "from_path": saved.FromPath, "kind": saved.Kind,
	})
	return c.JSON(http.StatusCreated, saved)
}

func handleRedirectUpdate(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseRedirectID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Redirect not found"})
	}
	in, err := decodeRedirectInput(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	saved, err := redirects.UpdateRule(data.DB, id, in)
	if err != nil {
		return redirectWriteError(c, err)
	}
	syslog.Audit(c.Request().Context(), user.ID, "redirect.update", map[string]any{
		"id": saved.ID, "from_path": saved.FromPath, "kind": saved.Kind,
	})
	return c.JSON(http.StatusOK, saved)
}

func handleRedirectDelete(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseRedirectID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Redirect not found"})
	}
	if err := redirects.DeleteRule(data.DB, id); err != nil {
		return redirectWriteError(c, err)
	}
	syslog.Audit(c.Request().Context(), user.ID, "redirect.delete", map[string]any{"id": id})
	return c.NoContent(http.StatusNoContent)
}

func handleRedirectTest(c echo.Context) error {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}
	if !strings.HasPrefix(body.Path, "/") {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "path must start with /"})
	}
	trace, err := redirects.TraceChain(data.DB, body.Path)
	if err != nil {
		log.Printf("redirects: trace failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to test path"})
	}
	return c.JSON(http.StatusOK, trace)
}

func handleRedirectImport(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	header, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file is required"})
	}
	if header.Size > redirectImportMaxBytes {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "file is too large"})
	}
	src, err := header.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file cannot be read"})
	}
	defer src.Close()
	rows, err := redirects.ParseRedirectCSV(src)
	if err != nil {
		var ruleErr *redirects.RuleError
		if errors.As(err, &ruleErr) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": ruleErr.Msg})
		}
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file cannot be read"})
	}
	dry := c.QueryParam("dry") == "1"
	report, err := redirects.ImportRedirects(data.DB, rows, dry, user.ID)
	if errors.Is(err, redirects.ErrImportRejected) {
		return c.JSON(http.StatusBadRequest, report)
	}
	if err != nil {
		log.Printf("redirects: import failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to import redirects"})
	}
	if !dry {
		syslog.Audit(c.Request().Context(), user.ID, "redirect.import", map[string]any{
			"created": report.Created, "conflicts": len(report.Conflicts),
		})
	}
	return c.JSON(http.StatusOK, report)
}

func handleRedirectExport(c echo.Context) error {
	var buf bytes.Buffer
	if err := redirects.WriteRedirectCSV(&buf, data.DB); err != nil {
		log.Printf("redirects: export failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to export redirects"})
	}
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename="redirects.csv"`)
	return c.Blob(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

func handleNotFoundList(c echo.Context) error {
	days := redirectQueryInt(c, "days", notFoundDefaultDays)
	if days < 1 || days > notFoundMaxDays {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "days must be between 1 and 365"})
	}
	items, err := redirects.ListNotFound(data.DB, days, c.QueryParam("ignored"))
	if err != nil {
		log.Printf("redirects: 404 list failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load 404 log"})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func handleNotFoundIgnore(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseRedirectID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Not found"})
	}
	if err := redirects.IgnoreNotFound(data.DB, int64(id)); err != nil {
		if errors.Is(err, redirects.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Not found"})
		}
		log.Printf("redirects: 404 ignore failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to ignore 404"})
	}
	syslog.Audit(c.Request().Context(), user.ID, "not_found.ignore", map[string]any{"id": id})
	return c.JSON(http.StatusOK, map[string]any{"id": id, "ignored": true})
}

func handleNotFoundClear(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	removed, err := redirects.ClearNotFound(data.DB)
	if err != nil {
		log.Printf("redirects: 404 clear failed: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to clear 404 log"})
	}
	syslog.Audit(c.Request().Context(), user.ID, "not_found.clear", map[string]any{"removed": removed})
	return c.JSON(http.StatusOK, map[string]any{"deleted": removed})
}
