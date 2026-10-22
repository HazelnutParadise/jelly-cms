package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// httpShutdownTimeout bounds how long in-flight requests may drain after the
// caller cancels the context. Connections still active after it are closed.
const httpShutdownTimeout = 5 * time.Second

// runHTTPServer serves HTTP until ctx is canceled or the server stops. It
// reuses e.Listener when set so tests can bind an ephemeral port; otherwise it
// binds address itself so listen failures are returned unchanged.
//
// On cancellation it stops accepting new connections, drains in-flight
// requests for at most httpShutdownTimeout, then force-closes and returns the
// shutdown error. A normal shutdown returns nil.
func runHTTPServer(ctx context.Context, e *echo.Echo, address string) error {
	if e == nil {
		return errors.New("run HTTPS server: nil echo instance")
	}

	if e.Listener == nil {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		e.Listener = listener
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- e.Start(address)
	}()

	select {
	case err := <-serveErr:
		return normalizeServeError(err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		closeErr := e.Close()
		// Always wait for Serve to return so no goroutine outlives the call.
		<-serveErr

		shutdownFailure := fmt.Errorf("shut down the HTTP server: %w", err)
		if closeErr != nil {
			return errors.Join(shutdownFailure, fmt.Errorf("force close the HTTP server: %w", closeErr))
		}
		return shutdownFailure
	}

	return normalizeServeError(<-serveErr)
}

// normalizeServeError treats the sentinel Serve returns after Shutdown or
// Close as a normal stop.
func normalizeServeError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
