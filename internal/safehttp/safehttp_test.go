package safehttp

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripFunc replaces the transport so body limits can be checked without
// reaching the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSafeHTTPBlocksPrivateLoopbackLinkLocalCGNAT(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "100.64.0.1", "::1"} {
		if !BlockedIP(net.ParseIP(s)) {
			t.Errorf("BlockedIP(%s) = false, want true", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1"} {
		if BlockedIP(net.ParseIP(s)) {
			t.Errorf("BlockedIP(%s) = true, want false", s)
		}
	}

	c := Client(Options{Timeout: 2 * time.Second})
	for _, raw := range []string{
		"http://127.0.0.1:1/",
		"http://10.0.0.1:1/",
		"http://169.254.1.1:1/",
		"http://100.64.0.1:1/",
		"http://[::1]:1/",
	} {
		resp, err := c.Get(raw)
		if err == nil {
			resp.Body.Close()
			t.Errorf("GET %s succeeded, want a blocked-address error", raw)
			continue
		}
		if !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("GET %s error = %v, want ErrBlockedAddress", raw, err)
		}
	}
}

func TestSafeHTTPFollowsAtMostThreeRedirects(t *testing.T) {
	if MaxRedirects != 3 {
		t.Fatalf("MaxRedirects = %d, want 3", MaxRedirects)
	}
	c := Client(Options{})
	if c.CheckRedirect == nil {
		t.Fatal("Client has no CheckRedirect; redirects are not bounded")
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Two earlier requests means this is the third hop: allowed.
	if err := c.CheckRedirect(req, make([]*http.Request, 2)); err != nil {
		t.Errorf("third request (two redirects so far) refused: %v", err)
	}
	// Three earlier requests means this would be the fourth hop: refused.
	if err := c.CheckRedirect(req, make([]*http.Request, 3)); err == nil {
		t.Error("fourth request allowed, want refusal after 3 redirects")
	}

	allowOnly := Client(Options{AllowHosts: []string{"a.example"}})
	other, _ := http.NewRequest(http.MethodGet, "https://b.example/x", nil)
	if err := allowOnly.CheckRedirect(other, make([]*http.Request, 1)); err == nil {
		t.Error("redirect to a host outside AllowHosts allowed")
	}
}

func TestSafeHTTPSizeAndTimeCaps(t *testing.T) {
	if got := Client(Options{}).Timeout; got != 30*time.Second {
		t.Errorf("default Timeout = %s, want 30s", got)
	}
	if got := Client(Options{Timeout: 250 * time.Millisecond}).Timeout; got != 250*time.Millisecond {
		t.Errorf("Timeout = %s, want 250ms", got)
	}

	// A body larger than MaxBytes must not be delivered in full. The client
	// is given a fake transport, so no dial happens.
	const limit = 1024
	big := strings.Repeat("x", 10*1024)
	c := Client(Options{MaxBytes: limit, Timeout: time.Second})
	c.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader([]byte(big))),
			Request:    r,
		}, nil
	})
	resp, err := c.Get("https://example.com/big")
	if err != nil {
		t.Fatalf("GET with fake transport: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if int64(len(body)) > limit {
		t.Errorf("read %d bytes through a client capped at %d", len(body), limit)
	}
}

func TestSafeHTTPDNSRebindBlocked(t *testing.T) {
	// A public-looking hostname that resolves to a private address must be
	// refused at dial time, after resolution. localhost is the stand-in.
	c := Client(Options{Timeout: 2 * time.Second})
	resp, err := c.Get("http://localhost:1/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("GET http://localhost:1/ succeeded; hostname resolving to loopback was dialled")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("error = %v, want ErrBlockedAddress", err)
	}
}
