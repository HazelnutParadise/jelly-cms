package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// moaMultipart sends one file in the "file" field to any media route.
func moaMultipart(h *accessHarness, method, path, token, name string, data []byte) accessResult {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
	req.Header.Set("X-Jelly-CSRF", "1")
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

type moaFile struct {
	id   int64
	path string
}

func moaUpload(t *testing.T, h *accessHarness, token, name string) moaFile {
	t.Helper()
	r := medUpload(h, token, name, medPNG())
	if r.code < 200 || r.code > 299 {
		t.Fatalf("upload %s = %d: %s", name, r.code, r.body)
	}
	var f moaFile
	f.id = int64(impJSONNumber(t, r.body, "id"))
	if err := h.db.Raw("SELECT path FROM media_files WHERE id = ?", f.id).Scan(&f.path).Error; err != nil || f.path == "" {
		t.Fatalf("load path of %d: %v", f.id, err)
	}
	return f
}

func moaListIDs(t *testing.T, h *accessHarness, token string) map[int64]bool {
	t.Helper()
	r := updCall(h, http.MethodGet, "/api/admin/media/files?per_page=100", "", token)
	if r.code != http.StatusOK {
		t.Fatalf("list files = %d: %s", r.code, r.body)
	}
	var body struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatalf("list body: %v", err)
	}
	ids := map[int64]bool{}
	for _, it := range body.Items {
		ids[it.ID] = true
	}
	if int64(len(ids)) != body.Total {
		t.Errorf("list total = %d but %d items returned", body.Total, len(ids))
	}
	return ids
}

func TestMediaAuthorsOnlyManageTheirOwnFiles(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.token(h.user("admin", "moa-admin"))
	editor := h.token(h.user("editor", "moa-editor"))
	alice := h.token(h.user("author", "moa-alice"))
	bobUser := h.user("author", "moa-bob")
	bob := h.token(bobUser)
	medPolicy(t, h, admin, "Uploads")

	af := moaUpload(t, h, alice, "alice.png")
	bf := moaUpload(t, h, bob, "bob.png")
	orphan := medSeedFile(t, h, moaPolicyID(t, h), "orphan.png", nil)

	if ids := moaListIDs(t, h, alice); !ids[af.id] || ids[bf.id] || ids[orphan] {
		t.Errorf("alice lists %v, want only her file %d", ids, af.id)
	}
	if ids := moaListIDs(t, h, editor); !ids[af.id] || !ids[bf.id] || !ids[orphan] {
		t.Errorf("editor lists %v, want %d, %d and %d", ids, af.id, bf.id, orphan)
	}

	for _, id := range []int64{bf.id, orphan} {
		p := fmt.Sprintf("/api/admin/media/files/%d", id)
		if r := updCall(h, http.MethodPatch, p, `{"alt":"x"}`, alice); r.code != http.StatusNotFound {
			t.Errorf("alice PATCH %d = %d, want 404", id, r.code)
		}
		if r := moaMultipart(h, http.MethodPost, p+"/replace", alice, "x.png", medPNG()); r.code != http.StatusNotFound {
			t.Errorf("alice replace %d = %d, want 404", id, r.code)
		}
		if r := updCall(h, http.MethodDelete, p, "", alice); r.code != http.StatusNotFound {
			t.Errorf("alice DELETE %d = %d, want 404", id, r.code)
		}
	}
	if n := impCount(t, h, fmt.Sprintf("SELECT COUNT(*) FROM media_files WHERE id IN (%d, %d) AND deleted_at IS NULL AND alt = ''", bf.id, orphan)); n != 2 {
		t.Errorf("files alice may not touch changed: %d of 2 untouched", n)
	}

	// Legacy filename routes take a bare filename, so Bob gets a top-level file.
	legacy := medSeedFile(t, h, moaPolicyID(t, h), "bob-legacy.png", nil)
	if err := h.db.Exec("UPDATE media_files SET owner_id = ? WHERE id = ?", bobUser.ID, legacy).Error; err != nil {
		t.Fatal(err)
	}
	if r := updCall(h, http.MethodDelete, "/api/admin/media/bob-legacy.png", "", alice); r.code != http.StatusNotFound {
		t.Errorf("alice legacy DELETE of bob's file = %d, want 404", r.code)
	}
	if r := moaMultipart(h, http.MethodPut, "/api/admin/media/bob-legacy.png", alice, "x.png", medPNG()); r.code != http.StatusNotFound {
		t.Errorf("alice legacy PUT of bob's file = %d, want 404", r.code)
	}
	if r := updCall(h, http.MethodPost, "/api/admin/media/rename", `{"old_name":"bob-legacy.png","new_name":"stolen.png"}`, alice); r.code != http.StatusNotFound {
		t.Errorf("alice legacy rename of bob's file = %d, want 404", r.code)
	}
	if n := impCount(t, h, fmt.Sprintf("SELECT COUNT(*) FROM media_files WHERE id = %d AND path = 'bob-legacy.png' AND deleted_at IS NULL", legacy)); n != 1 {
		t.Errorf("bob's file was changed through a legacy route")
	}

	// A file on disk with no row has no owner.
	stray := filepath.Join(uploadsRoot, "stray.png")
	if err := os.MkdirAll(uploadsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, medPNG(), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := updCall(h, http.MethodDelete, "/api/admin/media/stray.png", "", alice); r.code != http.StatusNotFound {
		t.Errorf("alice legacy DELETE of an unregistered file = %d, want 404", r.code)
	}
	if r := moaMultipart(h, http.MethodPut, "/api/admin/media/stray.png", alice, "x.png", medPNG()); r.code != http.StatusNotFound {
		t.Errorf("alice legacy PUT of an unregistered file = %d, want 404", r.code)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("unregistered file removed by an author: %v", err)
	}

	r := updCall(h, http.MethodGet, "/api/admin/media", "", alice)
	if r.code != http.StatusOK {
		t.Fatalf("alice legacy list = %d", r.code)
	}
	if !strings.Contains(r.body, af.path) || strings.Contains(r.body, bf.path) || strings.Contains(r.body, "bob-legacy.png") || strings.Contains(r.body, "stray.png") {
		t.Errorf("alice legacy list = %s, want only %s", r.body, af.path)
	}

	// Owners and editors keep full control.
	if r := updCall(h, http.MethodPatch, fmt.Sprintf("/api/admin/media/files/%d", af.id), `{"alt":"mine"}`, alice); r.code != http.StatusOK {
		t.Errorf("alice PATCH own file = %d: %s", r.code, r.body)
	}
	if r := moaMultipart(h, http.MethodPost, fmt.Sprintf("/api/admin/media/files/%d/replace", af.id), alice, "alice.png", medPNG()); r.code != http.StatusOK {
		t.Errorf("alice replace own file = %d: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPatch, fmt.Sprintf("/api/admin/media/files/%d", bf.id), `{"alt":"edited"}`, editor); r.code != http.StatusOK {
		t.Errorf("editor PATCH bob's file = %d: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodDelete, "/api/admin/media/stray.png", "", editor); r.code != http.StatusOK {
		t.Errorf("editor legacy DELETE of an unregistered file = %d: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/media/files/%d", af.id), "", alice); r.code != http.StatusOK {
		t.Errorf("alice DELETE own file = %d: %s", r.code, r.body)
	}
}

// moaPolicyID returns the id of the only media-purpose policy.
func moaPolicyID(t *testing.T, h *accessHarness) int64 {
	t.Helper()
	var id int64
	if err := h.db.Raw("SELECT id FROM media_policies WHERE purpose = 'media' AND deleted_at IS NULL ORDER BY id LIMIT 1").Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("media policy id: %v", err)
	}
	return id
}
