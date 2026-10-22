package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func TestRequestLoggerOAuthQueryDoesNotChangeRequestOrResponse(t *testing.T) {
	for _, provider := range []string{"google", "github"} {
		t.Run(provider, func(t *testing.T) {
			e, logs := newRequestLoggerTestEcho()
			var received requestLoggerRequest
			expectedBody := ""
			expectedResponse := `{"token":"response-token-independent"}`
			e.GET("/auth/"+provider+"/callback", func(c echo.Context) error {
				received = captureRequest(c)
				return c.JSONBlob(http.StatusOK, []byte(expectedResponse))
			})

			query := url.Values{
				"code":  {provider + "-fake-code-8fa9"},
				"state": {provider + "-fake-state-14b2"},
			}
			requestURI := "/auth/" + provider + "/callback?" + query.Encode()
			req := httptest.NewRequest(http.MethodGet, requestURI, nil)
			req.Header.Set("Authorization", "Bearer auth-secret-27cd")
			req.Header.Set("Referer", "https://source.invalid/page?ref=referer-secret-68de")
			req.AddCookie(&http.Cookie{Name: "session", Value: "cookie-secret-38ef"})
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)

			if res.Code != http.StatusOK || res.Body.String() != expectedResponse {
				t.Fatalf("response changed: status=%d body=%q", res.Code, res.Body.String())
			}
			if got := res.Header().Get(echo.HeaderContentType); got != echo.MIMEApplicationJSON {
				t.Fatalf("Content-Type changed: got %q", got)
			}
			want := requestLoggerRequest{
				requestURI:    requestURI,
				rawQuery:      query.Encode(),
				queryCode:     provider + "-fake-code-8fa9",
				queryState:    provider + "-fake-state-14b2",
				body:          expectedBody,
				authorization: "Bearer auth-secret-27cd",
				referer:       "https://source.invalid/page?ref=referer-secret-68de",
				cookie:        "cookie-secret-38ef",
			}
			if received != want {
				t.Fatalf("handler request changed or was not fully available:\n got %#v\nwant %#v", received, want)
			}
			entry := decodeSingleRequestLog(t, logs.String())
			assertRequestLogFields(t, entry, req.URL.Path, http.MethodGet, http.StatusOK)
			assertLogOmits(t, logs.String(), query.Get("code"), query.Get("state"), "cookie-secret-38ef", "auth-secret-27cd", "referer-secret-68de", "response-token-independent")
		})
	}
}

func TestRequestLoggerErrorAndRedirectResponses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		message  string
		location string
		wantBody string
	}{
		{name: "bad request", status: http.StatusBadRequest, message: "invalid request", wantBody: "{\"message\":\"invalid request\"}\n"},
		{name: "forbidden", status: http.StatusForbidden, message: "access denied", wantBody: "{\"message\":\"access denied\"}\n"},
		{name: "internal error", status: http.StatusInternalServerError, message: "request failed", wantBody: "{\"message\":\"request failed\"}\n"},
		{name: "redirect", status: http.StatusFound, location: "/login?next=%2Fadmin%3Freturn-secret%3Donly-in-response", wantBody: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, logs := newRequestLoggerTestEcho()
			var errorHandlerCalls int
			originalHTTPErrorHandler := e.HTTPErrorHandler
			e.HTTPErrorHandler = func(err error, c echo.Context) {
				errorHandlerCalls++
				originalHTTPErrorHandler(err, c)
			}
			e.GET("/result", func(c echo.Context) error {
				if tc.location != "" {
					return c.Redirect(tc.status, tc.location)
				}
				return echo.NewHTTPError(tc.status, tc.message)
			})
			querySecret := "query-secret-" + strings.ReplaceAll(tc.name, " ", "-") + "-a31f"
			requestURI := "/result?secret=" + querySecret + "&again=" + querySecret
			res := httptest.NewRecorder()
			e.ServeHTTP(res, httptest.NewRequest(http.MethodGet, requestURI, nil))
			if res.Code != tc.status || res.Body.String() != tc.wantBody {
				t.Fatalf("response changed: status=%d body=%q; want status=%d body=%q", res.Code, res.Body.String(), tc.status, tc.wantBody)
			}
			if tc.location != "" && res.Header().Get(echo.HeaderLocation) != tc.location {
				t.Fatalf("Location changed: got %q want %q", res.Header().Get(echo.HeaderLocation), tc.location)
			}
			wantErrorHandlerCalls := 1
			if tc.status == http.StatusFound {
				wantErrorHandlerCalls = 0
			}
			if errorHandlerCalls != wantErrorHandlerCalls {
				t.Errorf("HTTPErrorHandler call count changed: got %d want %d", errorHandlerCalls, wantErrorHandlerCalls)
			}
			entry := decodeSingleRequestLog(t, logs.String())
			assertRequestLogFields(t, entry, "/result", http.MethodGet, tc.status)
			assertLogOmits(t, logs.String(), querySecret)
			if tc.location != "" {
				assertLogOmits(t, logs.String(), "return-secret", "only-in-response")
			}
			if tc.message != "" && entry["error"] != fmt.Sprintf("code=%d, message=%s", tc.status, tc.message) {
				t.Errorf("existing generic error field changed: got %#v want %q", entry["error"], tc.message)
			}
		})
	}
}

func TestRequestLoggerAllRoutesAndMalformedQueriesUsePath(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		rawQuery string
		status   int
	}{
		{name: "search", path: "/search", rawQuery: "q=ordinary-search-secret-5ca1", status: http.StatusOK},
		{name: "repeated query", path: "/search", rawQuery: "q=first-secret-19b2&q=second-secret-4cd3", status: http.StatusOK},
		{name: "malformed escape", path: "/search", rawQuery: "q=%GG-malformed-secret-77ef", status: http.StatusOK},
		{name: "separator characters", path: "/search", rawQuery: "q=semi%3Bcolon%26ampersand-secret-81a0&state=other-secret-91b1", status: http.StatusOK},
		{name: "unknown route", path: "/missing", rawQuery: "code=unknown-route-secret-25ce", status: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, logs := newRequestLoggerTestEcho()
			e.GET("/search", func(c echo.Context) error { return c.String(http.StatusOK, "search-result") })
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.URL.RawQuery = tc.rawQuery
			req.RequestURI = tc.path + "?" + tc.rawQuery
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status changed: got %d want %d", res.Code, tc.status)
			}
			entry := decodeSingleRequestLog(t, logs.String())
			assertRequestLogFields(t, entry, tc.path, http.MethodGet, tc.status)
			for _, pair := range strings.Split(tc.rawQuery, "&") {
				if key, value, ok := strings.Cut(pair, "="); ok && value != "" {
					assertLogOmits(t, logs.String(), key+"="+value, value)
				}
			}
		})
	}
}

func TestRequestLoggerEscapesPathAsOneJSONRecord(t *testing.T) {
	e, logs := newRequestLoggerTestEcho()
	var handlerPath string
	e.GET("/*", func(c echo.Context) error {
		handlerPath = c.Request().URL.Path
		return c.NoContent(http.StatusNoContent)
	})
	encodedPath := "/escaped/%22/%0A/%E8%B2%93%E5%92%AA"
	req := httptest.NewRequest(http.MethodGet, encodedPath+"?state=path-secret-351b", nil)
	res := httptest.NewRecorder()
	e.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status changed: got %d", res.Code)
	}
	if handlerPath != req.URL.Path {
		t.Fatalf("handler path changed: got %q want %q", handlerPath, req.URL.Path)
	}
	entry := decodeSingleRequestLog(t, logs.String())
	assertRequestLogFields(t, entry, req.URL.Path, http.MethodGet, http.StatusNoContent)
	assertLogOmits(t, logs.String(), "path-secret-351b")
}

func TestRequestLoggerDoesNotAddSensitiveRequestOrResponseFields(t *testing.T) {
	e, logs := newRequestLoggerTestEcho()
	var received requestLoggerRequest
	responseBody := `{"access_token":"success-response-secret-c04f"}`
	e.POST("/submit", func(c echo.Context) error {
		received = captureRequest(c)
		return c.JSONBlob(http.StatusOK, []byte(responseBody))
	})
	body := `{"password":"request-body-secret-a16c"}`
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer authorization-secret-d734")
	req.Header.Set("Referer", "https://example.invalid/?referer-secret-e815")
	req.AddCookie(&http.Cookie{Name: "session", Value: "cookie-secret-f926"})
	res := httptest.NewRecorder()
	e.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != responseBody {
		t.Fatalf("response changed: status=%d body=%q", res.Code, res.Body.String())
	}
	if received.body != body || received.authorization != "Bearer authorization-secret-d734" || received.referer != "https://example.invalid/?referer-secret-e815" || received.cookie != "cookie-secret-f926" {
		t.Fatalf("handler could not read original sensitive inputs: %#v", received)
	}
	entry := decodeSingleRequestLog(t, logs.String())
	assertRequestLogFields(t, entry, "/submit", http.MethodPost, http.StatusOK)
	assertLogOmits(t, logs.String(), "request-body-secret-a16c", "authorization-secret-d734", "referer-secret-e815", "cookie-secret-f926", "success-response-secret-c04f")
}

func TestRequestLoggerInstancesKeepLogsSeparateAndDefaultFormat(t *testing.T) {
	before := middleware.DefaultLoggerConfig
	first, firstLogs := newRequestLoggerTestEcho()
	second, secondLogs := newRequestLoggerTestEcho()
	first.GET("/first", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	second.GET("/second", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	serveNoContent := func(e *echo.Echo, path string) {
		t.Helper()
		res := httptest.NewRecorder()
		e.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusNoContent {
			t.Fatalf("%s status changed: got %d", path, res.Code)
		}
	}
	serveNoContent(first, "/first")
	if strings.Contains(firstLogs.String(), "/second") || secondLogs.Len() != 0 {
		t.Fatalf("first Echo log crossed instance boundary: first=%q second=%q", firstLogs.String(), secondLogs.String())
	}
	serveNoContent(second, "/second")
	if strings.Contains(secondLogs.String(), "/first") || strings.Contains(firstLogs.String(), "/second") {
		t.Fatalf("Echo logs crossed instance boundary: first=%q second=%q", firstLogs.String(), secondLogs.String())
	}
	assertRequestLogFields(t, decodeSingleRequestLog(t, firstLogs.String()), "/first", http.MethodGet, http.StatusNoContent)
	assertRequestLogFields(t, decodeSingleRequestLog(t, secondLogs.String()), "/second", http.MethodGet, http.StatusNoContent)
	after := middleware.DefaultLoggerConfig
	if before.Format != after.Format || before.CustomTimeFormat != after.CustomTimeFormat || before.Output != after.Output {
		t.Fatalf("middleware.DefaultLoggerConfig public values changed: before=(%q,%q,%T) after=(%q,%q,%T)", before.Format, before.CustomTimeFormat, before.Output, after.Format, after.CustomTimeFormat, after.Output)
	}
}

type requestLoggerRequest struct {
	requestURI    string
	rawQuery      string
	queryCode     string
	queryState    string
	body          string
	authorization string
	referer       string
	cookie        string
}

func captureRequest(c echo.Context) requestLoggerRequest {
	body, _ := io.ReadAll(c.Request().Body)
	c.Request().Body = io.NopCloser(bytes.NewReader(body))
	cookie, _ := c.Cookie("session")
	cookieValue := ""
	if cookie != nil {
		cookieValue = cookie.Value
	}
	return requestLoggerRequest{
		requestURI:    c.Request().RequestURI,
		rawQuery:      c.Request().URL.RawQuery,
		queryCode:     c.QueryParam("code"),
		queryState:    c.QueryParam("state"),
		body:          string(body),
		authorization: c.Request().Header.Get("Authorization"),
		referer:       c.Request().Referer(),
		cookie:        cookieValue,
	}
}

func newRequestLoggerTestEcho() (*echo.Echo, *bytes.Buffer) {
	e := echo.New()
	logs := &bytes.Buffer{}
	e.Logger.SetOutput(logs)
	e.Use(requestLogger())
	return e, logs
}

func decodeSingleRequestLog(t *testing.T, output string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var entry map[string]any
	if err := decoder.Decode(&entry); err != nil {
		t.Fatalf("request logger did not emit one valid JSON object: %v; output=%q", err, output)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("request logger emitted more than one JSON value or trailing garbage: extra=%#v err=%v output=%q", extra, err, output)
	}
	return entry
}

func assertRequestLogFields(t *testing.T, entry map[string]any, path, method string, status int) {
	t.Helper()
	if got, ok := entry["uri"].(string); !ok || got != path {
		t.Errorf("log uri must be the request URL path only: got %#v want %q", entry["uri"], path)
	}
	if got := entry["method"]; got != method {
		t.Errorf("log method changed: got %#v want %q", got, method)
	}
	if got := entry["status"]; got != float64(status) {
		t.Errorf("log status changed: got %#v want %d", got, status)
	}
	if got, ok := entry["time"].(string); !ok || got == "" {
		t.Errorf("log time field missing or invalid: %#v", entry["time"])
	}
	if got, ok := entry["latency"].(float64); !ok || got < 0 {
		t.Errorf("log latency field missing or invalid: %#v", entry["latency"])
	}
}

func assertLogOmits(t *testing.T, output string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(output, secret) {
			t.Errorf("request log leaked secret %q: %s", secret, fmt.Sprintf("%q", output))
		}
	}
}
