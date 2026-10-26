package server

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/version"
)

func TestUpdateRuntimeGuardRegressionRoutesRejectManagedRuntimesBeforeSideEffects(t *testing.T) {
	for _, kind := range []string{version.KindContainer, version.KindKubernetes, version.KindPackage} {
		t.Run(kind, func(t *testing.T) {
			h := newAccessHarness(t)
			admin := h.user("admin", "upd-runtime-guard-"+kind)
			t.Setenv("JELLY_RUNTIME", kind)
			// The harness needs PostgreSQL, while these requests must exercise
			// the production queueing branch after setup.
			t.Setenv("JELLY_TEST_POSTGRES", "")

			executable, err := os.Executable()
			if err != nil {
				t.Fatalf("locate test executable: %v", err)
			}
			before, err := os.ReadFile(executable)
			if err != nil {
				t.Fatalf("read test executable before requests: %v", err)
			}
			beforeHash := sha256.Sum256(before)
			var jobsBefore, backupsBefore int64
			if err := h.db.Raw(`SELECT COUNT(*) FROM jobs WHERE kind IN ('update.apply','update.rollback')`).Scan(&jobsBefore).Error; err != nil {
				t.Fatalf("count update jobs before requests: %v", err)
			}
			if err := h.db.Raw(`SELECT COUNT(*) FROM backups`).Scan(&backupsBefore).Error; err != nil {
				t.Fatalf("count backups before requests: %v", err)
			}

			for _, route := range []struct{ name, path, body string }{
				{name: "apply", path: "/api/admin/update/apply", body: `{"version":"1.2.0"}`},
				{name: "rollback", path: "/api/admin/update/rollback"},
			} {
				t.Run(route.name, func(t *testing.T) {
					res := updCall(h, http.MethodPost, route.path, route.body, h.token(admin))
					var body struct {
						Runtime string `json:"runtime"`
						Error   string `json:"error"`
					}
					if err := json.Unmarshal([]byte(res.body), &body); err != nil {
						t.Fatalf("%s response is not JSON: status=%d body=%q: %v", route.name, res.code, res.body, err)
					}
					if res.code != http.StatusConflict || body.Runtime != kind || body.Error == "" {
						t.Errorf("%s in %s returned status=%d runtime=%q error=%q body=%s; want 409 JSON with runtime %q", route.name, kind, res.code, body.Runtime, body.Error, res.body, kind)
					}
				})
			}

			var jobsAfter, backupsAfter int64
			if err := h.db.Raw(`SELECT COUNT(*) FROM jobs WHERE kind IN ('update.apply','update.rollback')`).Scan(&jobsAfter).Error; err != nil {
				t.Fatalf("count update jobs after requests: %v", err)
			}
			if err := h.db.Raw(`SELECT COUNT(*) FROM backups`).Scan(&backupsAfter).Error; err != nil {
				t.Fatalf("count backups after requests: %v", err)
			}
			if jobsAfter != jobsBefore {
				t.Errorf("update jobs changed from %d to %d", jobsBefore, jobsAfter)
			}
			if backupsAfter != backupsBefore {
				t.Errorf("backup count changed from %d to %d", backupsBefore, backupsAfter)
			}
			after, err := os.ReadFile(executable)
			if err != nil {
				t.Fatalf("read test executable after requests: %v", err)
			}
			if afterHash := sha256.Sum256(after); afterHash != beforeHash {
				t.Error("test executable changed during managed-runtime update requests")
			}
			if strings.TrimSpace(os.Getenv("JELLY_RUNTIME")) != kind {
				t.Fatalf("runtime fixture changed unexpectedly")
			}
		})
	}
}
