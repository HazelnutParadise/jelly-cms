package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	httpLifecycleCaseBudget   = 8 * time.Second
	httpLifecycleShutdownWait = 5 * time.Second
)

// httpLifecycleCase bounds every wait in a test so a single case never
// exceeds httpLifecycleCaseBudget.
type httpLifecycleCase struct {
	deadline time.Time
}

func newHTTPLifecycleCase() *httpLifecycleCase {
	return &httpLifecycleCase{deadline: time.Now().Add(httpLifecycleCaseBudget)}
}

func (c *httpLifecycleCase) remaining() time.Duration {
	remaining := time.Until(c.deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (c *httpLifecycleCase) capped(limit time.Duration) time.Duration {
	if remaining := c.remaining(); remaining < limit {
		return remaining
	}
	return limit
}

func (c *httpLifecycleCase) requireBudget(t *testing.T) {
	t.Helper()
	if c.remaining() == 0 {
		t.Fatalf("test case exceeded its %s budget", httpLifecycleCaseBudget)
	}
}

type httpLifecycleServer struct {
	addr     string
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

// startHTTPLifecycleServer binds an ephemeral localhost port, hands the
// listener to Echo, and runs runHTTPServer in the background. Tests must use
// the returned addr, never a fixed port.
func startHTTPLifecycleServer(t *testing.T, clock *httpLifecycleCase, e *echo.Echo) *httpLifecycleServer {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on an ephemeral localhost port: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.Listener = listener

	server := &httpLifecycleServer{
		addr:     listener.Addr().String(),
		listener: listener,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	go func() {
		server.err = runHTTPServer(ctx, e, server.addr)
		close(server.done)
	}()

	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		select {
		case <-server.done:
		case <-time.After(clock.capped(2 * time.Second)):
			t.Errorf("runHTTPServer did not stop during cleanup for %s", server.addr)
		}
	})

	return server
}

func (s *httpLifecycleServer) wait(t *testing.T, clock *httpLifecycleCase, limit time.Duration) error {
	t.Helper()
	clock.requireBudget(t)
	select {
	case <-s.done:
		return s.err
	case <-time.After(clock.capped(limit)):
		t.Fatalf("runHTTPServer did not return within %s", limit)
		return nil
	}
}

// waitForRefusedConnection asserts the server stops accepting new connections.
func waitForRefusedConnection(t *testing.T, clock *httpLifecycleCase, addr string, limit time.Duration) {
	t.Helper()
	clock.requireBudget(t)
	deadline := time.Now().Add(clock.capped(limit))
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		if !time.Now().Before(deadline) {
			t.Fatalf("listener at %s still accepted a new connection after cancellation", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func getHTTPLifecycle(t *testing.T, clock *httpLifecycleCase, addr, path string) (int, string) {
	t.Helper()
	clock.requireBudget(t)

	client := &http.Client{
		Timeout:   clock.capped(4 * time.Second),
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the response body from %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

func TestHTTPServerServesRequestsAndStopsOnContextCancel(t *testing.T) {
	clock := newHTTPLifecycleCase()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	server := startHTTPLifecycleServer(t, clock, e)

	status, body := getHTTPLifecycle(t, clock, server.addr, "/health")
	if status != http.StatusOK {
		t.Fatalf("GET /health returned status %d, want %d", status, http.StatusOK)
	}
	if body != "ok" {
		t.Fatalf("GET /health returned body %q, want %q", body, "ok")
	}

	server.cancel()

	if err := server.wait(t, clock, httpLifecycleShutdownWait); err != nil {
		t.Fatalf("runHTTPServer returned %v after a normal context cancellation, want nil", err)
	}

	waitForRefusedConnection(t, clock, server.addr, time.Second)
}

func TestHTTPServerDrainsInFlightRequestBeforeReturningOnCancel(t *testing.T) {
	clock := newHTTPLifecycleCase()

	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseHandler) })

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/slow", func(c echo.Context) error {
		enteredOnce.Do(func() { close(handlerEntered) })
		<-releaseHandler
		return c.String(http.StatusOK, "finished")
	})

	server := startHTTPLifecycleServer(t, clock, e)

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), clock.capped(5*time.Second))
	defer cancelRequest()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+server.addr+"/slow", nil)
	if err != nil {
		t.Fatalf("build the slow request: %v", err)
	}

	type response struct {
		status int
		body   string
		err    error
	}
	responseCh := make(chan response, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Do(request)
		if err != nil {
			responseCh <- response{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		responseCh <- response{status: resp.StatusCode, body: string(body), err: err}
	}()

	select {
	case <-handlerEntered:
	case <-time.After(clock.capped(3 * time.Second)):
		t.Fatal("the slow request never reached the handler")
	}

	server.cancel()

	select {
	case err := <-server.done:
		t.Fatalf("runHTTPServer returned %v while an HTTP request was still in flight", err)
	case <-time.After(300 * time.Millisecond):
	}

	waitForRefusedConnection(t, clock, server.addr, 2*time.Second)

	releaseOnce.Do(func() { close(releaseHandler) })

	select {
	case got := <-responseCh:
		if got.err != nil {
			t.Fatalf("the in-flight request failed: %v", got.err)
		}
		if got.status != http.StatusOK {
			t.Fatalf("the in-flight request returned status %d, want %d", got.status, http.StatusOK)
		}
		if got.body != "finished" {
			t.Fatalf("the in-flight request returned body %q, want %q", got.body, "finished")
		}
	case <-time.After(clock.capped(3 * time.Second)):
		t.Fatal("the in-flight request did not finish after the handler was released")
	}

	if err := server.wait(t, clock, httpLifecycleShutdownWait); err != nil {
		t.Fatalf("runHTTPServer returned %v after draining the in-flight request, want nil", err)
	}
}

func TestHTTPServerReturnsAndClosesListenerWhenContextIsAlreadyCanceled(t *testing.T) {
	clock := newHTTPLifecycleCase()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on an ephemeral localhost port: %v", err)
	}
	addr := listener.Addr().String()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Listener = listener
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx, e, addr)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHTTPServer returned %v for an already-canceled context, want nil", err)
		}
	case <-time.After(clock.capped(2 * time.Second)):
		// Close the owned test listener before failing so a regression cannot
		// leave runHTTPServer or its Serve goroutine behind.
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(clock.capped(time.Second)):
		}
		t.Fatal("runHTTPServer did not return promptly for an already-canceled context")
	}

	if err := listener.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener at %s was still open after runHTTPServer returned (Close error = %v)", addr, err)
	}
	replacement, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listener at %s remained bound after runHTTPServer returned: %v", addr, err)
	}
	_ = replacement.Close()
}

func TestHTTPServerForceClosesWhenInFlightRequestExceedsShutdownDeadline(t *testing.T) {
	clock := newHTTPLifecycleCase()
	handlerEntered := make(chan struct{})
	handlerExited := make(chan struct{})
	var enteredOnce sync.Once
	var exitedOnce sync.Once

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/wait-for-cancel", func(c echo.Context) error {
		enteredOnce.Do(func() { close(handlerEntered) })
		<-c.Request().Context().Done()
		exitedOnce.Do(func() { close(handlerExited) })
		return nil
	})
	server := startHTTPLifecycleServer(t, clock, e)

	requestCtx, cancelRequest := context.WithTimeout(context.Background(), clock.capped(7*time.Second))
	defer cancelRequest()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+server.addr+"/wait-for-cancel", nil)
	if err != nil {
		t.Fatalf("build the cancellation-aware request: %v", err)
	}
	responseCh := make(chan error, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Do(request)
		if resp != nil {
			_ = resp.Body.Close()
		}
		responseCh <- err
	}()

	select {
	case <-handlerEntered:
	case <-time.After(clock.capped(2 * time.Second)):
		t.Fatal("the cancellation-aware request never reached the handler")
	}

	server.cancel()
	if err := server.wait(t, clock, httpLifecycleShutdownWait+time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runHTTPServer returned %v after the shutdown deadline, want context.DeadlineExceeded", err)
	}

	select {
	case <-handlerExited:
	case <-time.After(clock.capped(time.Second)):
		t.Fatal("the in-flight handler did not exit after the server force-closed its connection")
	}
	select {
	case <-responseCh:
	case <-time.After(clock.capped(time.Second)):
		t.Fatal("the in-flight HTTP client did not finish after force close")
	}
}

func TestHTTPServerReturnsListenErrorWhenAddressIsTaken(t *testing.T) {
	clock := newHTTPLifecycleCase()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy an ephemeral localhost port: %v", err)
	}
	defer occupied.Close()

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- runHTTPServer(ctx, e, occupied.Addr().String())
	}()

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatalf("runHTTPServer returned nil when %s was already in use", occupied.Addr())
		}
	case <-time.After(clock.capped(3 * time.Second)):
		t.Fatalf("runHTTPServer did not return after failing to listen on %s", occupied.Addr())
	}
}
