package server

// Startup steps for attachments (docs/tickets/08-system.md, 08d). A default
// local policy is ensured, then files that already sit in data/uploads are
// recorded under it with their public address unchanged. Both steps are
// idempotent and run each time RegisterRoutes runs.

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"gorm.io/gorm"
)

// ensureDefaultMediaPolicy creates the local media policy when no default is
// set. ON CONFLICT covers the partial unique index that allows one default per
// purpose, so two starting nodes cannot both insert one.
func ensureDefaultMediaPolicy(ctx context.Context) error {
	if data.DB == nil {
		return nil
	}
	if _, err := defaultMediaPolicy(ctx); err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return mediaDB(ctx).Exec(
		`INSERT INTO media_policies (name, driver, purpose, config, is_default)
		 VALUES (?, ?, ?, '{}'::jsonb, TRUE) ON CONFLICT DO NOTHING`,
		mediaDefaultPolicyName, storage.DriverLocal, mediaPurposeMedia,
	).Error
}

// importLegacyUploads records files under uploadsRoot that have no row in the
// default policy. Each file gets source legacy and keeps its /uploads path.
// Dot-prefixed names are skipped: they are temporary files of a write that did
// not finish. It returns how many rows it added.
func importLegacyUploads(ctx context.Context) (int, error) {
	if data.DB == nil {
		return 0, nil
	}
	if _, err := os.Stat(uploadsRoot); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	policy, err := defaultMediaPolicy(ctx)
	if err != nil {
		return 0, err
	}
	imported := 0
	err = filepath.WalkDir(uploadsRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// An unreadable entry is skipped so one bad directory does not stop the import.
			return nil
		}
		if d.IsDir() {
			if p != uploadsRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(uploadsRoot, p)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if _, err := storage.CleanPath(key); err != nil {
			return nil
		}
		added, err := importLegacyFile(ctx, policy, key, p)
		if err != nil {
			return err
		}
		if added {
			imported++
		}
		return nil
	})
	return imported, err
}

// importLegacyFile adds one row when the policy does not have the path yet.
func importLegacyFile(ctx context.Context, policy mediaPolicyRow, key, fullPath string) (bool, error) {
	var existing int64
	if err := mediaDB(ctx).Table("media_files").Where("policy_id = ? AND path = ?", policy.ID, key).Count(&existing).Error; err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		return false, nil
	}
	name := path.Base(key)
	kind := mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	if kind == "" {
		kind = http.DetectContentType(raw)
	}
	kind = strings.ToLower(strings.TrimSpace(strings.Split(kind, ";")[0]))
	row := mediaFileRow{
		PolicyID: policy.ID,
		Path:     key,
		Filename: name,
		Mime:     kind,
		Size:     int64(len(raw)),
		Sha256:   mediaSHA256(raw),
		Source:   "legacy",
	}
	if strings.HasPrefix(kind, "image/") {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			w, h := cfg.Width, cfg.Height
			row.Width, row.Height = &w, &h
		}
	}
	if err := mediaDB(ctx).Table("media_files").Create(&row).Error; err != nil {
		return false, err
	}
	return true, nil
}
