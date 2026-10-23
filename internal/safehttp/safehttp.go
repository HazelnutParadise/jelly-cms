// Package safehttp makes outbound HTTP requests that cannot reach internal
// networks. The address check runs at dial time, after DNS resolution, so a
// hostname that resolves to a private address is refused as well.
//
// Used by import (WXR attachments), media fetch and update checks.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// MaxRedirects is the number of redirects a client follows.
const MaxRedirects = 3

// ErrBlockedAddress is returned when a dial target is not public.
var ErrBlockedAddress = errors.New("safehttp: destination address is not allowed")

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("safehttp: not implemented")

// Options bounds one client.
type Options struct {
	Timeout time.Duration
	// MaxBytes limits how much of a response body a caller may read; callers
	// wrap bodies with io.LimitReader using this value.
	MaxBytes int64
	// AllowHosts, when set, restricts requests to these host names (lowercase).
	AllowHosts []string
}

// blockedNets lists the ranges a fetch may not reach.
var blockedNets = mustNets(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
)

func mustNets(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// BlockedIP reports whether ip is loopback, private, link-local, CGNAT,
// multicast, unspecified or otherwise internal.
func BlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Client returns an HTTP client whose dialer refuses blocked addresses and
// which follows at most MaxRedirects redirects.
func Client(opts Options) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("safehttp: no address for %s", host)
			}
			for _, ip := range ips {
				if BlockedIP(ip.IP) {
					return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, host)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("safehttp: stopped after %d redirects", MaxRedirects)
			}
			if len(opts.AllowHosts) > 0 && !hostAllowed(req.URL.Hostname(), opts.AllowHosts) {
				return fmt.Errorf("safehttp: redirect to %s is not allowed", req.URL.Hostname())
			}
			return nil
		},
	}
}

func hostAllowed(host string, allow []string) bool {
	for _, a := range allow {
		if host == a {
			return true
		}
	}
	return false
}
