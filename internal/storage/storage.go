// Package storage is the attachment storage driver interface. Drivers:
// local (local.go, files under a root directory) and s3 (minio-go). Plugins
// may provide drivers through the media.storage extension point.
package storage

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"
)

// Driver names used in media_policies.driver.
const (
	DriverLocal = "local"
	DriverS3    = "s3"
)

// ErrInvalidPath is returned for names that escape the driver root.
var ErrInvalidPath = errors.New("storage: invalid path")

// ErrNotImplemented is retained for callers handling optional storage drivers.
var ErrNotImplemented = errors.New("storage: not implemented")

// ErrS3Disabled is retained for source compatibility; the built-in S3 driver
// is enabled now and does not return this sentinel during normal operations.
var ErrS3Disabled = errors.New("storage: s3 driver is not enabled in this build")

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

// S3 is an S3-compatible object driver.
type S3 struct {
	Endpoint      string
	Bucket        string
	Region        string
	Prefix        string
	PublicBaseURL string
	client        s3API
	initErr       error
}

// NewS3 uses standard AWS environment credentials and keeps the existing API.
func NewS3(endpoint, bucket string) *S3 {
	d, err := NewS3WithConfig(S3Config{Endpoint: endpoint, Bucket: bucket})
	if d != nil {
		return d
	}
	return &S3{Endpoint: endpoint, Bucket: bucket, initErr: err}
}

var (
	_ Driver = (*Local)(nil)
	_ Driver = (*S3)(nil)
)
