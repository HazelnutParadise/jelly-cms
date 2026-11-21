package server

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"gorm.io/gorm"
)

// Plugin archives in PostgreSQL (ticket 37 R3). An archive is keyed by plugin
// id and the tree digest of its extracted directory (pluginTreeDigest), never
// changes once written, and is stored as 1 MiB chunks so memory use does not
// grow with the archive size. plugins.archive_tree_sha256 points at the
// archive in use.

// pluginArchiveChunkSize is the size of one stored chunk.
const pluginArchiveChunkSize = 1 << 20

// storePluginArchive streams the zip at zipPath into plugin_archives and
// plugin_archive_chunks under (id, treeSHA256). It is a no-op when that key
// already exists. It returns the SHA-256 of the zip.
func storePluginArchive(ctx context.Context, id, treeSHA256, zipPath string) (zipSHA256 string, err error) {
	if !plugin.ValidPluginID(id) || !storeSHA256Pattern.MatchString(treeSHA256) {
		return "", fmt.Errorf("invalid plugin archive key")
	}
	f, err := os.Open(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	zipSHA256 = hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	err = data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`INSERT INTO plugin_archives (plugin_id, tree_sha256, zip_sha256, zip_size)
			VALUES (?, ?, ?, ?) ON CONFLICT (plugin_id, tree_sha256) DO NOTHING`,
			id, treeSHA256, zipSHA256, info.Size())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// Same key already stored: the archive is immutable, so keep it.
			return nil
		}
		buf := make([]byte, pluginArchiveChunkSize)
		for seq := 0; ; seq++ {
			n, readErr := io.ReadFull(f, buf)
			if n > 0 {
				if err := tx.Exec(`INSERT INTO plugin_archive_chunks (plugin_id, tree_sha256, seq, data)
					VALUES (?, ?, ?, ?)`, id, treeSHA256, seq, buf[:n]).Error; err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	})
	if err != nil {
		return "", err
	}

	return zipSHA256, nil
}

// openPluginArchive streams the archive (id, treeSHA256) into a new temporary
// file and returns its path. The SHA-256 of the assembled file must match the
// stored one; otherwise the file is removed and an error is returned.
func openPluginArchive(ctx context.Context, id, treeSHA256 string) (zipPath string, err error) {
	if !plugin.ValidPluginID(id) || !storeSHA256Pattern.MatchString(treeSHA256) {
		return "", fmt.Errorf("invalid plugin archive key")
	}
	var meta []struct {
		ZipSHA256 string `gorm:"column:zip_sha256"`
		ZipSize   int64  `gorm:"column:zip_size"`
	}
	if err := data.DB.WithContext(ctx).Raw(`SELECT zip_sha256, zip_size FROM plugin_archives WHERE plugin_id = ? AND tree_sha256 = ?`,
		id, treeSHA256).Scan(&meta).Error; err != nil {
		return "", err
	}
	if len(meta) != 1 {
		return "", fmt.Errorf("plugin %s archive %s is not stored", id, treeSHA256)
	}

	tmp, err := os.CreateTemp("", "jelly-plugin-archive-*.zip")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(cause error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(name)
		return "", cause
	}

	h := sha256.New()
	w := io.MultiWriter(tmp, h)
	rows, err := data.DB.WithContext(ctx).Raw(`SELECT seq, data FROM plugin_archive_chunks WHERE plugin_id = ? AND tree_sha256 = ? ORDER BY seq`,
		id, treeSHA256).Rows()
	if err != nil {
		return fail(err)
	}
	var (
		expect int
		total  int64
	)
	for rows.Next() {
		var (
			seq   int
			chunk []byte
		)
		if err := rows.Scan(&seq, &chunk); err != nil {
			_ = rows.Close()
			return fail(err)
		}
		if seq != expect {
			_ = rows.Close()
			return fail(fmt.Errorf("plugin %s archive %s chunk %d is missing", id, treeSHA256, expect))
		}
		expect++
		total += int64(len(chunk))
		if _, err := w.Write(chunk); err != nil {
			_ = rows.Close()
			return fail(err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fail(err)
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	if total != meta[0].ZipSize {
		return fail(fmt.Errorf("plugin %s archive %s has %d bytes, want %d", id, treeSHA256, total, meta[0].ZipSize))
	}
	if hex.EncodeToString(h.Sum(nil)) != meta[0].ZipSHA256 {
		return fail(fmt.Errorf("plugin %s archive %s failed its checksum", id, treeSHA256))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}

	return name, nil
}

// prunePluginArchives deletes archives of id that no plugins row references.
func prunePluginArchives(ctx context.Context, id string) error {
	return data.DB.WithContext(ctx).Exec(`DELETE FROM plugin_archives a
		WHERE a.plugin_id = ? AND NOT EXISTS (
			SELECT 1 FROM plugins p WHERE p.id = a.plugin_id AND p.archive_tree_sha256 = a.tree_sha256)`, id).Error
}

// packPluginDir writes the plugin directory dir into a zip at zipPath. Directory
// entries are included so empty directories survive the round trip.
func packPluginDir(dir, zipPath string) error {
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	walkErr := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Method: zip.Store})
			return err
		case info.Mode().IsRegular():
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name = name
			hdr.Method = zip.Deflate
			dst, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(dst, src)
			closeErr := src.Close()
			return errors.Join(copyErr, closeErr)
		default:
			return fmt.Errorf("plugin tree contains unsupported entry %s", name)
		}
	})
	closeErr := zw.Close()
	fileErr := out.Close()

	return errors.Join(walkErr, closeErr, fileErr)
}

// stagePluginArchive is plugin.StageArchive plus the empty directories the zip
// names. StageArchive writes files only, so without this step an archive packed
// from a directory with empty subdirectories would not reproduce its digest.
func stagePluginArchive(zipPath, pluginsDir string, want plugin.ArchiveExpectation) (*plugin.StagedArchive, error) {
	staged, err := plugin.StageArchive(zipPath, pluginsDir, want)
	if err != nil {
		return nil, err
	}
	if err := createArchiveDirectories(zipPath, staged.Stage); err != nil {
		staged.Cleanup()
		return nil, err
	}
	return staged, nil
}

// createArchiveDirectories creates the directory entries of zipPath under stage.
// It follows the wrapper-folder rule of plugin.StageArchive and skips the same
// hidden paths, so the result matches the staged file tree.
func createArchiveDirectories(zipPath, stage string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	prefix := ""
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/plugin.json") {
			prefix = strings.TrimSuffix(f.Name, "plugin.json")
			break
		}
	}
	for _, f := range r.File {
		if !f.FileInfo().IsDir() && !strings.HasSuffix(f.Name, "/") {
			continue
		}
		name := f.Name
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		rel := strings.TrimSuffix(name, "/")
		if rel == "" {
			continue
		}
		if path.Clean(rel) != rel || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("archive directory %q has an unsafe path", f.Name)
		}
		hidden := false
		for _, seg := range strings.Split(rel, "/") {
			if strings.HasPrefix(seg, ".") {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(rel)), 0o755); err != nil {
			return err
		}
	}
	return nil
}
