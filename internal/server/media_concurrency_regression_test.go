package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/storage"
)

type mediaExclusiveRaceDriver struct {
	objects     map[string][]byte
	deleteCalls int
}

func (d *mediaExclusiveRaceDriver) Put(_ context.Context, path string, r io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.objects[path] = data
	return nil
}

func (d *mediaExclusiveRaceDriver) PutExclusive(_ context.Context, path string, _ io.Reader, _ int64, _ string) error {
	if _, exists := d.objects[path]; exists {
		return os.ErrExist
	}
	d.objects[path] = []byte("foreign-writer-bytes")
	return os.ErrExist
}

func (d *mediaExclusiveRaceDriver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	data, ok := d.objects[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (d *mediaExclusiveRaceDriver) Stat(_ context.Context, path string) (storage.Info, error) {
	data, ok := d.objects[path]
	if !ok {
		return storage.Info{}, os.ErrNotExist
	}
	return storage.Info{Path: path, Size: int64(len(data)), ModTime: time.Now()}, nil
}

func (d *mediaExclusiveRaceDriver) Delete(_ context.Context, path string) error {
	d.deleteCalls++
	delete(d.objects, path)
	return nil
}

func (*mediaExclusiveRaceDriver) URL(string) string          { return "" }
func (*mediaExclusiveRaceDriver) Test(context.Context) error { return nil }

func TestMediaConcurrencyRegressionExclusiveCreateDoesNotDeleteConcurrentObject(t *testing.T) {
	ctx := context.Background()
	source := &mediaExclusiveRaceDriver{objects: map[string][]byte{"source.png": []byte("source-bytes")}}
	destination := &mediaExclusiveRaceDriver{objects: map[string][]byte{}}
	row := mediaFileRow{Size: int64(len(source.objects["source.png"])), Mime: "image/png"}

	err := copyMediaObject(ctx, source, destination, "source.png", "target.png", row)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("copyMediaObject() error = %v, want exclusive-create collision", err)
	}
	if destination.deleteCalls != 0 {
		t.Errorf("collision cleanup called Delete %d times; it must preserve the object owned by the concurrent writer", destination.deleteCalls)
	}
	if got := string(destination.objects["target.png"]); got != "foreign-writer-bytes" {
		t.Errorf("concurrent writer's target bytes = %q, want preserved foreign bytes", got)
	}
}

func TestMediaConcurrencyRegressionMovePathCollisionPreservesTargetObject(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "media-move-path-collision-admin")
	token := h.token(admin)
	from := medPolicy(t, h, token, "Move collision source")
	to := medPolicy(t, h, token, "Move collision destination")
	// Local policy drivers share data/uploads regardless of Config. Distinct
	// stored configs make the move path treat them as separate stores, while
	// both rows still refer to the same physical path.
	if err := h.db.Exec("UPDATE media_policies SET config = ? WHERE id = ?", `{"marker":"source"}`, from).Error; err != nil {
		t.Fatalf("set source policy config: %v", err)
	}
	if err := h.db.Exec("UPDATE media_policies SET config = ? WHERE id = ?", `{"marker":"target"}`, to).Error; err != nil {
		t.Fatalf("set destination policy config: %v", err)
	}
	const sharedPath = "collision/shared.png"
	sourceID := medSeedFile(t, h, from, sharedPath, nil)
	targetID := medSeedFile(t, h, to, sharedPath, nil)
	physical := filepath.Join("data", "uploads", filepath.FromSlash(sharedPath))
	wantTarget := []byte("data")
	if err := os.MkdirAll(filepath.Dir(physical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(physical, wantTarget, 0o644); err != nil {
		t.Fatal(err)
	}

	response := updCall(h, http.MethodPost, "/api/admin/media/policies/"+itoa64(from)+"/move", `{"to_policy_id":`+itoa64(to)+`}`, token)
	if response.code == http.StatusConflict {
		if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE id = ? AND policy_id = ? AND path = ?", sourceID, from, sharedPath); n != 1 {
			t.Errorf("rejected move changed the source media row; count=%d", n)
		}
	} else if response.code < 200 || response.code >= 300 {
		t.Fatalf("move returned %d, want 409 path_conflict or a safe success: %s", response.code, response.body)
	} else if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE id = ? AND policy_id = ?", sourceID, to); n != 1 {
		t.Errorf("successful move did not move source row to target policy; count=%d", n)
	}
	if n := impCount(t, h, "SELECT COUNT(*) FROM media_files WHERE id = ? AND policy_id = ? AND path = ?", targetID, to, sharedPath); n != 1 {
		t.Fatalf("target collision row was removed or changed; count=%d", n)
	}
	if got, err := os.ReadFile(physical); err != nil || !bytes.Equal(got, wantTarget) {
		t.Errorf("target collision object's bytes = %q, read error %v; want preserved %q", got, err, wantTarget)
	}
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
