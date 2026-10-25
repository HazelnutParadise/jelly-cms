package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config is additive configuration for the S3-compatible storage driver.
// Empty credentials use the standard AWS environment/credential chain.
type S3Config struct {
	Endpoint      string
	Bucket        string
	Region        string
	Prefix        string
	AccessKey     string
	SecretKey     string
	SessionToken  string
	Secure        bool
	PublicBaseURL string
}

type s3API interface {
	BucketExists(context.Context, string) (bool, error)
	PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
	GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
	StatObject(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error)
	RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
	PresignedGetObject(context.Context, string, string, time.Duration, url.Values) (*url.URL, error)
}

// Some S3-compatible gateways omit Last-Modified from successful HEAD object
// responses. minio-go currently treats that optional HTTP metadata as fatal;
// expose an explicit zero-time sentinel while retaining all response data.
type s3MetadataTransport struct{ base http.RoundTripper }

func (t s3MetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil && (req.Method == http.MethodHead || req.Method == http.MethodGet) && resp.StatusCode >= 200 && resp.StatusCode < 300 && resp.Header.Get("Last-Modified") == "" {
		resp.Header.Set("Last-Modified", time.Unix(0, 0).UTC().Format(http.TimeFormat))
	}
	return resp, err
}

// NewS3WithConfig constructs an S3-compatible driver using minio-go's signed
// requests. Endpoint may include a scheme; paths are not allowed.
func NewS3WithConfig(cfg S3Config) (*S3, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("storage: s3 endpoint and bucket are required")
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	secure := cfg.Secure
	if strings.HasPrefix(strings.ToLower(endpoint), "http://") || strings.HasPrefix(strings.ToLower(endpoint), "https://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("storage: parse s3 endpoint: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("storage: unsupported s3 endpoint scheme %q", u.Scheme)
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return nil, fmt.Errorf("storage: s3 endpoint must be an origin without credentials, path, query, or fragment")
		}
		secure = u.Scheme == "https"
		endpoint = u.Host
	} else if strings.Contains(endpoint, "://") {
		return nil, fmt.Errorf("storage: unsupported s3 endpoint scheme")
	}
	if strings.Contains(endpoint, "/") || strings.Contains(endpoint, "?") || strings.Contains(endpoint, "#") {
		return nil, fmt.Errorf("storage: invalid s3 endpoint")
	}
	if cfg.Region == "" {
		cfg.Region = os.Getenv("AWS_REGION")
	}
	if cfg.Region == "" {
		cfg.Region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	var creds *credentials.Credentials
	if cfg.AccessKey != "" || cfg.SecretKey != "" || cfg.SessionToken != "" {
		if cfg.AccessKey == "" || cfg.SecretKey == "" {
			return nil, fmt.Errorf("storage: s3 access key and secret key must be supplied together")
		}
		creds = credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)
	} else {
		creds = credentials.NewEnvAWS()
	}
	transport := http.DefaultTransport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: creds, Secure: secure, Region: cfg.Region, Transport: s3MetadataTransport{base: transport}})
	if err != nil {
		return nil, fmt.Errorf("storage: create s3 client: %w", err)
	}
	prefix := ""
	if strings.TrimSpace(cfg.Prefix) != "" {
		p, err := CleanPath(strings.Trim(cfg.Prefix, "/"))
		if err != nil {
			return nil, fmt.Errorf("storage: invalid s3 prefix: %w", err)
		}
		prefix = p
	}
	return &S3{Endpoint: endpoint, Bucket: cfg.Bucket, Region: cfg.Region, Prefix: prefix, PublicBaseURL: strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/"), client: client}, nil
}

func (s *S3) objectKey(p string) (string, error) {
	if s.initErr != nil {
		return "", fmt.Errorf("storage: configure s3 driver: %w", s.initErr)
	}
	clean, err := CleanPath(p)
	if err != nil {
		return "", err
	}
	if s.Prefix != "" {
		return path.Join(s.Prefix, clean), nil
	}
	return clean, nil
}

func (s *S3) Put(ctx context.Context, p string, r io.Reader, size int64, contentType string) error {
	key, err := s.objectKey(p)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("storage: s3 put %q: nil reader", p)
	}
	if size < 0 {
		size = -1
	}
	opts := minio.PutObjectOptions{ContentType: contentType, DisableContentSha256: true}
	if _, err := s.client.PutObject(ctx, s.Bucket, key, r, size, opts); err != nil {
		return fmt.Errorf("storage: s3 put %q: %w", p, err)
	}
	return nil
}
func (s *S3) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	key, err := s.objectKey(p)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: s3 open %q: %w", p, err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("storage: s3 open %q: %w", p, err)
	}
	return obj, nil
}
func (s *S3) Stat(ctx context.Context, p string) (Info, error) {
	key, err := s.objectKey(p)
	if err != nil {
		return Info{}, err
	}
	st, err := s.client.StatObject(ctx, s.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return Info{}, fmt.Errorf("storage: s3 stat %q: %w", p, err)
	}
	return Info{Path: p, Size: st.Size, ModTime: st.LastModified}, nil
}
func (s *S3) Delete(ctx context.Context, p string) error {
	key, err := s.objectKey(p)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: s3 delete %q: %w", p, err)
	}
	return nil
}

// URL only returns an unsigned URL when an explicit public base URL is set.
// Private buckets should use PresignedURL instead.
func (s *S3) URL(p string) string {
	if s.PublicBaseURL == "" {
		return ""
	}
	key, err := s.objectKey(p)
	if err != nil {
		return ""
	}
	parts := strings.Split(key, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return s.PublicBaseURL + "/" + strings.Join(parts, "/")
}

// PresignedURL signs a temporary GET for private buckets. The URL expiry is
// bounded to the S3 SigV4 maximum of seven days.
func (s *S3) PresignedURL(ctx context.Context, p string, expiry time.Duration) (string, error) {
	key, err := s.objectKey(p)
	if err != nil {
		return "", err
	}
	if expiry <= 0 || expiry > 7*24*time.Hour {
		return "", fmt.Errorf("storage: s3 presigned URL expiry must be between 0 and 7 days")
	}
	u, err := s.client.PresignedGetObject(ctx, s.Bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("storage: s3 presign %q: %w", p, err)
	}
	return u.String(), nil
}
func (s *S3) Test(ctx context.Context) (retErr error) {
	if s.initErr != nil {
		return fmt.Errorf("storage: configure s3 driver: %w", s.initErr)
	}
	// A signed temporary object round trip verifies the actual permissions the
	// storage driver needs, including on S3-compatible endpoints that do not
	// implement BucketExists consistently.
	probePath := ".jelly-test-" + fmt.Sprint(time.Now().UTC().UnixNano())
	key, err := s.objectKey(probePath)
	if err != nil {
		return err
	}
	if _, err := s.client.PutObject(ctx, s.Bucket, key, strings.NewReader("jelly"), 5, minio.PutObjectOptions{ContentType: "text/plain", DisableContentSha256: true}); err != nil {
		return fmt.Errorf("storage: s3 test write bucket %q: %w", s.Bucket, err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.client.RemoveObject(cleanupCtx, s.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("storage: s3 test cleanup bucket %q: %w", s.Bucket, err))
		}
	}()
	readCtx, cancelRead := context.WithTimeout(ctx, 6*time.Second)
	defer cancelRead()
	obj, err := s.client.GetObject(readCtx, s.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage: s3 test read bucket %q: %w", s.Bucket, err)
	}
	defer obj.Close()
	b, err := io.ReadAll(io.LimitReader(obj, 6))
	if err != nil {
		return fmt.Errorf("storage: s3 test read bucket %q: %w", s.Bucket, err)
	}
	if len(b) != 5 || string(b) != "jelly" {
		return fmt.Errorf("storage: s3 test returned unexpected object content")
	}
	return nil
}

var _ Driver = (*S3)(nil)
