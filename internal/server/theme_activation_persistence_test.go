package server

import (
	"net/http"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

func TestThemeActivationPersistAPIpublishesAfterPersistence(t *testing.T) {
	h := newWebMCPHarness(t)
	r := h.adminJSON(http.MethodPost, "/api/admin/themes/activate", `{"name":"webmcp-alt"}`)
	if r.code != http.StatusOK {
		t.Fatalf("activate returned %d: %s", r.code, archivesSnippet(r.body))
	}
	var saved core.Option
	if err := h.db.Where("key = ?", "active_theme").First(&saved).Error; err != nil || saved.Value != webmcpAltTheme {
		t.Fatalf("persisted theme=%+v err=%v", saved, err)
	}
	if h.tm.GetActive() != webmcpAltTheme {
		t.Fatalf("runtime theme=%q want %q", h.tm.GetActive(), webmcpAltTheme)
	}
}

func TestThemeActivationPersistDatabaseFailureKeepsOldRuntimeAndReturns500(t *testing.T) {
	h := newWebMCPHarness(t)
	if err := h.db.Exec(`CREATE FUNCTION reject_theme_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'theme write rejected'; END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`CREATE TRIGGER reject_theme_write BEFORE INSERT OR UPDATE ON options FOR EACH ROW WHEN (NEW.key = 'active_theme') EXECUTE FUNCTION reject_theme_write()`).Error; err != nil {
		t.Fatal(err)
	}
	r := h.adminJSON(http.MethodPost, "/api/admin/themes/activate", `{"name":"webmcp-alt"}`)
	if r.code != http.StatusInternalServerError {
		t.Fatalf("activation returned %d, want 500: %s", r.code, archivesSnippet(r.body))
	}
	if h.tm.GetActive() != "default" {
		t.Fatalf("failed persistence changed runtime to %q", h.tm.GetActive())
	}
	var saved core.Option
	if err := h.db.Where("key = ?", "active_theme").First(&saved).Error; err == nil {
		t.Fatalf("failed persistence created active_theme=%q", saved.Value)
	}
}
