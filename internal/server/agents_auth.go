package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/labstack/echo/v4"
)

func agentCredential(c echo.Context) (string, string) {
	header := c.Request().Header.Get(echo.HeaderAuthorization)
	if header == "" {
		return "", "key_missing"
	}
	if !strings.HasPrefix(header, "Bearer ") || strings.HasPrefix(header, "Bearer "+" ") {
		return "", "key_invalid"
	}
	token := strings.TrimPrefix(header, "Bearer ")
	if len(token) != 47 || !(strings.HasPrefix(token, "jla_") || strings.HasPrefix(token, "jly_")) {
		return "", "key_invalid"
	}
	return token, ""
}

// agentRoleRank orders the roles an agent can be capped to. Roles outside
// admin, editor and author rank 0.
func agentRoleRank(role string) int {
	switch role {
	case "admin":
		return 3
	case "editor":
		return 2
	case "author":
		return 1
	}
	return 0
}

func agentKeyMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		raw, problem := agentCredential(c)
		if problem != "" {
			return writeAPIError(c, 401, problem, "A valid access key is required", nil)
		}
		var key core.AgentKey
		if err := data.DB.WithContext(c.Request().Context()).Where("key_hash = ?", agent.HashKey(raw)).First(&key).Error; err != nil {
			return writeAPIError(c, 401, "key_invalid", "Invalid access key", nil)
		}
		var a core.Agent
		if err := data.DB.WithContext(c.Request().Context()).Unscoped().First(&a, key.AgentID).Error; err != nil {
			return writeAPIError(c, 401, "key_revoked", "Access key is no longer valid", nil)
		}
		if key.RevokedAt != nil {
			return deniedAgentKey(c, a, key, "key_revoked")
		}
		now := time.Now()
		if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
			return deniedAgentKey(c, a, key, "key_expired")
		}
		if a.DeletedAt.Valid {
			return deniedAgentKey(c, a, key, "key_revoked")
		}
		if a.Status != "active" {
			return deniedAgentKey(c, a, key, "agent_disabled")
		}
		var owner core.User
		if a.OwnerID == nil || data.DB.WithContext(c.Request().Context()).First(&owner, *a.OwnerID).Error != nil || owner.Status != "active" {
			return deniedAgentKey(c, a, key, "owner_unavailable")
		}
		// The role cap lowers the role the agent runs with, never raises it.
		// The user copy carries the effective role, so every later check
		// (permissions, author scope) uses it; the owner's own role is kept for /me.
		c.Set("agent.owner_role", owner.Role)
		if a.RoleCap != nil && agentRoleRank(*a.RoleCap) < agentRoleRank(owner.Role) {
			owner.Role = *a.RoleCap
		}
		c.Set("agent.identity", &a)
		c.Set("agent.key", &key)
		c.Set("user", &owner)
		return next(c)
	}
}

func deniedAgentKey(c echo.Context, a core.Agent, key core.AgentKey, code string) error {
	// A matched key's rejected use is auditable; unknown hashes never reach this point.
	ev := newAgentRequestEvent(c, &a, &key)
	record, err := createPendingAgentRequest(ev)
	if err != nil {
		logAgentAuditFailure(ev, 0, 0, "insert")
		return writeAPIError(c, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
	}
	finalizePendingAgentRequest(record, http.StatusUnauthorized, ev.Summary, ev.RequestID, a.ID, key.ID, ev.IPMasked)
	return writeAPIError(c, 401, code, "Access key is not currently usable", nil)
}

func agentPermissionMiddleware(route agentRoute) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			a := c.Get("agent.identity").(*core.Agent)
			owner := c.Get("user").(*core.User)
			want := string(route.Permission)
			allowed := false
			for _, p := range a.Permissions {
				if subtle.ConstantTimeCompare([]byte(p), []byte(want)) == 1 {
					allowed = true
					break
				}
			}
			if !allowed {
				return writeAPIError(c, 403, "permission_missing", "Access key lacks required permission", map[string]any{"missing_permissions": []string{want}, "required_permission": want})
			}
			if !roleListContains(owner.Role, route.RequiredRoles) {
				return writeAPIError(c, 403, "owner_role_insufficient", "Owner role cannot use this permission", map[string]any{"permission": want, "required_roles": route.RequiredRoles, "owner_role": owner.Role})
			}
			return next(c)
		}
	}
}

func agentMe(c echo.Context) error {
	a := c.Get("agent.identity").(*core.Agent)
	k := c.Get("agent.key").(*core.AgentKey)
	owner := c.Get("user").(*core.User)
	ownerRole, _ := c.Get("agent.owner_role").(string)
	perms := []string{}
	for _, p := range a.Permissions {
		if roleListContains(owner.Role, permissionRoles(p)) {
			perms = append(perms, p)
		}
	}
	return c.JSON(200, map[string]any{"id": a.ID, "name": a.Name, "description": a.Description, "permissions": a.Permissions, "effective_permissions": perms, "owner_role": ownerRole, "role_cap": a.RoleCap, "effective_role": owner.Role, "key": map[string]any{"id": k.ID, "prefix": k.Prefix, "expires_at": k.ExpiresAt}})
}

func agentTouchKey(c echo.Context) {
	k := c.Get("agent.key").(*core.AgentKey)
	now := time.Now().UTC()
	if k.LastUsedAt != nil && k.LastUsedAt.After(now.Add(-time.Minute)) {
		return
	}
	_ = data.DB.WithContext(c.Request().Context()).Model(&core.AgentKey{}).Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", k.ID, now.Add(-time.Minute)).Updates(map[string]any{"last_used_at": now, "last_used_ip_masked": agent.MaskIP(c.RealIP())}).Error
}

func withAgentKeyTouch(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := next(c)
		if c.Response().Status >= 200 && c.Response().Status < 300 {
			agentTouchKey(c)
		}
		return err
	}
}

func agentIdentity(c echo.Context) (*core.Agent, *core.AgentKey, *core.User) {
	a, _ := c.Get("agent.identity").(*core.Agent)
	k, _ := c.Get("agent.key").(*core.AgentKey)
	u, _ := c.Get("user").(*core.User)
	return a, k, u
}

func agentReadBody(c echo.Context) ([]byte, error) {
	b, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return nil, err
	}
	c.Request().Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}
func agentBodyTooLarge(err error) bool {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) && httpErr.Code == http.StatusRequestEntityTooLarge {
		return true
	}
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}
func agentRewriteBody(c echo.Context, b []byte) {
	c.Request().Body = io.NopCloser(bytes.NewReader(b))
	c.Request().ContentLength = int64(len(b))
}
func agentTypeAndVersion(pathType string, guard bool, next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if pathType != "" {
			c.Set("agent.content_type", pathType)
		}
		if c.Request().Method == http.MethodGet && strings.HasSuffix(c.Path(), "/posts") || c.Request().Method == http.MethodGet && strings.HasSuffix(c.Path(), "/pages") {
			q := c.Request().URL.Query()
			q.Set("type", pathType)
			c.Request().URL.RawQuery = q.Encode()
		}
		if pathType != "" && (strings.Contains(c.Path(), "posts") || strings.Contains(c.Path(), "pages")) && (strings.Contains(c.Path(), "/:id")) {
			if c.Param("id") != "" {
				id, err := strconv.ParseUint(c.Param("id"), 10, 64)
				if err != nil || id == 0 {
					return writeAPIError(c, 404, "not_found", "Resource not found", nil)
				}
				var typ string
				if err := data.DB.WithContext(c.Request().Context()).Model(&core.Post{}).Select("type").Where("id = ?", id).Scan(&typ).Error; err != nil {
					return writeAPIError(c, 500, "internal_error", "Unable to read content", nil)
				}
				if typ != "" && typ != pathType {
					return writeAPIError(c, 404, "not_found", "Resource not found", nil)
				}
			}
		}
		if pathType != "" && (c.Request().Method == http.MethodPost || c.Request().Method == http.MethodPut) {
			body, err := agentReadBody(c)
			if agentBodyTooLarge(err) {
				return writeAPIError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large", nil)
			}
			if err == nil && len(body) > 0 {
				var fields map[string]json.RawMessage
				if json.Unmarshal(body, &fields) == nil {
					if raw, ok := fields["type"]; ok {
						var got string
						if json.Unmarshal(raw, &got) != nil || got != pathType {
							return writeAPIError(c, 400, "type_mismatch", "Content type does not match this endpoint", nil)
						}
					} else if c.Request().Method == http.MethodPost {
						fields["type"], _ = json.Marshal(pathType)
						body, _ = json.Marshal(fields)
						agentRewriteBody(c, body)
					}
				}
			}
		}
		if guard && c.Request().Method == http.MethodPut {
			body, err := agentReadBody(c)
			if err != nil {
				if agentBodyTooLarge(err) {
					return writeAPIError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large", nil)
				}
				return writeAPIError(c, 400, "bad_request", "Unable to read request body", nil)
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(body, &fields) != nil {
				return writeAPIError(c, 400, "bad_request", "Invalid request body", nil)
			}
			raw, ok := fields["base_updated_at"]
			if !ok {
				return writeAPIError(c, 400, "version_required", "base_updated_at is required", nil)
			}
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return writeAPIError(c, 400, "version_invalid", "base_updated_at must be RFC3339", nil)
			}
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return writeAPIError(c, 400, "version_invalid", "base_updated_at must be RFC3339", nil)
			}
			c.Set("agent.base_updated_at", parsed)
			agentRewriteBody(c, body)
		}
		return next(c)
	}
}

func agentSettingsURLGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if c.Request().Method != http.MethodPost && c.Request().Method != http.MethodPut {
			return next(c)
		}
		b, err := agentReadBody(c)
		if err != nil {
			if agentBodyTooLarge(err) {
				return writeAPIError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large", nil)
			}
			return next(c)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(b, &raw); err != nil {
			return next(c)
		}
		values := make(map[string]string, len(raw))
		for key, value := range raw {
			if !isURLSettingKey(key) {
				continue
			}
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				agentRewriteBody(c, b)
				return writeAPIError(c, 400, "bad_request", "URL settings must be strings", nil)
			}
			values[key] = text
		}
		if e := checkURLSettings(c.Request().Context(), values); e != nil {
			agentRewriteBody(c, b)
			return writeAPIError(c, 400, "bad_request", e.Error(), nil)
		}
		agentRewriteBody(c, b)
		return next(c)
	}
}

func agentErrorEnvelope(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		original := c.Response().Writer
		rec := httptest.NewRecorder()
		c.Response().Writer = rec
		c.Response().Committed = false
		c.Response().Status = 0
		c.Response().Size = 0
		err := next(c)
		status := rec.Code
		if status == 0 {
			status = c.Response().Status
		}
		if err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) && he.Code >= 400 {
				status = he.Code
			}
		}
		if status == 0 {
			status = http.StatusOK
		}
		if err != nil && status < 400 {
			status = http.StatusInternalServerError
		}
		body := rec.Body.Bytes()
		var parsed map[string]json.RawMessage
		if status >= 400 {
			code := map[int]string{400: "bad_request", 401: "key_invalid", 403: "forbidden", 404: "not_found", 405: "method_not_allowed", 409: "conflict", 413: "payload_too_large", 500: "internal_error", 503: "service_unavailable"}[status]
			if code == "" {
				code = fmt.Sprintf("http_%d", status)
			}
			message := http.StatusText(status)
			if status >= 500 {
				message = "Internal server error"
			}
			if err != nil {
				var he *echo.HTTPError
				if errors.As(err, &he) {
					if status < 500 {
						if s, ok := he.Message.(string); ok && s != "" {
							message = s
						}
					}
				}
			}
			if json.Unmarshal(body, &parsed) == nil {
				_, hasCode := parsed["code"]
				_, hasDetails := parsed["details"]
				_, hasCorrelationID := parsed["correlation_id"]
				if hasCode && hasDetails && hasCorrelationID && status < 500 {
					c.Response().Writer = original
					c.Response().Committed = false
					c.Response().Status = 0
					c.Response().Size = 0
					for k, v := range rec.Header() {
						original.Header()[k] = append([]string(nil), v...)
					}
					c.Response().WriteHeader(status)
					_, writeErr := c.Response().Write(body)
					return writeErr
				}
				if raw, ok := parsed["code"]; ok {
					var existing string
					_ = json.Unmarshal(raw, &existing)
					if existing != "" {
						code = existing
					}
				}
				if raw, ok := parsed["error"]; ok && status < 500 {
					var originalMessage string
					if json.Unmarshal(raw, &originalMessage) == nil && originalMessage != "" {
						message = originalMessage
					}
				}
				if raw, ok := parsed["message"]; ok && status < 500 {
					var m string
					if json.Unmarshal(raw, &m) == nil && m != "" {
						message = m
					}
				}
			}
			c.Response().Writer = original
			c.Response().Committed = false
			c.Response().Status = 0
			c.Response().Size = 0
			for k, v := range rec.Header() {
				original.Header()[k] = append([]string(nil), v...)
			}
			return writeAPIError(c, status, code, message, nil)
		}
		c.Response().Writer = original
		c.Response().Committed = false
		c.Response().Status = 0
		c.Response().Size = 0
		for k, v := range rec.Header() {
			original.Header()[k] = append([]string(nil), v...)
		}
		c.Response().WriteHeader(status)
		_, writeErr := c.Response().Write(body)
		if err != nil {
			return err
		}
		return writeErr
	}
}
