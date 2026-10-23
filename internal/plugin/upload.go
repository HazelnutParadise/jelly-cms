package plugin

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits for plugin archives uploaded through the admin API.
const (
	// MaxUploadEntryBytes caps one extracted file.
	MaxUploadEntryBytes = 20 << 20
	// MaxUploadTotalBytes caps everything extracted from one archive.
	MaxUploadTotalBytes = 100 << 20
)

// ArchiveError marks a problem with the uploaded archive itself. Callers map
// it to a client error; any other error is a server error.
type ArchiveError struct{ msg string }

func (e *ArchiveError) Error() string { return e.msg }

// IsArchiveError reports whether err was caused by the archive contents.
func IsArchiveError(err error) bool {
	var ae *ArchiveError
	return errors.As(err, &ae)
}

func archiveErrorf(format string, args ...any) error {
	return &ArchiveError{msg: fmt.Sprintf(format, args...)}
}

// ValidPluginID reports whether id is safe to use as a directory name.
func ValidPluginID(id string) bool {
	return pluginIDPattern.MatchString(id)
}

// InstallArchive extracts the plugin ZIP at zipPath to pluginsDir/<id> and
// returns the plugin ID from its manifest.
//
// The manifest id is validated before it is used in any path. Entries are
// extracted into a staging directory inside pluginsDir and only then renamed
// into place, so a failed upload never leaves a half-written plugin. Absolute
// names, ".." segments, backslashes and symbolic links are refused, and the
// per-file and total sizes are capped.
func InstallArchive(zipPath, pluginsDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", archiveErrorf("invalid ZIP file: %v", err)
	}
	defer r.Close()

	meta, err := readArchiveManifest(r.File)
	if err != nil {
		return "", err
	}
	if !ValidPluginID(meta.ID) {
		return "", archiveErrorf("invalid plugin.json: id %q is missing or invalid", meta.ID)
	}

	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return "", fmt.Errorf("create plugins directory: %w", err)
	}
	staging, err := os.MkdirTemp(pluginsDir, ".upload-")
	if err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(staging) // no-op once the staging directory has been renamed

	var total int64
	for _, f := range r.File {
		rel, skip, err := archiveEntryPath(f)
		if err != nil {
			return "", err
		}
		if skip {
			continue
		}
		dest := filepath.Join(staging, filepath.FromSlash(rel))
		if err := writeArchiveEntry(f, dest, &total); err != nil {
			return "", err
		}
	}

	target := filepath.Join(pluginsDir, meta.ID)
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("replace plugin directory: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("activate plugin directory: %w", err)
	}
	return meta.ID, nil
}

// readArchiveManifest finds and decodes plugin.json at the root or one folder down.
func readArchiveManifest(files []*zip.File) (PluginMetadata, error) {
	for _, f := range files {
		if f.Name != "plugin.json" && !strings.HasSuffix(f.Name, "/plugin.json") {
			continue
		}
		if f.UncompressedSize64 > maxManifestBytes {
			return PluginMetadata{}, archiveErrorf("plugin.json exceeds %d bytes", maxManifestBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return PluginMetadata{}, archiveErrorf("open plugin.json: %v", err)
		}
		defer rc.Close()
		var meta PluginMetadata
		if err := json.NewDecoder(io.LimitReader(rc, maxManifestBytes)).Decode(&meta); err != nil {
			return PluginMetadata{}, archiveErrorf("invalid plugin.json: %v", err)
		}
		return meta, nil
	}
	return PluginMetadata{}, archiveErrorf("plugin.json not found in ZIP file")
}

// archiveEntryPath maps a ZIP entry to its path inside the plugin directory.
// The first folder of the archive is dropped, as the uploaded archives wrap
// the plugin in one folder. skip is true for directories and hidden entries.
func archiveEntryPath(f *zip.File) (rel string, skip bool, err error) {
	name := f.Name
	if strings.ContainsAny(name, "\\\x00") {
		return "", false, archiveErrorf("entry %q has an invalid name", name)
	}
	if strings.HasPrefix(name, "/") || path.IsAbs(name) {
		return "", false, archiveErrorf("entry %q is an absolute path", name)
	}
	segments := strings.Split(name, "/")
	for _, seg := range segments {
		if seg == ".." {
			return "", false, archiveErrorf("entry %q escapes the plugin directory", name)
		}
		if strings.Contains(seg, ":") {
			return "", false, archiveErrorf("entry %q has a drive or scheme prefix", name)
		}
	}
	if f.Mode()&fs.ModeSymlink != 0 {
		return "", false, archiveErrorf("entry %q is a symbolic link", name)
	}
	if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
		return "", true, nil
	}
	if idx := strings.Index(name, "/"); idx >= 0 {
		segments = strings.Split(name[idx+1:], "/")
	}
	for _, seg := range segments {
		if strings.HasPrefix(seg, ".") {
			return "", true, nil
		}
	}
	rel = strings.Join(segments, "/")
	if rel == "" {
		return "", true, nil
	}
	return rel, false, nil
}

// writeArchiveEntry copies one entry to dest and enforces the size caps.
// total accumulates the bytes written across the whole archive.
func writeArchiveEntry(f *zip.File, dest string, total *int64) error {
	if f.UncompressedSize64 > MaxUploadEntryBytes {
		return archiveErrorf("entry %q exceeds %d bytes", f.Name, MaxUploadEntryBytes)
	}
	if *total+int64(f.UncompressedSize64) > MaxUploadTotalBytes {
		return archiveErrorf("archive exceeds %d bytes extracted", MaxUploadTotalBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return archiveErrorf("open entry %q: %v", f.Name, err)
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", f.Name, err)
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create %q: %w", f.Name, err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, MaxUploadEntryBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("write %q: %w", f.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write %q: %w", f.Name, closeErr)
	}
	if n > MaxUploadEntryBytes {
		return archiveErrorf("entry %q exceeds %d bytes", f.Name, MaxUploadEntryBytes)
	}
	*total += n
	if *total > MaxUploadTotalBytes {
		return archiveErrorf("archive exceeds %d bytes extracted", MaxUploadTotalBytes)
	}
	return nil
}
