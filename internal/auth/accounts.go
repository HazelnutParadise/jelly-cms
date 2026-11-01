package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/integration"
)

type loginDevice struct {
	label    string
	uaHash   string
	ipMasked string
}

func loginDeviceFromRequest(request *http.Request) loginDevice {
	if request == nil {
		return loginDevice{}
	}
	ua := strings.TrimSpace(request.UserAgent())
	device := loginDevice{label: describeUserAgent(ua), ipMasked: maskClientIP(request.RemoteAddr)}
	if ua != "" {
		sum := sha256.Sum256([]byte(ua))
		device.uaHash = hex.EncodeToString(sum[:])
	}
	return device
}

func describeUserAgent(ua string) string {
	if ua == "" {
		return "Unknown device"
	}
	lower := strings.ToLower(ua)
	browser := "Browser"
	switch {
	case strings.Contains(lower, "edg/"):
		browser = "Edge"
	case strings.Contains(lower, "firefox/"):
		browser = "Firefox"
	case strings.Contains(lower, "opr/") || strings.Contains(lower, "opera"):
		browser = "Opera"
	case strings.Contains(lower, "chrome/") || strings.Contains(lower, "chromium/"):
		browser = "Chrome"
	case strings.Contains(lower, "safari/"):
		browser = "Safari"
	}
	os := "Unknown OS"
	switch {
	case strings.Contains(lower, "windows"):
		os = "Windows"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ios"):
		os = "iOS"
	case strings.Contains(lower, "android"):
		os = "Android"
	case strings.Contains(lower, "mac os") || strings.Contains(lower, "macintosh"):
		os = "macOS"
	case strings.Contains(lower, "linux"):
		os = "Linux"
	}
	return browser + " on " + os
}

func maskClientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.Trim(remote, "[]")
	}
	return integration.MaskIP(host)
}
