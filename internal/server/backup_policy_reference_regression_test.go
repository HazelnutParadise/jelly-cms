package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/backup"
)

func createBackupPolicyReferenceFixture(t *testing.T, h *accessHarness, token, name string, policyIDOut *int64) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"driver":"s3","purpose":"backup","config":{"endpoint":"https://s3.example.test","bucket":"backup-fixture","region":"us-east-1","prefix":"jelly/backups","access_key":"test-access","secret_key":"test-secret"}}`, name)
	response := updCall(h, http.MethodPost, "/api/admin/media/policies", body, token)
	if response.code != http.StatusCreated {
		t.Fatalf("create backup destination = %d: %s", response.code, response.body)
	}
	*policyIDOut = int64(impJSONNumber(t, response.body, "id"))
}

func seedBackupPolicyReference(t *testing.T, h *accessHarness, policyID int64, state string, marker string) {
	t.Helper()
	manifest := map[string]any{
		"_jelly_remote": map[string]any{
			"policy_id": policyID,
			"key":       "backups/" + marker + ".bak",
			"sha256":    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"size":      17,
		},
	}
	if state == "queued" {
		manifest = map[string]any{
			"_jelly_pending": map[string]any{
				"policy_id": policyID,
			},
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`INSERT INTO backups (state, kind, contents, manifest, created_at)
		VALUES (?, ?, '[]'::jsonb, ?::jsonb, now())`, state, backup.KindScheduled, string(raw)).Error; err != nil {
		t.Fatalf("seed %s backup reference: %v", state, err)
	}
}

func TestBackupPolicyReferenceRegressionProtectsQueuedAndRemoteDestinations(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "backup-policy-reference-admin")
	token := h.token(admin)

	for _, tc := range []struct {
		name  string
		state string
	}{
		{name: "queued backup fixes policy at enqueue", state: "queued"},
		{name: "completed remote backup pins destination", state: "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var policyID int64
			createBackupPolicyReferenceFixture(t, h, token, "Backup destination "+tc.state, &policyID)
			seedBackupPolicyReference(t, h, policyID, tc.state, tc.state)

			configChange := fmt.Sprintf(`{"config":{"endpoint":"https://replacement.example.test","bucket":"replacement-bucket","region":"us-east-1","prefix":"replacement"}}`)
			updateResponse := updCall(h, http.MethodPut, fmt.Sprintf("/api/admin/media/policies/%d", policyID), configChange, token)
			if updateResponse.code != http.StatusConflict {
				t.Errorf("change referenced backup destination = %d, want 409: %s", updateResponse.code, updateResponse.body)
			}
			var beforeRotation struct {
				Config string `gorm:"column:config"`
			}
			if err := h.db.Raw("SELECT config FROM media_policies WHERE id = ?", policyID).Scan(&beforeRotation).Error; err != nil {
				t.Fatalf("read backup policy config before credential rotation: %v", err)
			}

			credentialRotation := `{"secret":"{\"access_key\":\"rotated-access\",\"secret_key\":\"rotated-secret\"}"}`
			rotationResponse := updCall(h, http.MethodPut, fmt.Sprintf("/api/admin/media/policies/%d", policyID), credentialRotation, token)
			if rotationResponse.code < 200 || rotationResponse.code >= 300 {
				t.Errorf("rotate credentials for referenced backup policy = %d, want 2xx: %s", rotationResponse.code, rotationResponse.body)
			}
			var row struct {
				Config string `gorm:"column:config"`
			}
			if err := h.db.Raw("SELECT config FROM media_policies WHERE id = ?", policyID).Scan(&row).Error; err != nil {
				t.Fatalf("read backup policy config after credential rotation: %v", err)
			}
			var beforeConfig, afterConfig map[string]any
			if err := json.Unmarshal([]byte(beforeRotation.Config), &beforeConfig); err != nil {
				t.Fatalf("decode destination config before rotation: %v", err)
			}
			if err := json.Unmarshal([]byte(row.Config), &afterConfig); err != nil {
				t.Fatalf("decode destination config after rotation: %v", err)
			}
			if !reflect.DeepEqual(beforeConfig, afterConfig) || !strings.Contains(row.Config, "s3.example.test") {
				t.Errorf("credential rotation changed destination config: before %s after %s", beforeRotation.Config, row.Config)
			}

			deleteResponse := updCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/media/policies/%d", policyID), "", token)
			if deleteResponse.code != http.StatusConflict {
				t.Errorf("delete referenced backup policy = %d, want 409: %s", deleteResponse.code, deleteResponse.body)
			}
		})
	}
}
