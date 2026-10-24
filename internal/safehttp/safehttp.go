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
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// MaxRedirects is the number of redirects a client follows.
const MaxRedirects = 3

// ErrBlockedAddress is returned when a dial target is not public.
var ErrBlockedAddress = errors.New("safehttp: destination address is not allowed")

// ErrBodyTooLarge is returned by Read when a response body passes Options.MaxBytes.
var ErrBodyTooLarge = errors.New("safehttp: response body exceeds the size limit")

// allowLoopbackEnv lets tests dial a fake server on 127.0.0.1. Production
// deployments never set it; loopback stays blocked unless it is exactly "1".
const allowLoopbackEnv = "JELLY_TEST_ALLOW_LOOPBACK"

func loopbackAllowed() bool { return os.Getenv(allowLoopbackEnv) == "1" }

// Options bounds one client.
type Options struct {
	Timeout time.Duration
	// MaxBytes limits how much of a response body a caller may read. A body
	// that is longer fails with ErrBodyTooLarge after MaxBytes bytes. Zero
	// means no limit.
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
				if BlockedIP(ip.IP) && !(loopbackAllowed() && ip.IP.IsLoopback()) {
					return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, host)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	var rt http.RoundTripper = transport
	if opts.MaxBytes > 0 {
		rt = &limitTransport{base: transport, max: opts.MaxBytes}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: rt,
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

// limitTransport caps every response body at max bytes.
type limitTransport struct {
	base http.RoundTripper
	max  int64
}

func (t *limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: %d bytes", ErrBodyTooLarge, resp.ContentLength)
	}
	resp.Body = &limitedBody{rc: resp.Body, left: t.max}
	return resp, nil
}

// limitedBody returns ErrBodyTooLarge instead of silently truncating.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// Probe one byte: a clean EOF means the body ended exactly at the limit.
		var one [1]byte
		n, err := b.rc.Read(one[:])
		if n > 0 {
			return 0, ErrBodyTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }
