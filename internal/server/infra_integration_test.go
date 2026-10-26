package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// infraSeedJob inserts one row into the jobs table and returns its ID.
func infraSeedJob(t *testing.T, h *accessHarness, queue, kind, state string, attempts int, lastError string) string {
	t.Helper()
	var id string
	if err := h.db.Raw(
		`INSERT INTO jobs (queue, kind, payload, state, attempts, max_attempts, last_error)
		 VALUES (?, ?, '{}'::jsonb, ?, ?, 5, NULLIF(?, '')) RETURNING id::text`,
		queue, kind, state, attempts, lastError,
	).Scan(&id).Error; err != nil {
		t.Fatalf("seed job %s: %v", kind, err)
	}
	return id
}

func infraJobState(t *testing.T, h *accessHarness, id string) string {
	t.Helper()
	var state string
	if err := h.db.Raw("SELECT state FROM jobs WHERE id = ?::uuid", id).Scan(&state).Error; err != nil {
		t.Fatalf("read job %s state: %v", id, err)
	}
	return state
}

func TestInfraStatusShape(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "infra-shape-admin")

	result := h.request(http.MethodGet, "/api/admin/infra", "", h.token(admin), "application/json", "")
	h.mustNoPanic(result, "GET /api/admin/infra")
	if result.code != http.StatusOK {
		t.Fatalf("GET /api/admin/infra returned %d, want 200; body=%q", result.code, settingsTruncate(result.body))
	}
	status := decodeInfraStatus(t, result.body)
	cache := infraSection(t, status, "cache")
	for _, key := range []string{"driver", "connected", "latency_ms", "stats"} {
		if _, ok := cache[key]; !ok {
			t.Errorf("infra cache section lacks %q; keys=%v", key, infraKeys(cache))
		}
	}
	queue := infraSection(t, status, "queue")
	for _, key := range []string{"driver", "workers", "paused", "queues"} {
		if _, ok := queue[key]; !ok {
			t.Errorf("infra queue section lacks %q; keys=%v", key, infraKeys(queue))
		}
	}

	editor := h.user("editor", "infra-shape-editor")
	denied := h.request(http.MethodGet, "/api/admin/infra", "", h.token(editor), "application/json", "")
	h.mustNoPanic(denied, "editor GET /api/admin/infra")
	if denied.code != http.StatusForbidden {
		t.Errorf("editor GET /api/admin/infra returned %d, want 403", denied.code)
	}
}

func TestInfraCacheFlushScopes(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "infra-flush-admin")
	editor := h.user("editor", "infra-flush-editor")
	token := h.token(admin)

	for _, scope := range []string{"page", "object", "all"} {
		result := h.request(http.MethodPost, "/api/admin/infra/cache/flush", fmt.Sprintf(`{"scope":%q}`, scope), token, "application/json", "application/json")
		h.mustNoPanic(result, "flush scope "+scope)
		if result.code != http.StatusOK {
			t.Errorf("POST cache/flush scope=%s returned %d, want 200; body=%q", scope, result.code, settingsTruncate(result.body))
		}
	}

	bogus := h.request(http.MethodPost, "/api/admin/infra/cache/flush", `{"scope":"everything-please"}`, token, "application/json", "application/json")
	h.mustNoPanic(bogus, "flush unknown scope")
	if bogus.code != http.StatusBadRequest {
		t.Errorf("POST cache/flush with unknown scope returned %d, want 400", bogus.code)
	}

	denied := h.request(http.MethodPost, "/api/admin/infra/cache/flush", `{"scope":"all"}`, h.token(editor), "application/json", "application/json")
	h.mustNoPanic(denied, "editor flush")
	if denied.code != http.StatusForbidden {
		t.Errorf("editor POST cache/flush returned %d, want 403", denied.code)
	}
}

func TestInfraQueuePauseResumeAndRetry(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "infra-queue-admin")
	token := h.token(admin)

	// The failing job is written as it would look after its last attempt.
	id := infraSeedJob(t, h, "mail", "infra.probe.fail", "failed", 5, "probe failure")

	failed := h.request(http.MethodGet, "/api/admin/jobs?queue=mail&state=failed", "", token, "application/json", "")
	h.mustNoPanic(failed, "GET failed jobs")
	if failed.code != http.StatusOK || !strings.Contains(failed.body, id) {
		t.Fatalf("GET /api/admin/jobs?state=failed returned %d without job %s; body=%q", failed.code, id, settingsTruncate(failed.body))
	}

	retry := h.request(http.MethodPost, "/api/admin/jobs/"+id+"/retry", "", token, "application/json", "application/json")
	h.mustNoPanic(retry, "retry job")
	if retry.code != http.StatusOK {
		t.Fatalf("POST /api/admin/jobs/%s/retry returned %d, want 200; body=%q", id, retry.code, settingsTruncate(retry.body))
	}
	if state := infraJobState(t, h, id); state != "queued" {
		t.Errorf("retried job state=%q, want queued", state)
	}

	pause := h.request(http.MethodPost, "/api/admin/infra/queue/pause", "", token, "application/json", "application/json")
	h.mustNoPanic(pause, "pause queue")
	if pause.code != http.StatusOK {
		t.Fatalf("POST /api/admin/infra/queue/pause returned %d, want 200", pause.code)
	}
	if paused := infraQueuePaused(t, h, token); !paused {
		t.Errorf("infra queue.paused=false after pause")
	}

	resume := h.request(http.MethodPost, "/api/admin/infra/queue/resume", "", token, "application/json", "application/json")
	h.mustNoPanic(resume, "resume queue")
	if resume.code != http.StatusOK {
		t.Fatalf("POST /api/admin/infra/queue/resume returned %d, want 200", resume.code)
	}
	if paused := infraQueuePaused(t, h, token); paused {
		t.Errorf("infra queue.paused=true after resume")
	}
}

func TestInfraQueueSwitchLosesNoJobs(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "infra-switch-admin")
	token := h.token(admin)

	const kind = "infra.switch.probe"
	if err := h.db.Exec(`INSERT INTO jobs (queue, kind, payload) SELECT 'mail', ?, '{}'::jsonb FROM generate_series(1, 10)`, kind).Error; err != nil {
		t.Fatalf("seed 10 queued jobs: %v", err)
	}
	var seededRows []struct{ ID string }
	if err := h.db.Raw("SELECT id::text AS id FROM jobs WHERE kind = ? ORDER BY id", kind).Scan(&seededRows).Error; err != nil {
		t.Fatalf("read seeded job IDs: %v", err)
	}
	seeded := make([]string, 0, len(seededRows))
	for _, row := range seededRows {
		seeded = append(seeded, row.ID)
	}
	if len(seeded) != 10 {
		t.Fatalf("seeded %d jobs, want 10", len(seeded))
	}

	// Switching to the in-process queue is the target under test.
	result := h.request(http.MethodPost, "/api/admin/infra/queue/switch", `{"to":"memory"}`, token, "application/json", "application/json")
	h.mustNoPanic(result, "switch queue")
	if result.code != http.StatusAccepted {
		t.Fatalf("POST /api/admin/infra/queue/switch returned %d, want 202; body=%q", result.code, settingsTruncate(result.body))
	}

	// The switch runs in the background; wait for the target to hold the jobs.
	var targetIDs []string
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := h.request(http.MethodGet, "/api/admin/jobs?queue=mail&state=queued", "", token, "application/json", "")
		h.mustNoPanic(list, "GET queued jobs after switch")
		targetIDs = nil
		for _, item := range logsItems(t, list.body) {
			if id, ok := item["id"].(string); ok {
				targetIDs = append(targetIDs, id)
			}
		}
		if len(targetIDs) >= len(seeded) || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	counts := map[string]int{}
	for _, id := range targetIDs {
		counts[id]++
	}
	for _, id := range seeded {
		switch counts[id] {
		case 0:
			t.Errorf("job %s was lost in the switch", id)
		case 1:
		default:
			t.Errorf("job %s appears %d times on the target", id, counts[id])
		}
	}
	if len(targetIDs) != len(seeded) {
		t.Errorf("target holds %d queued jobs, want exactly %d", len(targetIDs), len(seeded))
	}
}

func TestInfraTestEndpointsSanitiseErrors(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "infra-test-admin")
	token := h.token(admin)

	const secret = "s3cretpw"
	// The malformed escape makes url.Parse fail; its error text echoes the whole URL.
	body := `{"driver":"redis","url":"redis://probe-user:` + secret + `@%zz/0"}`
	for _, path := range []string{"/api/admin/infra/cache/test", "/api/admin/infra/queue/test"} {
		result := h.request(http.MethodPost, path, body, token, "application/json", "application/json")
		h.mustNoPanic(result, "POST "+path)
		if result.code != http.StatusBadRequest {
			t.Errorf("POST %s with a bad redis URL returned %d, want 400; body=%q", path, result.code, settingsTruncate(result.body))
		}
		if strings.Contains(result.body, secret) {
			t.Errorf("POST %s error response leaks the password", path)
		}
	}
}

// decodeInfraStatus parses the infra status document into a generic map.
func decodeInfraStatus(t *testing.T, body string) map[string]any {
	t.Helper()
	var status map[string]any
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode infra status: %v; body=%q", err, settingsTruncate(body))
	}
	return status
}

func infraSection(t *testing.T, status map[string]any, name string) map[string]any {
	t.Helper()
	section, ok := status[name].(map[string]any)
	if !ok {
		t.Fatalf("infra status has no %q object; keys=%v", name, infraKeys(status))
	}
	return section
}

func infraKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func infraQueuePaused(t *testing.T, h *accessHarness, token string) bool {
	t.Helper()
	result := h.request(http.MethodGet, "/api/admin/infra", "", token, "application/json", "")
	h.mustNoPanic(result, "GET /api/admin/infra")
	queue := infraSection(t, decodeInfraStatus(t, result.body), "queue")
	paused, ok := queue["paused"].(bool)
	if !ok {
		t.Fatalf("infra queue.paused is %v, want a boolean", queue["paused"])
	}
	return paused
}
