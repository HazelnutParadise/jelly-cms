package server

import "github.com/labstack/echo/v4"

// Error envelope shared by the agent API (docs/tickets/14-agent-api.md, 錯誤格式)
// and the WebMCP work in issue #7:
//
//	{"code": "...", "message": "...", "details": {...}, "correlation_id": "req_..."}
//
// details is always an object; correlation_id equals the X-Request-ID response
// header. This file is the ticket 14 skeleton.

// writeAPIError writes the envelope with status. A nil details map is written
// as {}.
func writeAPIError(c echo.Context, status int, code, message string, details map[string]any) error {
	// TODO(ticket 14): write the envelope and make sure X-Request-ID is set.
	return nil
}

// requestIDFor returns the request's "req_" id, creating it and setting the
// X-Request-ID response header on first use. Ticket 08 replaces the generator
// with its RequestID middleware.
func requestIDFor(c echo.Context) string {
	// TODO(ticket 14)
	return ""
}
