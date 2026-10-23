package auth

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	protectionErrorForbidden   = "Request origin is not allowed"
	protectionErrorUnavailable = "Request protection is unavailable"
)

func ProtectBrowserWrites(next echo.HandlerFunc) echo.HandlerFunc {
	return protectWrites(next, func(*http.Request) bool { return true })
}

func ProtectCookieWrites(next echo.HandlerFunc) echo.HandlerFunc {
	return protectWrites(next, hasLoginCookie)
}

func ProtectLogoutWrites(next echo.HandlerFunc) echo.HandlerFunc {
	return protectWrites(next, func(r *http.Request) bool {
		return hasLoginCookie(r) || hasSourceHeader(r)
	})
}

func protectWrites(next echo.HandlerFunc, required func(*http.Request) bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		method := c.Request().Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || !required(c.Request()) {
			return next(c)
		}

		target, ok := requestProtectionTarget(c.Request())
		if !ok {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": protectionErrorUnavailable})
		}
		if !requestHasValidSource(c.Request(), target) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": protectionErrorForbidden})
		}
		return next(c)
	}
}

func hasLoginCookie(r *http.Request) bool {
	for _, cookie := range r.Cookies() {
		if cookie.Name == "jwt_token" || cookie.Name == "jelly-cms-session" {
			return true
		}
	}
	return false
}

func hasSourceHeader(r *http.Request) bool {
	return headerPresent(r.Header, "Origin") || headerPresent(r.Header, "Referer") || headerPresent(r.Header, "Sec-Fetch-Site")
}

func requestProtectionTarget(r *http.Request) (string, bool) {
	if appURL := os.Getenv("APP_URL"); appURL != "" {
		return parseTargetOrigin(appURL)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return normalizeOrigin(scheme, r.Host)
}

func parseTargetOrigin(raw string) (string, bool) {
	if strings.TrimSpace(raw) != raw || strings.Contains(raw, "#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	return normalizeOrigin(u.Scheme, u.Host)
}

func requestHasValidSource(r *http.Request, target string) bool {
	validProof := false

	if values, present := headerValues(r.Header, "Origin"); present {
		if len(values) != 1 || values[0] == "" {
			return false
		}
		origin, ok := parseSourceOrigin(values[0], false)
		if !ok || origin != target {
			return false
		}
		validProof = true
	}

	if values, present := headerValues(r.Header, "Referer"); present {
		if len(values) != 1 || values[0] == "" {
			return false
		}
		origin, ok := parseSourceOrigin(values[0], true)
		if !ok || origin != target {
			return false
		}
		validProof = true
	}

	if values, present := headerValues(r.Header, "Sec-Fetch-Site"); present {
		if len(values) != 1 || values[0] == "" {
			return false
		}
		switch values[0] {
		case "same-origin":
			validProof = true
		case "none":
		default:
			return false
		}
	}

	if values, present := headerValues(r.Header, "X-Jelly-CSRF"); present {
		if len(values) != 1 || values[0] != "1" {
			return false
		}
		validProof = true
	}

	return validProof
}

func parseSourceOrigin(raw string, allowPathQuery bool) (string, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Contains(raw, "#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return "", false
	}
	if !allowPathQuery && (u.Path != "" || u.RawQuery != "" || u.ForceQuery) {
		return "", false
	}
	return normalizeOrigin(u.Scheme, u.Host)
}

func normalizeOrigin(scheme, authority string) (string, bool) {
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" || authority == "" || strings.TrimSpace(authority) != authority || strings.ContainsAny(authority, "/?#@\\%") || strings.HasSuffix(authority, ":") {
		return "", false
	}
	u, err := url.Parse("//" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	}
	port := u.Port()
	if strings.Contains(authority, ":") && port == "" && net.ParseIP(host) == nil {
		// A colon in a DNS authority must introduce a port.
		if !strings.HasPrefix(authority, "[") {
			return "", false
		}
	}
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 0 || p > 65535 {
			return "", false
		}
		port = strconv.Itoa(p)
		if (scheme == "http" && p == 80) || (scheme == "https" && p == 443) {
			port = ""
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + host, true
}

func headerPresent(header http.Header, name string) bool {
	_, present := headerValues(header, name)
	return present
}

func headerValues(header http.Header, name string) ([]string, bool) {
	var values []string
	present := false
	for key, current := range header {
		if strings.EqualFold(key, name) {
			present = true
			values = append(values, current...)
		}
	}
	return values, present
}
