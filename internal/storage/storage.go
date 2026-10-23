// Package storage is the attachment storage driver interface. Drivers:
// local (files under a root directory, implemented here) and s3 (minio-go,
// stub). Plugins may provide drivers through the media.storage extension point.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Driver names used in media_policies.driver.
const (
	DriverLocal = "local"
	DriverS3    = "s3"
)

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("storage: not implemented")

// ErrInvalidPath is returned for names that escape the driver root.
var ErrInvalidPath = errors.New("storage: invalid path")

// Info describes a stored object.
type Info struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// Driver is one storage backend. Paths are slash-separated and relative.
type Driver interface {
	Put(ctx context.Context, p string, r io.Reader, size int64, contentType string) error
	Open(ctx context.Context, p string) (io.ReadCloser, error)
	Stat(ctx context.Context, p string) (Info, error)
	Delete(ctx context.Context, p string) error
	// URL returns the public address of p, or "" when the driver serves nothing directly.
	URL(p string) string
	Test(ctx context.Context) error
}

// CleanPath validates a relative slash path and returns its clean form.
// It rejects absolute paths, ".." segments, backslashes and control characters.
func CleanPath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return "", ErrInvalidPath
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", ErrInvalidPath
		}
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrInvalidPath
	}
	return clean, nil
}

// Local stores objects below Root. It serves nothing itself; /uploads does.
type Local struct {
	Root string
}

// NewLocal returns a local driver rooted at root.
func NewLocal(root string) *Local {
	return &Local{Root: root}
}

func (l *Local) resolve(p string) (string, error) {
	clean, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	return filepath.Join(l.Root, filepath.FromSlash(clean)), nil
}

// Put writes r to p, creating parent directories. size is advisory.
// TODO(08d): write to a temporary file and rename.
func (l *Local) Put(ctx context.Context, p string, r io.Reader, size int64, contentType string) error {
	full, err := l.resolve(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("storage: create directory: %w", err)
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("storage: create %s: %w", p, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("storage: write %s: %w", p, err)
	}
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

// URL returns "" because the local driver is served by the /uploads route.
func (l *Local) URL(string) string { return "" }

// Test checks that the root exists and is a directory.
func (l *Local) Test(context.Context) error {
	st, err := os.Stat(l.Root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("storage: %s is not a directory", l.Root)
	}
	return nil
}

// S3 is a stub for the S3-compatible driver (minio-go, ticket 08d).
type S3 struct {
	Endpoint string
	Bucket   string
}

// NewS3 returns the stub; every operation reports ErrNotImplemented.
func NewS3(endpoint, bucket string) *S3 {
	return &S3{Endpoint: endpoint, Bucket: bucket}
}

func (s *S3) Put(context.Context, string, io.Reader, int64, string) error {
	return ErrNotImplemented
}
func (s *S3) Open(context.Context, string) (io.ReadCloser, error) { return nil, ErrNotImplemented }
func (s *S3) Stat(context.Context, string) (Info, error)          { return Info{}, ErrNotImplemented }
func (s *S3) Delete(context.Context, string) error                { return ErrNotImplemented }
func (s *S3) URL(string) string                                   { return "" }
func (s *S3) Test(context.Context) error                          { return ErrNotImplemented }

var (
	_ Driver = (*Local)(nil)
	_ Driver = (*S3)(nil)
)
