package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// Acceptance test for ticket 25a (docs/tickets/25-integration-internal-rename.md):
// creating an integration through the admin API records the renamed audit
// values, and using the returned access key records actor_type integration.
// The external URL and JSON stay unchanged in 25a.
func TestIntegrationAuditWritesIntegrationValues(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "integration-audit-admin")

	res := agtAdmin(h, h.token(admin), http.MethodPost, "/api/admin/agents", agtCreateBody("稽核串接", "posts.read"))
	if res.code != http.StatusCreated {
		t.Fatalf("POST /api/admin/agents returned %d, want 201; body=%q", res.code, res.body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.body), &raw); err != nil {
		t.Fatalf("decode create response: %v; body=%q", err, res.body)
	}
	if _, ok := raw["agent"]; !ok {
		t.Errorf("create response has no agent key; 25a keeps the external JSON unchanged; body=%q", res.body)
	}
	if _, ok := raw["integration"]; ok {
		t.Errorf("create response already has an integration key; 25a keeps the external JSON unchanged; body=%q", res.body)
	}
	if _, ok := raw["api_key"]; !ok {
		t.Errorf("create response has no api_key; body=%q", res.body)
	}
	var created agtCreated
	agtDecode(t, res, &created, "create integration")
	if created.Agent.ID == 0 || created.APIKey == "" {
		t.Fatalf("create response has no agent id or api key; body=%q", res.body)
	}
	integrationID := strconv.FormatUint(uint64(created.Agent.ID), 10)

	var createEvents int64
	if err := h.db.Raw(`SELECT count(*) FROM integration_events
		WHERE integration_id = ? AND actor_type = 'user' AND module = 'integrations' AND action = 'integration.create'`,
		created.Agent.ID).Row().Scan(&createEvents); err != nil {
		t.Fatalf("count integration_events after create: %v", err)
	}
	if createEvents != 1 {
		t.Errorf("integration_events has %d integration.create rows for the integration, want 1", createEvents)
	}

	var logID uint
	var fields string
	if err := h.db.Raw(`SELECT id, fields::text FROM system_logs
		WHERE source = 'audit' AND message = 'integration.create' ORDER BY id DESC LIMIT 1`).
		Row().Scan(&logID, &fields); err != nil {
		t.Fatalf("read the integration.create audit log: %v", err)
	}
	var hasIntegrationID, hasAgentID bool
	if err := h.db.Raw(`SELECT jsonb_exists(fields, 'integration_id'), jsonb_exists(fields, 'agent_id')
		FROM system_logs WHERE id = ?`, logID).Row().Scan(&hasIntegrationID, &hasAgentID); err != nil {
		t.Fatalf("read the audit log fields: %v", err)
	}
	if !hasIntegrationID {
		t.Errorf("system_logs.fields of integration.create has no integration_id: %s", fields)
	}
	if hasAgentID {
		t.Errorf("system_logs.fields of integration.create still has agent_id: %s", fields)
	}
	var fieldIntegrationID string
	if err := h.db.Raw(`SELECT coalesce(fields->>'integration_id', '') FROM system_logs WHERE id = ?`, logID).
		Row().Scan(&fieldIntegrationID); err != nil {
		t.Fatalf("read fields.integration_id: %v", err)
	}
	if fieldIntegrationID != integrationID {
		t.Errorf("system_logs.fields.integration_id = %q, want %q", fieldIntegrationID, integrationID)
	}
	var legacyAudit int64
	if err := h.db.Raw(`SELECT count(*) FROM system_logs WHERE source = 'audit' AND message LIKE 'agent.%'`).Row().Scan(&legacyAudit); err != nil {
		t.Fatalf("count legacy audit messages: %v", err)
	}
	if legacyAudit != 0 {
		t.Errorf("system_logs has %d audit rows with an agent.* message after creating an integration, want 0", legacyAudit)
	}

	agtExpectOK(t, agtMe(h, created.APIKey), "GET /api/v1/me with the created integration key")
	var keyEvents int64
	if err := h.db.Raw(`SELECT count(*) FROM integration_events
		WHERE integration_id = ? AND actor_type = 'integration'`, created.Agent.ID).Row().Scan(&keyEvents); err != nil {
		t.Fatalf("count integration_events after the key request: %v", err)
	}
	if keyEvents < 1 {
		t.Errorf("integration_events has %d integration-actor rows after GET /api/v1/me, want at least 1", keyEvents)
	}
}
