// Package storage is the attachment storage driver interface. Drivers:
// local (local.go, files under a root directory) and s3 (minio-go, stub). Plugins
// may provide drivers through the media.storage extension point.
package storage

import (
	"context"
	"errors"
	"fmt"
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

// S3 is a stub for the S3-compatible driver. minio-go is not in go.mod yet, so
// every operation reports ErrNotImplemented and the policy is kept but unusable.
type S3 struct {
	Endpoint string
	Bucket   string
}

// NewS3 returns the stub; every operation reports ErrNotImplemented.
func NewS3(endpoint, bucket string) *S3 {
	return &S3{Endpoint: endpoint, Bucket: bucket}
}

// ErrS3Disabled names the state of the S3 driver so admins see why a policy fails.
var ErrS3Disabled = fmt.Errorf("%w: the s3 driver is not enabled in this build", ErrNotImplemented)

func (s *S3) Put(context.Context, string, io.Reader, int64, string) error {
	return ErrS3Disabled
}
func (s *S3) Open(context.Context, string) (io.ReadCloser, error) { return nil, ErrS3Disabled }
func (s *S3) Stat(context.Context, string) (Info, error)          { return Info{}, ErrS3Disabled }
func (s *S3) Delete(context.Context, string) error                { return ErrS3Disabled }
func (s *S3) URL(string) string                                   { return "" }
func (s *S3) Test(context.Context) error                          { return ErrS3Disabled }

var (
	_ Driver = (*Local)(nil)
	_ Driver = (*S3)(nil)
)
