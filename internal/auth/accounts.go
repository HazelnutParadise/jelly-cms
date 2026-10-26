package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
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
	return agent.MaskIP(host)
}

// touchTokenUse records only coarse activity. The principal loader calls this
// helper after resolving a token; repeated API traffic writes at most once per
// minute per token.
func touchTokenUse(db *gorm.DB, tokenID uint, remote string) error {
	if db == nil || tokenID == 0 {
		return nil
	}
	now := time.Now()
	return db.Model(&core.APIToken{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", tokenID, now.Add(-time.Minute)).
		Updates(map[string]any{"last_used_at": now, "last_used_ip_masked": maskClientIP(remote)}).Error
}
