package auth

import (
	"net/url"
	"strings"
	"unicode"
)

// SafeRedirectURL returns a safe root-relative URL, or /admin when raw is invalid.
func SafeRedirectURL(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/admin"
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == '\\' {
			return "/admin"
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return "/admin"
	}
	if _, err := url.QueryUnescape(u.RawQuery); err != nil {
		return "/admin"
	}
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsRune(u.Path, '\\') {
		return "/admin"
	}
	for _, r := range u.Path {
		if unicode.IsControl(r) {
			return "/admin"
		}
	}

	return u.String()
}
