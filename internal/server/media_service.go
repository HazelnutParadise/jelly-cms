package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"gorm.io/gorm"
)

type mediaS3Credentials struct {
	AccessKey    string `json:"access_key,omitempty"`
	SecretKey    string `json:"secret_key,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
}

type mediaS3StoredConfig struct {
	Endpoint      string `json:"endpoint"`
	Bucket        string `json:"bucket"`
	Region        string `json:"region,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	Secure        bool   `json:"secure"`
	PublicBaseURL string `json:"public_base_url,omitempty"`
}

// parseMediaS3Config validates the complete public configuration and extracts
// credentials so they can be encrypted separately from the readable fields.
func parseMediaS3Config(input map[string]any) (mediaS3StoredConfig, mediaS3Credentials, *mediaFailure) {
	var cfg mediaS3StoredConfig
	var creds mediaS3Credentials
	allowed := map[string]bool{
		"endpoint": true, "bucket": true, "region": true, "prefix": true,
		"secure": true, "public_base_url": true,
		"access_key": true, "secret_key": true, "session_token": true,
	}
	for key := range input {
		if !allowed[key] {
			return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "config contains an unsupported field"}
		}
	}
	readString := func(key string) (string, *mediaFailure) {
		v, ok := input[key]
		if !ok {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", &mediaFailure{Status: 400, Code: "invalid_config", Msg: "config fields must have the correct type"}
		}
		return strings.TrimSpace(s), nil
	}
	var fail *mediaFailure
	if cfg.Endpoint, fail = readString("endpoint"); fail != nil {
		return cfg, creds, fail
	}
	if cfg.Bucket, fail = readString("bucket"); fail != nil {
		return cfg, creds, fail
	}
	if cfg.Region, fail = readString("region"); fail != nil {
		return cfg, creds, fail
	}
	if cfg.Prefix, fail = readString("prefix"); fail != nil {
		return cfg, creds, fail
	}
	if cfg.PublicBaseURL, fail = readString("public_base_url"); fail != nil {
		return cfg, creds, fail
	}
	secureProvided := false
	if v, ok := input["secure"]; ok {
		secureProvided = true
		var valid bool
		cfg.Secure, valid = v.(bool)
		if !valid {
			return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "secure must be true or false"}
		}
	}
	if creds.AccessKey, fail = readString("access_key"); fail != nil {
		return cfg, creds, fail
	}
	if creds.SecretKey, fail = readString("secret_key"); fail != nil {
		return cfg, creds, fail
	}
	if creds.SessionToken, fail = readString("session_token"); fail != nil {
		return cfg, creds, fail
	}
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "endpoint and bucket are required for s3"}
	}
	if strings.HasPrefix(strings.ToLower(cfg.Endpoint), "http://") || strings.HasPrefix(strings.ToLower(cfg.Endpoint), "https://") {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "endpoint URL is invalid"}
		}
		tlsFromEndpoint := u.Scheme == "https"
		if secureProvided && cfg.Secure != tlsFromEndpoint {
			return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "secure conflicts with the endpoint URL scheme"}
		}
		cfg.Secure = tlsFromEndpoint
	}
	if (creds.AccessKey == "") != (creds.SecretKey == "") {
		return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "access_key and secret_key must be supplied together"}
	}
	if cfg.PublicBaseURL != "" {
		u, err := url.Parse(cfg.PublicBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "public_base_url must be an absolute http or https URL without credentials, query, or fragment"}
		}
		cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	}
	if cfg.Prefix != "" {
		clean, err := storage.CleanPath(strings.Trim(cfg.Prefix, "/"))
		if err != nil {
			return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "prefix must be a relative storage path"}
		}
		cfg.Prefix = clean
	}
	if _, err := storage.NewS3WithConfig(storage.S3Config{
		Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, Region: cfg.Region, Prefix: cfg.Prefix,
		AccessKey: creds.AccessKey, SecretKey: creds.SecretKey, SessionToken: creds.SessionToken,
		Secure: cfg.Secure, PublicBaseURL: cfg.PublicBaseURL,
	}); err != nil {
		return cfg, creds, &mediaFailure{Status: 400, Code: "invalid_config", Msg: "S3 configuration is invalid"}
	}
	return cfg, creds, nil
}

func mediaCredentialsFromSecret(raw string, cfg mediaS3Credentials) (mediaS3Credentials, error) {
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	var supplied mediaS3Credentials
	if err := json.Unmarshal([]byte(raw), &supplied); err != nil {
		if cfg.AccessKey == "" {
			return cfg, errors.New("S3 access_key is required when secret is supplied as a plain value")
		}
		supplied.SecretKey = raw
	}
	if supplied.AccessKey != "" {
		cfg.AccessKey = supplied.AccessKey
	}
	if supplied.SecretKey != "" {
		cfg.SecretKey = supplied.SecretKey
	}
	if supplied.SessionToken != "" {
		cfg.SessionToken = supplied.SessionToken
	}
	if (cfg.AccessKey == "") != (cfg.SecretKey == "") {
		return cfg, errors.New("S3 access_key and secret_key must be supplied together")
	}
	return cfg, nil
}

func sealMediaCredentials(credentials mediaS3Credentials) ([]byte, error) {
	if credentials == (mediaS3Credentials{}) {
		return nil, nil
	}
	plain, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	return settings.SealBytes("media-policy", plain)
}

func openMediaCredentials(sealed []byte) (mediaS3Credentials, error) {
	var credentials mediaS3Credentials
	if len(sealed) == 0 {
		return credentials, nil
	}
	plain, err := settings.OpenBytes("media-policy", sealed)
	if err != nil {
		return credentials, err
	}
	err = json.Unmarshal(plain, &credentials)
	return credentials, err
}

func mergeMediaCredentials(preferred, fallback mediaS3Credentials) mediaS3Credentials {
	if preferred.AccessKey == "" {
		preferred.AccessKey = fallback.AccessKey
	}
	if preferred.SecretKey == "" {
		preferred.SecretKey = fallback.SecretKey
	}
	if preferred.SessionToken == "" {
		preferred.SessionToken = fallback.SessionToken
	}
	return preferred
}

// mediaWithDB binds worker operations to the database supplied by the system
// runtime without changing the process-global site database.
func mediaWithDB(ctx context.Context, db *gorm.DB) context.Context {
	return context.WithValue(ctx, mediaDBContextKey{}, db)
}

func policyDriver(p mediaPolicyRow, requestContexts ...context.Context) (storage.Driver, error) {
	ctx := context.Background()
	if len(requestContexts) > 0 && requestContexts[0] != nil {
		ctx = requestContexts[0]
	}
	switch {
	case p.Driver == storage.DriverLocal:
		if p.Purpose == "backup" {
			return storage.NewPrivateLocal(filepath.Join("data", "backups", "local")), nil
		}
		return storage.NewLocal(uploadsRoot), nil
	case p.Driver == storage.DriverS3:
		var cfg mediaS3StoredConfig
		if err := json.Unmarshal([]byte(p.Config), &cfg); err != nil {
			return nil, fmt.Errorf("media: invalid S3 policy config: %w", err)
		}
		creds, err := openMediaCredentials(p.SecretEnc)
		if err != nil {
			return nil, fmt.Errorf("media: decrypt S3 policy credentials: %w", err)
		}
		return storage.NewS3WithConfig(storage.S3Config{
			Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, Region: cfg.Region, Prefix: cfg.Prefix,
			AccessKey: creds.AccessKey, SecretKey: creds.SecretKey, SessionToken: creds.SessionToken,
			Secure: cfg.Secure, PublicBaseURL: cfg.PublicBaseURL,
		})
	default:
		if mediaPluginDriver.MatchString(p.Driver) {
			id := strings.TrimPrefix(p.Driver, "plugin:")
			if drv, err := newPluginStorageDriver(ctx, id); err == nil {
				return drv, nil
			}
			for _, provider := range ext.Resolve(ext.PointMediaStorage) {
				if "plugin:"+provider.ID == p.Driver {
					if drv, ok := provider.Provider.(storage.Driver); ok {
						return drv, nil
					}
				}
			}
		}
		return nil, errMediaDriverUnavailable
	}
}

// mediaBackupStorageDriver is an internal bridge for the backup worker. It
// accepts only backup-purpose policies and never returns a public media URL.
func mediaBackupStorageDriver(ctx context.Context, policyID int64) (storage.Driver, error) {
	policy, err := loadMediaPolicy(ctx, policyID)
	if err != nil {
		return nil, err
	}
	if policy.Purpose != "backup" {
		return nil, errors.New("media: policy is not a backup destination")
	}
	if policy.Driver == storage.DriverS3 {
		var cfg mediaS3StoredConfig
		if err := json.Unmarshal([]byte(policy.Config), &cfg); err != nil {
			return nil, err
		}
		if cfg.PublicBaseURL != "" {
			return nil, errors.New("media: backup S3 policy must remain private")
		}
	}
	return policyDriver(policy, ctx)
}

func validateMediaPolicyPurposeConfig(driver, purpose, rawConfig string) *mediaFailure {
	if purpose != "backup" || driver != storage.DriverS3 {
		return nil
	}
	var cfg mediaS3StoredConfig
	if err := json.Unmarshal([]byte(rawConfig), &cfg); err != nil {
		return &mediaFailure{Status: 400, Code: "invalid_config", Msg: "S3 configuration is invalid"}
	}
	if cfg.PublicBaseURL != "" {
		return &mediaFailure{Status: 400, Code: "invalid_config", Msg: "backup S3 storage must remain private"}
	}
	return nil
}

func readDriverObject(ctx context.Context, drv storage.Driver, path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("media: invalid object size limit")
	}
	r, err := drv.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("media: object exceeds size limit")
	}
	return b, nil
}

func mediaJobContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Minute)
}
