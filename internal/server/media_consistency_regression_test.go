package server

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestMediaConsistencyLegacyUploadAndReplaceKeepMetadataAndBytesAligned(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "media-consistency-admin"))
	medPolicy(t, h, token, "Legacy compatibility")
	path := "compat-shared.png"
	oldBytes := append(medPNG(), bytes.Repeat([]byte{0x31}, 32)...)
	newBytes := append(medPNG(), bytes.Repeat([]byte{0x72}, 48)...)

	upload := func(route string, payload []byte) accessResult {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, route, &body)
		req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
		req.Header.Set("X-Jelly-CSRF", "1")
		req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
		rec := httptest.NewRecorder()
		h.echo.ServeHTTP(rec, req)
		return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
	}
	legacy := upload("/api/admin/media", oldBytes)
	if legacy.code < 200 || legacy.code > 299 {
		t.Fatalf("legacy upload = %d: %s", legacy.code, legacy.body)
	}
	var storedPath string
	if err := h.db.Raw(`SELECT path FROM media_files WHERE filename = ? AND deleted_at IS NULL`, path).Scan(&storedPath).Error; err != nil {
		t.Fatalf("read legacy media metadata: %v", err)
	}
	if storedPath == "" {
		t.Fatal("legacy upload did not create media_files metadata")
	}
	physical := filepath.Join("data", "uploads", filepath.FromSlash(storedPath))
	if got, err := os.ReadFile(physical); err != nil || !bytes.Equal(got, oldBytes) {
		t.Fatalf("legacy upload bytes at %s differ from request: read err=%v", physical, err)
	}

	// The canonical media API replacing this same path must update the bytes
	// and the checksum together, while retaining the row identity and refs.
	var fileID int64
	if err := h.db.Raw(`SELECT id FROM media_files WHERE path = ? AND deleted_at IS NULL`, storedPath).Scan(&fileID).Error; err != nil || fileID == 0 {
		t.Fatalf("read media file id: id=%d err=%v", fileID, err)
	}
	if err := h.db.Exec(`INSERT INTO media_refs (file_id, ref_type, ref_id) VALUES (?, 'post', 731)`, fileID).Error; err != nil {
		t.Fatalf("add media reference: %v", err)
	}
	canonical := upload(fmt.Sprintf("/api/admin/media/files/%d/replace", fileID), newBytes)
	if canonical.code < 200 || canonical.code > 299 {
		t.Fatalf("canonical replacement = %d: %s", canonical.code, canonical.body)
	}
	got, err := os.ReadFile(physical)
	if err != nil || !bytes.Equal(got, newBytes) {
		t.Fatalf("replacement bytes differ from request: read err=%v", err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(newBytes))
	var gotHash string
	if err := h.db.Raw(`SELECT sha256 FROM media_files WHERE id = ?`, fileID).Scan(&gotHash).Error; err != nil {
		t.Fatalf("read replacement checksum: %v", err)
	}
	if gotHash != wantHash {
		t.Errorf("replacement checksum = %q, want %q", gotHash, wantHash)
	}
	if n := impCount(t, h, `SELECT COUNT(*) FROM media_refs WHERE file_id = ? AND ref_type = 'post' AND ref_id = 731`, fileID); n != 1 {
		t.Errorf("replacement lost media reference; rows=%d", n)
	}
	if strings.Contains(legacy.body, "../") {
		t.Errorf("legacy upload returned an unsafe path: %s", legacy.body)
	}
}

func TestMediaConsistencyLegacyUploadRejectsInvalidTypeAndOversize(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "media-legacy-validation-admin"))
	medPolicy(t, h, token, "Legacy validation")
	for _, tc := range []struct {
		name string
		data []byte
		want int
	}{
		{name: "legacy-unsafe.exe", data: []byte("MZ\x90\x00 executable"), want: http.StatusUnsupportedMediaType},
		{name: "legacy-too-large.png", data: append(medPNG(), bytes.Repeat([]byte{1}, 21<<20)...), want: http.StatusRequestEntityTooLarge},
	} {
		r := medConsistencyUpload(h, token, "/api/admin/media", tc.name, tc.data)
		if r.code != tc.want {
			t.Errorf("legacy upload %s = %d, want %d: %s", tc.name, r.code, tc.want, r.body)
		}
		if _, err := os.Stat(filepath.Join("data", "uploads", tc.name)); !os.IsNotExist(err) {
			t.Errorf("rejected legacy upload %s left a file (stat err=%v)", tc.name, err)
		}
		if n := impCount(t, h, `SELECT COUNT(*) FROM media_files WHERE filename = ? AND deleted_at IS NULL`, tc.name); n != 0 {
			t.Errorf("rejected legacy upload %s left %d media rows", tc.name, n)
		}
	}
}

func TestMediaConsistencyReplaceDBFailurePreservesOldBytesAndMetadata(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "media-replace-failure-admin"))
	medPolicy(t, h, token, "Replace failure")
	path := "replace-failure.png"
	oldBytes := append(medPNG(), bytes.Repeat([]byte{0x44}, 40)...)
	newBytes := append(medPNG(), bytes.Repeat([]byte{0x55}, 56)...)
	upload := medConsistencyUpload(h, token, "/api/admin/media/files", path, oldBytes)
	if upload.code < 200 || upload.code > 299 {
		t.Fatalf("initial canonical upload = %d: %s", upload.code, upload.body)
	}
	var row struct {
		ID   int64  `gorm:"column:id"`
		Path string `gorm:"column:path"`
		Hash string `gorm:"column:sha256"`
	}
	if err := h.db.Raw(`SELECT id, path, sha256 FROM media_files WHERE filename = ? AND deleted_at IS NULL`, path).Scan(&row).Error; err != nil || row.ID == 0 {
		t.Fatalf("read uploaded metadata: %+v err=%v", row, err)
	}
	physical := filepath.Join("data", "uploads", filepath.FromSlash(row.Path))
	if err := h.db.Exec(`CREATE FUNCTION jelly_test_reject_media_replace() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected media metadata failure'; END; $$`).Error; err != nil {
		t.Fatalf("create metadata failure function: %v", err)
	}
	if err := h.db.Exec(`CREATE TRIGGER jelly_test_reject_media_replace BEFORE UPDATE ON media_files FOR EACH ROW EXECUTE FUNCTION jelly_test_reject_media_replace()`).Error; err != nil {
		t.Fatalf("create metadata failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = h.db.Exec(`DROP TRIGGER IF EXISTS jelly_test_reject_media_replace ON media_files`).Error
		_ = h.db.Exec(`DROP FUNCTION IF EXISTS jelly_test_reject_media_replace()`).Error
	})
	replaced := medConsistencyUpload(h, token, fmt.Sprintf("/api/admin/media/files/%d/replace", row.ID), path, newBytes)
	if replaced.code < 400 {
		t.Fatalf("replacement with injected DB failure = %d, want error: %s", replaced.code, replaced.body)
	}
	gotBytes, err := os.ReadFile(physical)
	if err != nil || !bytes.Equal(gotBytes, oldBytes) {
		t.Errorf("DB failure changed original file bytes (read err=%v)", err)
	}
	var afterHash string
	if err := h.db.Raw(`SELECT sha256 FROM media_files WHERE id = ? AND deleted_at IS NULL`, row.ID).Scan(&afterHash).Error; err != nil {
		t.Fatalf("read media row after failed replacement: %v", err)
	}
	if afterHash != row.Hash {
		t.Errorf("DB failure changed media hash from %q to %q", row.Hash, afterHash)
	}
}

func TestMediaConsistencyConcurrentSameFilenameKeepsDistinctBytes(t *testing.T) {
	h := newAccessHarness(t)
	token := h.token(h.user("admin", "media-parallel-upload-admin"))
	medPolicy(t, h, token, "Parallel same name")
	name := "parallel-name.png"
	payloads := [][]byte{
		append(medPNG(), bytes.Repeat([]byte{0x61}, 40)...),
		append(medPNG(), bytes.Repeat([]byte{0x62}, 56)...),
	}
	results := make([]accessResult, len(payloads))
	var wg sync.WaitGroup
	for i := range payloads {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = medConsistencyUpload(h, token, "/api/admin/media/files", name, payloads[i])
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.code < 200 || r.code > 299 {
			t.Fatalf("concurrent upload %d = %d: %s", i, r.code, r.body)
		}
	}
	var rows []struct {
		Path string `gorm:"column:path"`
		Hash string `gorm:"column:sha256"`
	}
	if err := h.db.Raw(`SELECT path, sha256 FROM media_files WHERE filename LIKE 'parallel-name%' AND deleted_at IS NULL ORDER BY path`).Scan(&rows).Error; err != nil {
		t.Fatalf("query concurrent media rows: %v", err)
	}
	if len(rows) != len(payloads) {
		t.Fatalf("concurrent same-name upload rows = %d, want %d: %+v", len(rows), len(payloads), rows)
	}
	want := map[string]bool{}
	for _, payload := range payloads {
		want[fmt.Sprintf("%x", sha256.Sum256(payload))] = true
	}
	for _, row := range rows {
		content, err := os.ReadFile(filepath.Join("data", "uploads", filepath.FromSlash(row.Path)))
		if err != nil {
			t.Fatalf("read concurrent media file %s: %v", row.Path, err)
		}
		gotHash := fmt.Sprintf("%x", sha256.Sum256(content))
		if !want[gotHash] || row.Hash != gotHash {
			t.Errorf("same-name path %s has bytes/hash %s/%s outside requested payloads", row.Path, gotHash, row.Hash)
		}
		delete(want, gotHash)
	}
	if len(want) != 0 {
		t.Errorf("concurrent upload lost distinct payload hash(es): %v", want)
	}
}

func medConsistencyUpload(h *accessHarness, token, route, name string, payload []byte) accessResult {
	h.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		h.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		h.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, route, &body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	req.Header.Set("X-Jelly-CSRF", "1")
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}
