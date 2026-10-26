package server

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

// trustedProxyExtractorFromEnv applies JELLY_TRUSTED_PROXIES. An invalid value
// is logged and the extractor fails closed to the socket address.
func trustedProxyExtractorFromEnv() echo.IPExtractor {
	extractor, err := TrustedProxyExtractor(os.Getenv("JELLY_TRUSTED_PROXIES"))
	if err != nil {
		log.Printf("ignoring JELLY_TRUSTED_PROXIES: %v; client IPs come from the socket", err)
		return echo.ExtractIPDirect()
	}
	return extractor
}

// TrustedProxyExtractor returns the client IP extractor for a deployment.
//
// An empty spec returns echo.ExtractIPDirect, which ignores forwarding headers
// entirely. A comma-separated list of CIDRs (or single addresses) makes the
// extractor read X-Forwarded-For, but only when the request comes from one of
// the listed proxies; the first untrusted hop from the right is the client.
func TrustedProxyExtractor(spec string) (echo.IPExtractor, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return echo.ExtractIPDirect(), nil
	}
	var options []echo.TrustOption
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("JELLY_TRUSTED_PROXIES: %q is not an IP address or CIDR", item)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			item = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("JELLY_TRUSTED_PROXIES: %q is not a valid CIDR: %w", item, err)
		}
		options = append(options, echo.TrustIPRange(network))
	}
	if len(options) == 0 {
		return echo.ExtractIPDirect(), nil
	}
	return echo.ExtractIPFromXFFHeader(options...), nil
}
