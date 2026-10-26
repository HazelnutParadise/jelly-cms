package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

// medPolicy creates a local policy through the API and returns its id.
func medPolicy(t *testing.T, h *accessHarness, token, name string) int64 {
	t.Helper()
	r := updCall(h, http.MethodPost, "/api/admin/media/policies", fmt.Sprintf(`{"name":%q,"driver":"local","purpose":"media"}`, name), token)
	if r.code < 200 || r.code > 299 {
		t.Fatalf("create policy %s = %d: %s", name, r.code, r.body)
	}
	return int64(impJSONNumber(t, r.body, "id"))
}

// medSeedFile inserts a media_files row directly, for tests about what
// happens to files that already exist.
func medSeedFile(t *testing.T, h *accessHarness, policyID int64, path string, groupID *int64) int64 {
	t.Helper()
	var id int64
	err := h.db.Raw(`INSERT INTO media_files (policy_id, group_id, path, filename, mime, size, sha256, source)
		VALUES (?, ?, ?, ?, 'image/png', 4, '', 'upload') RETURNING id`, policyID, groupID, path, filepath.Base(path)).Scan(&id).Error
	if err != nil || id == 0 {
		t.Fatalf("seed media file %s: %v", path, err)
	}
	return id
}

func medUpload(h *accessHarness, token, name string, data []byte) accessResult {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/media/files", &buf)
	req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
	req.Header.Set("X-Jelly-CSRF", "1")
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

func medPNG() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
}

// medJPEGWithGPS returns a valid JPEG with an APP1 Exif segment that carries a
// GPS IFD (latitude reference N).
func medJPEGWithGPS(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, nil); err != nil {
		t.Fatal(err)
	}
	// Little-endian TIFF: IFD0 at 8 holds one GPSInfo pointer to the GPS IFD at 26.
	tiff := []byte{'I', 'I', 0x2A, 0x00, 8, 0, 0, 0}
	ifd0 := make([]byte, 2+12+4)
	binary.LittleEndian.PutUint16(ifd0[0:], 1)
	binary.LittleEndian.PutUint16(ifd0[2:], 0x8825)
	binary.LittleEndian.PutUint16(ifd0[4:], 4)
	binary.LittleEndian.PutUint32(ifd0[6:], 1)
	binary.LittleEndian.PutUint32(ifd0[10:], 26)
	tiff = append(tiff, ifd0...)
	gps := make([]byte, 2+12+4)
	binary.LittleEndian.PutUint16(gps[0:], 1)
	binary.LittleEndian.PutUint16(gps[2:], 0x0001)
	binary.LittleEndian.PutUint16(gps[4:], 2)
	binary.LittleEndian.PutUint32(gps[6:], 2)
	copy(gps[10:], "N\x00")
	tiff = append(tiff, gps...)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := uint16(len(payload) + 2)
	app1 := append([]byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}, payload...)
	out := append([]byte{0xFF, 0xD8}, app1...)
	return append(out, plain.Bytes()[2:]...)
}

func TestMediaPolicyCRUDAndDefault(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-crud-admin")
	token := h.token(admin)

	id := medPolicy(t, h, token, "Local main")
	if list := updCall(h, http.MethodGet, "/api/admin/media/policies", "", token); list.code != http.StatusOK || !strings.Contains(list.body, "Local main") {
		t.Errorf("GET policies = %d, want 200 listing Local main: %s", list.code, list.body)
	}
	if r := updCall(h, http.MethodPut, fmt.Sprintf("/api/admin/media/policies/%d", id), `{"name":"Local renamed"}`, token); r.code < 200 || r.code > 299 {
		t.Errorf("PUT policy = %d, want 2xx: %s", r.code, r.body)
	}
	if r := updCall(h, http.MethodPost, fmt.Sprintf("/api/admin/media/policies/%d/default", id), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("set default = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_policies WHERE id = ? AND is_default", id); n != 1 {
		t.Errorf("policy %d not default after POST /default", id)
	}
	second := medPolicy(t, h, token, "Second")
	if r := updCall(h, http.MethodPost, fmt.Sprintf("/api/admin/media/policies/%d/default", second), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("set second default = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_policies WHERE id = ? AND is_default", id); n != 0 {
		t.Errorf("previous default still marked default")
	}
}

func TestMediaPolicyDeleteBlockedWhenInUse(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-inuse-admin")
	token := h.token(admin)
	id := medPolicy(t, h, token, "In use")
	medSeedFile(t, h, id, "in-use/a.png", nil)

	r := updCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/media/policies/%d", id), "", token)
	if r.code != http.StatusConflict || !strings.Contains(r.body, "policy_in_use") {
		t.Errorf("delete policy with files = %d %s, want 409 policy_in_use", r.code, r.body)
	}
}

func TestMediaPolicyMoveRewritesRefs(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-move-admin")
	token := h.token(admin)
	from := medPolicy(t, h, token, "From")
	to := medPolicy(t, h, token, "To")
	file := medSeedFile(t, h, from, "moved/x.png", nil)
	if err := h.db.Exec("INSERT INTO media_refs (file_id, ref_type, ref_id) VALUES (?, 'post', 1)", file).Error; err != nil {
		t.Fatal(err)
	}

	r := updCall(h, http.MethodPost, fmt.Sprintf("/api/admin/media/policies/%d/move", from), fmt.Sprintf(`{"to_policy_id":%d}`, to), token)
	if r.code < 200 || r.code > 299 {
		t.Fatalf("move = %d, want 2xx: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE id = ? AND policy_id = ?", file, to); n != 1 {
		t.Errorf("file did not move to policy %d", to)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_refs WHERE file_id = ?", file); n != 1 {
		t.Errorf("media reference lost during move: %d rows", n)
	}
}

func TestMediaGroupDeleteModes(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-group-admin")
	token := h.token(admin)
	policy := medPolicy(t, h, token, "Groups")

	var ungroupID, trashID int64
	h.db.Raw("INSERT INTO media_groups (name) VALUES ('ungroup-me') RETURNING id").Scan(&ungroupID)
	h.db.Raw("INSERT INTO media_groups (name) VALUES ('trash-me') RETURNING id").Scan(&trashID)
	kept := medSeedFile(t, h, policy, "g1/a.png", &ungroupID)
	trashed := medSeedFile(t, h, policy, "g2/b.png", &trashID)

	if r := updCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/media/groups/%d?files=ungroup", ungroupID), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("delete group ungroup = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE id = ? AND group_id IS NULL AND deleted_at IS NULL", kept); n != 1 {
		t.Errorf("ungroup mode: file not kept outside the group")
	}

	if r := updCall(h, http.MethodDelete, fmt.Sprintf("/api/admin/media/groups/%d?files=trash", trashID), "", token); r.code < 200 || r.code > 299 {
		t.Fatalf("delete group trash = %d: %s", r.code, r.body)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE id = ? AND deleted_at IS NOT NULL", trashed); n != 1 {
		t.Errorf("trash mode: file not moved to the trash")
	}
}

func TestMediaFileUploadLimitsTypesAndSanitizedName(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-upload-admin")
	token := h.token(admin)
	medPolicy(t, h, token, "Uploads")

	oversize := append(medPNG(), bytes.Repeat([]byte{1}, 21<<20)...)
	if r := medUpload(h, token, "big.png", oversize); r.code != http.StatusRequestEntityTooLarge {
		t.Errorf("21 MB upload = %d, want 413 with the 20 MB default", r.code)
	}
	if r := medUpload(h, token, "tool.exe", []byte("MZ\x90\x00executable")); r.code != http.StatusUnsupportedMediaType {
		t.Errorf(".exe upload = %d, want 415", r.code)
	}

	// Go's multipart parser rejects most control bytes in a filename before the handler runs, so the
	// control character here is a tab, which still must not survive into the stored name.
	r := medUpload(h, token, "../../evil\tname.png", medPNG())
	if r.code < 200 || r.code > 299 {
		t.Fatalf("PNG with a hostile name = %d, want 2xx: %s", r.code, r.body)
	}
	var v any
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("upload response is not JSON: %v", err)
	}
	var names []any
	plgCollect(v, "filename", &names)
	if len(names) == 0 {
		t.Fatalf("upload response has no filename: %s", r.body)
	}
	name, _ := names[0].(string)
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		t.Errorf("filename %q keeps path segments", name)
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			t.Errorf("filename %q keeps control character %U", name, c)
		}
	}
}

func TestMediaExifGPSStripped(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-exif-admin")
	token := h.token(admin)
	medPolicy(t, h, token, "Exif")

	r := medUpload(h, token, "gps.jpg", medJPEGWithGPS(t))
	if r.code < 200 || r.code > 299 {
		t.Fatalf("upload JPEG with GPS = %d: %s", r.code, r.body)
	}
	var v any
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		t.Fatalf("upload response is not JSON: %v", err)
	}
	var urls []any
	plgCollect(v, "url", &urls)
	if len(urls) == 0 {
		t.Fatalf("upload response has no url: %s", r.body)
	}
	url, _ := urls[0].(string)
	stored, err := os.ReadFile(filepath.Join("data", "uploads", filepath.FromSlash(strings.TrimPrefix(url, "/uploads/"))))
	if err != nil {
		t.Fatalf("read stored file for %s: %v", url, err)
	}
	if bytes.Contains(stored, []byte("Exif\x00\x00")) {
		t.Error("stored JPEG still carries an Exif segment with GPS data")
	}
	var flagged bool
	h.db.Raw("SELECT exif_stripped FROM media_files WHERE path LIKE '%gps%' LIMIT 1").Scan(&flagged)
	if !flagged {
		t.Error("media_files.exif_stripped not set")
	}
}

func TestMediaFetchBlocksPrivateHosts(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "med-fetch-admin")
	token := h.token(admin)
	medPolicy(t, h, token, "Fetch")

	var hits atomic.Int64
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set(echo.HeaderContentType, "image/png")
		_, _ = w.Write(medPNG())
	}))
	defer internal.Close()

	body := fmt.Sprintf(`{"urls":[%q,"http://169.254.169.254/latest/meta-data/"]}`, internal.URL+"/a.png")
	fetch := updCall(h, http.MethodPost, "/api/admin/media/fetch", body, token)
	if fetch.code == http.StatusNotFound || fetch.code == http.StatusNotImplemented || fetch.code >= 500 {
		t.Errorf("POST /api/admin/media/fetch = %d, want a per-URL result: %s", fetch.code, fetch.body)
	}
	if hits.Load() != 0 {
		t.Errorf("fetch reached a loopback host %d time(s)", hits.Load())
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_files"); n != 0 {
		t.Errorf("fetch of blocked hosts stored %d files", n)
	}
}

func TestMediaLegacyUploadsImportedOnStart(t *testing.T) {
	h := newAccessHarness(t)
	if err := os.MkdirAll(filepath.Join("data", "uploads", "legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "uploads", "legacy", "cat.jpg"), medJPEGWithGPS(t), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second start over the same database and data directory runs the
	// legacy import.
	restarted := echo.New()
	RegisterRoutes(restarted, theme.NewManager("web/themes"), nil)

	if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE path = 'legacy/cat.jpg' AND source = 'legacy'"); n != 1 {
		t.Errorf("legacy upload rows = %d, want 1", n)
	}
	rec := httptest.NewRecorder()
	restarted.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/uploads/legacy/cat.jpg", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("legacy public URL /uploads/legacy/cat.jpg = %d after import, want 200", rec.Code)
	}
}

func TestMediaS3PolicyFakeServer(t *testing.T) {
	t.Skip("minio-go is not in go.mod and internal/storage.S3 is a stub; the fake S3 PUT/GET round trip needs the 08d driver")
}
