package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/storage"
)

// An unsuccessful write deliberately installs another writer's object after
// Stat reported a missing destination. The error does not grant ownership.
type mediaOwnershipDriver struct {
	objects           map[string][]byte
	writeErr          error
	missingStat       bool
	wroteAfterMissing bool
	deletes           []string
}

func (d *mediaOwnershipDriver) Put(_ context.Context, p string, r io.Reader, _ int64, _ string) error {
	d.wroteAfterMissing = d.missingStat
	if d.writeErr != nil {
		d.objects[p] = []byte("foreign-writer-bytes")
		return fmt.Errorf("injected write failure: %w", d.writeErr)
	}
	b, err := io.ReadAll(r)
	if err == nil {
		d.objects[p] = b
	}
	return err
}
func (d *mediaOwnershipDriver) Open(_ context.Context, p string) (io.ReadCloser, error) {
	b, ok := d.objects[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (d *mediaOwnershipDriver) Stat(_ context.Context, p string) (storage.Info, error) {
	b, ok := d.objects[p]
	if !ok {
		d.missingStat = true
		return storage.Info{}, os.ErrNotExist
	}
	return storage.Info{Path: p, Size: int64(len(b)), ModTime: time.Unix(1, 0)}, nil
}
func (d *mediaOwnershipDriver) Delete(_ context.Context, p string) error {
	d.deletes = append(d.deletes, p)
	delete(d.objects, p)
	return nil
}
func (*mediaOwnershipDriver) URL(string) string          { return "" }
func (*mediaOwnershipDriver) Test(context.Context) error { return nil }

type mediaOwnershipExclusiveDriver struct{ *mediaOwnershipDriver }

func (d *mediaOwnershipExclusiveDriver) PutExclusive(ctx context.Context, p string, r io.Reader, n int64, mime string) error {
	return d.Put(ctx, p, r, n, mime)
}

func TestMediaCopyOwnershipFailedWritePreservesForeignObject(t *testing.T) {
	ordinaryErr := errors.New("destination write failed")
	for _, exclusive := range []bool{false, true} {
		for _, cause := range []struct {
			name string
			err  error
		}{
			{"ordinary_error", ordinaryErr}, {"cancelled", context.Canceled},
		} {
			t.Run(fmt.Sprintf("exclusive_%t/%s", exclusive, cause.name), func(t *testing.T) {
				src := &mediaOwnershipDriver{objects: map[string][]byte{"source.bin": []byte("source-bytes")}}
				dst := &mediaOwnershipDriver{objects: map[string][]byte{}, writeErr: cause.err}
				var driver storage.Driver = dst
				if exclusive {
					driver = &mediaOwnershipExclusiveDriver{dst}
				}
				err := copyMediaObject(context.Background(), src, driver, "source.bin", "destination.bin", mediaFileRow{Size: 12, Mime: "application/octet-stream"})
				if !errors.Is(err, cause.err) {
					t.Errorf("copy error = %v, want original error in chain", err)
				}
				if !dst.wroteAfterMissing {
					t.Error("fixture did not write after the missing-object Stat")
				}
				if len(dst.deletes) != 0 {
					t.Errorf("failed write deleted paths %v without owning them", dst.deletes)
				}
				if got := string(dst.objects["destination.bin"]); got != "foreign-writer-bytes" {
					t.Errorf("foreign bytes = %q, want preserved", got)
				}
				if got := string(src.objects["source.bin"]); got != "source-bytes" {
					t.Errorf("source bytes changed: %q", got)
				}
			})
		}
	}
}

func TestMediaCopyOwnershipSuccessfulWriteValidationCleansOnlyOwnedCopy(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		for _, mismatch := range []string{"size", "checksum"} {
			t.Run(fmt.Sprintf("exclusive_%t/%s", exclusive, mismatch), func(t *testing.T) {
				src := &mediaOwnershipDriver{objects: map[string][]byte{"source.bin": []byte("source-bytes")}}
				dst := &mediaOwnershipDriver{objects: map[string][]byte{"unrelated.bin": []byte("keep")}}
				row := mediaFileRow{Size: 12, Mime: "application/octet-stream"}
				if mismatch == "size" {
					row.Size++
				} else {
					row.Sha256 = strings.Repeat("0", 64)
				}
				var driver storage.Driver = dst
				if exclusive {
					driver = &mediaOwnershipExclusiveDriver{dst}
				}
				err := copyMediaObject(context.Background(), src, driver, "source.bin", "destination.bin", row)
				if err == nil || !strings.Contains(err.Error(), mismatch+" mismatch") {
					t.Errorf("copy error = %v, want %s mismatch", err, mismatch)
				}
				if !dst.wroteAfterMissing {
					t.Error("fixture never successfully wrote the destination")
				}
				if !reflect.DeepEqual(dst.deletes, []string{"destination.bin"}) {
					t.Errorf("cleanup = %v, want only owned destination", dst.deletes)
				}
				if _, exists := dst.objects["destination.bin"]; exists {
					t.Error("invalid owned copy remains")
				}
				if got := string(dst.objects["unrelated.bin"]); got != "keep" {
					t.Errorf("unrelated bytes = %q", got)
				}
				if got := string(src.objects["source.bin"]); got != "source-bytes" {
					t.Errorf("source bytes = %q", got)
				}
			})
		}
	}
}

func mediaIdentitySeal(t *testing.T, accessKey string) []byte {
	t.Helper()
	sealed, err := sealMediaCredentials(mediaS3Credentials{AccessKey: accessKey, SecretKey: "identity-test-secret"})
	if err != nil {
		t.Fatalf("seal fixture credentials: %v", err)
	}
	return sealed
}

func TestMediaStorageIdentityPhysicalNamespace(t *testing.T) {
	// Credential sealing is isolated from the checkout and requires no database.
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/auth-secrets.json", []byte(`{"totp_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`), 0600); err != nil {
		t.Fatal(err)
	}
	firstSecret, secondSecret := mediaIdentitySeal(t, "identity-source"), mediaIdentitySeal(t, "identity-target")
	local := mediaPolicyRow{Driver: "local", Purpose: "media", Config: `{"marker":"one"}`, SecretEnc: []byte("unused-one")}
	otherLocal := mediaPolicyRow{Driver: "local", Purpose: "media", Config: `invalid ignored local config`, SecretEnc: []byte("unused-two")}
	backup := local
	backup.Purpose = "backup"
	plugin := mediaPolicyRow{Driver: "plugin:identity-provider", Purpose: "media", Config: `{"marker":1}`, SecretEnc: []byte("unused-one")}
	samePlugin := mediaPolicyRow{Driver: plugin.Driver, Purpose: "backup", Config: `{"marker":2}`, SecretEnc: []byte("unused-two")}
	otherPlugin := samePlugin
	otherPlugin.Driver = "plugin:other-provider"
	s3 := mediaPolicyRow{Driver: "s3", Purpose: "media", Config: `{"endpoint":"objects.example.test:9443","secure":true,"bucket":"identity-bucket","prefix":"/shared//images/","region":"us-east-1","public_base_url":"https://source.example.test"}`, SecretEnc: firstSecret}
	sameS3 := mediaPolicyRow{Driver: "s3", Purpose: "media", Config: `{"public_base_url":"https://target.example.test","region":"ap-northeast-1","prefix":"shared/images","bucket":"identity-bucket","secure":true,"endpoint":"https://objects.example.test:9443/"}`, SecretEnc: secondSecret}
	cases := []struct {
		name string
		a, b mediaPolicyRow
		want bool
	}{
		{"local_ignores_unused_metadata", local, otherLocal, true},
		{"local_media_and_backup_have_distinct_roots", local, backup, false},
		{"same_plugin_provider_ignores_policy_metadata", plugin, samePlugin, true},
		{"different_plugin_provider", plugin, otherPlugin, false},
		{"different_driver", local, plugin, false},
		{"s3_origin_prefix_credentials_and_json_order", s3, sameS3, true},
	}
	for _, change := range []struct{ name, old, new string }{
		{"bucket", "identity-bucket", "other-bucket"},
		{"prefix", "shared/images", "shared/other"},
		{"endpoint", "objects.example.test:9443", "other.example.test:9443"},
		{"transport", "https://objects.example.test:9443/", "http://objects.example.test:9443/"},
	} {
		changed := sameS3
		changed.Config = strings.ReplaceAll(changed.Config, change.old, change.new)
		cases = append(cases, struct {
			name string
			a, b mediaPolicyRow
			want bool
		}{"s3_different_" + change.name, s3, changed, false})
	}
	for _, invalid := range []string{`{`, `{}`, `{"endpoint":"http://objects.example.test/path","bucket":"identity-bucket"}`} {
		bad := s3
		bad.Config = invalid
		cases = append(cases, struct {
			name string
			a, b mediaPolicyRow
			want bool
		}{"invalid_s3_" + strconv.Itoa(len(cases)), bad, bad, false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaStorageSame(tc.a, tc.b); got != tc.want {
				t.Errorf("same physical namespace = %t, want %t", got, tc.want)
			}
			if got := mediaStorageSame(tc.b, tc.a); got != tc.want {
				t.Errorf("reverse namespace comparison = %t, want %t", got, tc.want)
			}
		})
	}
}

// This is an HTTP S3 probe, not a replacement Driver. policyDriver therefore
// exercises the real minio signing, HEAD, lazy GET and response parsing paths.
type mediaIdentityS3Probe struct {
	mu         sync.Mutex
	objects    map[string][]byte
	mode       string
	targetGETs int
	mutations  []string
}

const mediaIdentityS3Source = "/identity-bucket/shared/source.bin"
const mediaIdentityS3Keep = "/identity-bucket/shared/keep.bin"

func (p *mediaIdentityS3Probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	source := strings.Contains(auth, "Credential=identity-source/")
	target := strings.Contains(auth, "Credential=identity-target/")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") || (!source && !target) {
		p.fail(w, r, http.StatusForbidden, "AccessDenied")
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodDelete {
		p.mu.Lock()
		p.mutations = append(p.mutations, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
	}
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		p.mu.Lock()
		body, exists := p.objects[r.URL.Path]
		body = bytes.Clone(body)
		mode := p.mode
		isTargetRead := target && r.Method == http.MethodGet && r.URL.Path == mediaIdentityS3Source
		if isTargetRead {
			p.targetGETs++
		}
		p.mu.Unlock()
		if !exists {
			p.fail(w, r, http.StatusNotFound, "NoSuchKey")
			return
		}
		if isTargetRead {
			switch mode {
			case "target_get_denied":
				p.fail(w, r, http.StatusForbidden, "AccessDenied")
				return
			case "target_region_denied":
				if strings.Contains(auth, "/ap-northeast-1/s3/aws4_request") {
					p.fail(w, r, http.StatusBadRequest, "AuthorizationHeaderMalformed")
					return
				}
			case "target_checksum_mismatch":
				body = bytes.Repeat([]byte("x"), len(body))
			case "target_size_mismatch":
				body = body[:len(body)-1]
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"0123456789abcdef0123456789abcdef"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	case http.MethodPut:
		// Reading this streaming body may trigger a simultaneous source GET.
		// Never hold the object-map mutex while waiting for network I/O.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			p.fail(w, r, http.StatusBadRequest, "InvalidRequest")
			return
		}
		p.mu.Lock()
		p.objects[r.URL.Path] = body
		p.mu.Unlock()
		w.Header().Set("ETag", `"0123456789abcdef0123456789abcdef"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		p.mu.Lock()
		delete(p.objects, r.URL.Path)
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		p.fail(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}
func (*mediaIdentityS3Probe) fail(w http.ResponseWriter, r *http.Request, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = fmt.Fprintf(w, `<Error><Code>%s</Code><Message>fixture rejection</Message></Error>`, code)
	}
}

func mediaIdentityRead(t *testing.T, policy mediaPolicyRow, path string) []byte {
	t.Helper()
	driver, err := policyDriver(policy)
	if err != nil {
		t.Fatalf("construct fixture S3 driver: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, err := driver.Open(ctx, path)
	if err != nil {
		t.Fatalf("open fixture S3 object: %v", err)
	}
	got, readErr := io.ReadAll(in)
	closeErr := in.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read fixture S3 object: read=%v close=%v", readErr, closeErr)
	}
	return got
}

func TestMediaStorageIdentityS3MoveVerifiesTargetAndPreservesObjects(t *testing.T) {
	for _, mode := range []string{"success", "target_get_denied", "target_region_denied", "target_checksum_mismatch", "target_size_mismatch"} {
		t.Run(mode, func(t *testing.T) {
			h := newAccessHarness(t)
			admin := h.user("admin", "media-identity-admin")
			token := h.token(admin)
			from := medPolicy(t, h, token, "Identity source")
			to := medPolicy(t, h, token, "Identity destination")
			wantSource, wantKeep := []byte("source-bytes"), []byte("existing-target-bytes")
			probe := &mediaIdentityS3Probe{mode: mode, objects: map[string][]byte{mediaIdentityS3Source: bytes.Clone(wantSource), mediaIdentityS3Keep: bytes.Clone(wantKeep)}}
			srv := httptest.NewServer(probe)
			defer srv.Close()
			sourceConfig, err := json.Marshal(mediaS3StoredConfig{Endpoint: srv.URL, Bucket: "identity-bucket", Prefix: "/shared/", Region: "us-east-1", PublicBaseURL: srv.URL + "/public-source"})
			if err != nil {
				t.Fatal(err)
			}
			targetConfig, err := json.Marshal(mediaS3StoredConfig{Endpoint: strings.TrimPrefix(srv.URL, "http://"), Bucket: "identity-bucket", Prefix: "shared", Region: "ap-northeast-1", PublicBaseURL: srv.URL + "/public-target"})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []struct {
				id  int64
				cfg []byte
				key string
			}{
				{from, sourceConfig, "identity-source"}, {to, targetConfig, "identity-target"},
			} {
				if err := h.db.Exec("UPDATE media_policies SET driver = 's3', config = ?, secret_enc = ? WHERE id = ?", string(p.cfg), mediaIdentitySeal(t, p.key), p.id).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Prove the fixture can serve the real SDK before testing the move.
			var sourcePolicy mediaPolicyRow
			if err := h.db.Table("media_policies").Where("id = ?", from).Take(&sourcePolicy).Error; err != nil {
				t.Fatal(err)
			}
			if got := mediaIdentityRead(t, sourcePolicy, "source.bin"); !bytes.Equal(got, wantSource) {
				t.Fatalf("source fixture bytes = %q, want %q", got, wantSource)
			}
			fileID := medSeedFile(t, h, from, "source.bin", nil)
			keepID := medSeedFile(t, h, to, "keep.bin", nil)
			digest := fmt.Sprintf("%x", sha256.Sum256(wantSource))
			if err := h.db.Exec("UPDATE media_files SET size = ?, sha256 = ? WHERE id = ?", len(wantSource), digest, fileID).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Exec("UPDATE media_files SET size = ? WHERE id = ?", len(wantKeep), keepID).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Exec("INSERT INTO media_refs (file_id, ref_type, ref_id) VALUES (?, 'post', 12345)", fileID).Error; err != nil {
				t.Fatal(err)
			}
			var before, keepBefore mediaFileRow
			if err := h.db.Table("media_files").Where("id = ?", fileID).Take(&before).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Table("media_files").Where("id = ?", keepID).Take(&keepBefore).Error; err != nil {
				t.Fatal(err)
			}

			moveCtx, cancelMove := context.WithTimeout(context.Background(), 15*time.Second)
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/admin/media/policies/%d/move", from), strings.NewReader(fmt.Sprintf(`{"to_policy_id":%d}`, to))).WithContext(moveCtx)
			req.Header.Set("X-Jelly-CSRF", "1")
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token})
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			moveErr := moveCtx.Err()
			cancelMove()
			if moveErr != nil {
				t.Fatalf("move request exceeded fixture deadline: %v", moveErr)
			}
			response := accessResult{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
			var after, keepAfter mediaFileRow
			if err := h.db.Table("media_files").Where("id = ?", fileID).Take(&after).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Table("media_files").Where("id = ?", keepID).Take(&keepAfter).Error; err != nil {
				t.Fatal(err)
			}
			if mode == "success" {
				if response.code != http.StatusOK {
					t.Errorf("move = %d, want 200: %s", response.code, response.body)
				}
				if after.PolicyID != to || after.Path != before.Path || after.Size != before.Size || after.Sha256 != before.Sha256 {
					t.Errorf("successful move did not retain path, size and hash while switching policy: %+v", after)
				}
			} else {
				if response.code < 400 || response.code > 599 {
					t.Errorf("unverified target move = %d, want rejected move: %s", response.code, response.body)
				}
				if !reflect.DeepEqual(after, before) {
					t.Errorf("rejected move changed source row: before %+v, after %+v", before, after)
				}
			}
			if !reflect.DeepEqual(keepAfter, keepBefore) {
				t.Error("move changed existing target row")
			}
			if n := impCount(t, h, "SELECT COUNT(*) FROM media_refs WHERE file_id = ? AND ref_type = 'post' AND ref_id = 12345", fileID); n != 1 {
				t.Errorf("source reference changed: count=%d", n)
			}
			if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE policy_id IN (?, ?)", from, to); n != 2 {
				t.Errorf("move changed file row count: %d", n)
			}
			probe.mu.Lock()
			if probe.targetGETs == 0 {
				t.Error("move never read the source path with target credentials")
			}
			if len(probe.mutations) != 0 {
				t.Errorf("same-location move mutated S3 objects: %v", probe.mutations)
			}
			if !bytes.Equal(probe.objects[mediaIdentityS3Source], wantSource) {
				t.Error("original source object bytes were changed or removed")
			}
			if !bytes.Equal(probe.objects[mediaIdentityS3Keep], wantKeep) {
				t.Error("existing target object bytes were changed or removed")
			}
			if len(probe.objects) != 2 {
				t.Errorf("move changed object count: %d", len(probe.objects))
			}
			probe.mu.Unlock()
			if mode == "success" && after.PolicyID == to && after.Path == before.Path {
				var policy mediaPolicyRow
				if err := h.db.Table("media_policies").Where("id = ?", to).Take(&policy).Error; err != nil {
					t.Fatal(err)
				}
				if got := mediaIdentityRead(t, policy, after.Path); !bytes.Equal(got, wantSource) {
					t.Errorf("moved bytes = %q, want %q", got, wantSource)
				}
			}
		})
	}
}
