package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// uploadsURLPrefix is the public prefix that /uploads serves (uploads.go).
const uploadsURLPrefix = "/uploads/"

// Local stores objects below Root. /uploads serves them, so URL returns the
// public address under that route.
type Local struct {
	Root string
}

// NewLocal returns a local driver rooted at root.
func NewLocal(root string) *Local {
	return &Local{Root: root}
}

// resolve maps a slash path to a file below Root. It rejects any result that
// does not stay inside Root, which CleanPath alone cannot guarantee for
// hostile roots such as symlinked directories.
func (l *Local) resolve(p string) (string, error) {
	clean, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	root := filepath.Clean(l.Root)
	full := filepath.Join(root, filepath.FromSlash(clean))
	if !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	return full, nil
}

// Put writes r to p through a temporary file in the same directory, so a
// reader never sees a half-written file. size is advisory.
func (l *Local) Put(_ context.Context, p string, r io.Reader, size int64, contentType string) error {
	full, err := l.resolve(p)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage: create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*.tmp")
	if err != nil {
		return fmt.Errorf("storage: create temporary file for %s: %w", p, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("storage: write %s: %w", p, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: close %s: %w", p, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("storage: chmod %s: %w", p, err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return fmt.Errorf("storage: rename %s: %w", p, err)
	}
	committed = true
	return nil
}

// Open opens p for reading.
func (l *Local) Open(_ context.Context, p string) (io.ReadCloser, error) {
	full, err := l.resolve(p)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

// Stat returns the size and modification time of p.
func (l *Local) Stat(_ context.Context, p string) (Info, error) {
	full, err := l.resolve(p)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return Info{}, err
	}
	if st.IsDir() {
		return Info{}, os.ErrNotExist
	}
	return Info{Path: p, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Delete removes p.
func (l *Local) Delete(_ context.Context, p string) error {
	full, err := l.resolve(p)
	if err != nil {
		return err
	}
	return os.Remove(full)
}

// URL returns the /uploads address of p. Each segment is escaped so names with
// reserved characters still form a valid path.
func (l *Local) URL(p string) string {
	clean, err := CleanPath(p)
	if err != nil {
		return ""
	}
	segments := strings.Split(clean, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return uploadsURLPrefix + strings.Join(segments, "/")
}

// Test checks that the root exists (creating it when missing) and that a
// file can be written and removed in it.
func (l *Local) Test(context.Context) error {
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return err
	}
	st, err := os.Stat(l.Root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("storage: %s is not a directory", l.Root)
	}
	probe, err := os.CreateTemp(l.Root, ".probe-*.tmp")
	if err != nil {
		return fmt.Errorf("storage: %s is not writable: %w", l.Root, err)
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

var _ Driver = (*Local)(nil)
