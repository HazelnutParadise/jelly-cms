package server

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/labstack/echo/v4"
)

// redirectMiddleware runs before routing. It answers GET and HEAD requests that
// match a redirect rule, and records GET misses in the 404 log. Paths that belong
// to the application (admin, API and the like) are left alone.
func redirectMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		path := req.URL.Path
		isRead := req.Method == http.MethodGet || req.Method == http.MethodHead
		if !isRead || data.DB == nil || redirects.IsReservedPath(path) {
			return next(c)
		}
		rule, ok, err := redirects.MatchContext(req.Context(), path)
		switch {
		case err != nil:
			log.Printf("redirects: lookup %q failed: %v", path, err)
		case ok:
			return serveRedirect(c, rule)
		}
		err = next(c)
		if req.Method == http.MethodGet && isNotFoundResponse(c, err) {
			recordMiss(c, path)
		}
		return err
	}
}

// serveRedirect answers with the rule's status and target. A 410 rule has no target.
func serveRedirect(c echo.Context, rule *redirects.Redirect) error {
	redirects.CountHit(rule.FromPath)
	if rule.Kind == redirects.Gone {
		return c.NoContent(http.StatusGone)
	}
	target := rule.ToURL
	if rule.KeepQuery {
		if raw := c.Request().URL.RawQuery; raw != "" {
			if strings.Contains(target, "?") {
				target += "&" + raw
			} else {
				target += "?" + raw
			}
		}
	}
	return c.Redirect(rule.Kind, target)
}

// isNotFoundResponse reports whether the handler chain answered 404.
func isNotFoundResponse(c echo.Context, err error) bool {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Code == http.StatusNotFound
	}
	return err == nil && c.Response().Status == http.StatusNotFound
}

// recordMiss counts one miss. A failure is logged and never changes the response.
func recordMiss(c echo.Context, path string) {
	req := c.Request()
	db := data.DB.WithContext(req.Context())
	if err := redirects.RecordNotFound(db, path, redirectReferrerKind(req), redirectUAKind(req.UserAgent())); err != nil {
		log.Printf("redirects: 404 record %q failed: %v", path, err)
	}
}

// redirectReferrerKind sorts the referrer into none, internal or external.
func redirectReferrerKind(req *http.Request) string {
	ref := req.Referer()
	if ref == "" {
		return "none"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return "external"
	}
	if strings.EqualFold(u.Host, req.Host) {
		return "internal"
	}
	return "external"
}

// redirectUAKind sorts the user agent into bot, browser or other.
func redirectUAKind(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case ua == "":
		return "other"
	case strings.Contains(lower, "bot"), strings.Contains(lower, "crawl"),
		strings.Contains(lower, "spider"), strings.Contains(lower, "slurp"):
		return "bot"
	case strings.Contains(lower, "mozilla"):
		return "browser"
	}
	return "other"
}

// publicPostPath is the public URL of a post or page under the default
// permalinks: /post/{slug} for posts and /{slug} for pages.
func publicPostPath(postType, slug string) string {
	if postType == "page" {
		return "/" + slug
	}
	return "/post/" + slug
}
