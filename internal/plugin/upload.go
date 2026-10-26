package plugin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxUploadEntryBytes = 20 << 20
	MaxUploadTotalBytes = 100 << 20
)

type ArchiveError struct{ msg string }

func (e *ArchiveError) Error() string { return e.msg }
func IsArchiveError(err error) bool   { var ae *ArchiveError; return errors.As(err, &ae) }
func archiveErrorf(format string, args ...any) error {
	return &ArchiveError{msg: fmt.Sprintf(format, args...)}
}
func ValidPluginID(id string) bool { return pluginIDPattern.MatchString(id) }

type ArchiveExpectation struct {
	ID              string
	Version         string
	PermissionsHash string
}

// StagedArchive is a fully extracted and validated plugin that has not yet
// replaced its installed version. The caller owns its cleanup.
type StagedArchive struct {
	ID      string
	Version string
	Stage   string
	Target  string
}

func (s *StagedArchive) Cleanup() {
	if s != nil {
		_ = os.RemoveAll(s.Stage)
	}
}

func InstallArchive(zipPath, pluginsDir string) (string, error) {
	return InstallArchiveExpecting(zipPath, pluginsDir, ArchiveExpectation{})
}

func InspectArchive(zipPath string) (PluginMetadata, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return PluginMetadata{}, archiveErrorf("invalid ZIP file: %v", err)
	}
	defer r.Close()
	layout, err := inspectArchiveLayout(r.File)
	return layout.meta, err
}

// ValidateArchive reads every regular entry without writing a plugin directory.
// It is used for permission previews so corrupted data and size violations are
// rejected before approval is requested.
func ValidateArchive(zipPath string) (PluginMetadata, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return PluginMetadata{}, archiveErrorf("invalid ZIP file: %v", err)
	}
	defer r.Close()
	layout, err := inspectArchiveLayout(r.File)
	if err != nil {
		return PluginMetadata{}, err
	}
	var total int64
	for _, entry := range layout.entries {
		if entry.directory {
			continue
		}
		if err := validateArchiveContent(entry.file, &total); err != nil {
			return PluginMetadata{}, err
		}
	}
	return layout.meta, nil
}

// StageArchive validates the complete archive and writes it to a hidden staging
// directory. It never replaces a live plugin directory.
func StageArchive(zipPath, pluginsDir string, want ArchiveExpectation) (*StagedArchive, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, archiveErrorf("invalid ZIP file: %v", err)
	}
	defer r.Close()
	layout, err := inspectArchiveLayout(r.File)
	if err != nil {
		return nil, err
	}
	meta := layout.meta
	if !ValidPluginID(meta.ID) {
		return nil, archiveErrorf("invalid plugin.json: id %q is missing or invalid", meta.ID)
	}
	if want.ID != "" && meta.ID != want.ID {
		return nil, archiveErrorf("archive id %q does not match %q", meta.ID, want.ID)
	}
	if want.Version != "" && meta.Version != want.Version {
		return nil, archiveErrorf("archive version %q does not match %q", meta.Version, want.Version)
	}
	if want.PermissionsHash != "" && PermissionNamesHash(ManifestPermissionNames(meta)) != want.PermissionsHash {
		return nil, archiveErrorf("archive permissions differ from the approved permissions")
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create plugins directory: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(pluginsDir), ".jelly-plugin-stage-")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()
	var total int64
	for _, entry := range layout.entries {
		if entry.directory {
			continue
		}
		if entry.skip {
			if err := validateArchiveContent(entry.file, &total); err != nil {
				return nil, err
			}
			continue
		}
		if err := writeArchiveEntry(entry.file, filepath.Join(stage, filepath.FromSlash(entry.rel)), &total); err != nil {
			return nil, err
		}
	}
	stagedMeta, err := ReadManifest(stage)
	if err != nil || stagedMeta.ID != meta.ID || stagedMeta.Version != meta.Version {
		return nil, archiveErrorf("staged plugin manifest is invalid or changed")
	}
	ok = true
	return &StagedArchive{ID: meta.ID, Version: meta.Version, Stage: stage, Target: filepath.Join(pluginsDir, meta.ID)}, nil
}

func InstallArchiveExpecting(zipPath, pluginsDir string, want ArchiveExpectation) (string, error) {
	staged, err := StageArchive(zipPath, pluginsDir, want)
	if err != nil {
		return "", err
	}
	defer staged.Cleanup()
	backup, err := os.MkdirTemp(filepath.Dir(pluginsDir), ".jelly-plugin-backup-")
	if err != nil {
		return "", fmt.Errorf("prepare plugin backup: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return "", fmt.Errorf("prepare plugin backup: %w", err)
	}
	hasBackup := false
	if _, err := os.Stat(staged.Target); err == nil {
		if err := os.Rename(staged.Target, backup); err != nil {
			return "", fmt.Errorf("backup plugin directory: %w", err)
		}
		hasBackup = true
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(staged.Stage, staged.Target); err != nil {
		if hasBackup {
			_ = os.Rename(backup, staged.Target)
		}
		return "", fmt.Errorf("activate plugin directory: %w", err)
	}
	if hasBackup {
		if err := os.RemoveAll(backup); err != nil {
			_ = os.RemoveAll(staged.Target)
			_ = os.Rename(backup, staged.Target)
			return "", fmt.Errorf("remove prior plugin backup: %w", err)
		}
	}
	return staged.ID, nil
}

type archiveLayoutEntry struct {
	file      *zip.File
	rel       string
	skip      bool
	directory bool
}

func validateArchiveContent(f *zip.File, total *int64) error {
	if f.UncompressedSize64 > MaxUploadEntryBytes || *total+int64(f.UncompressedSize64) > MaxUploadTotalBytes {
		return archiveErrorf("plugin archive exceeds extraction limits")
	}
	rc, err := f.Open()
	if err != nil {
		return archiveErrorf("open entry %q: %v", f.Name, err)
	}
	n, readErr := io.Copy(io.Discard, io.LimitReader(rc, MaxUploadEntryBytes+1))
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		return archiveErrorf("read entry %q: %v", f.Name, errors.Join(readErr, closeErr))
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

type archiveLayout struct {
	meta    PluginMetadata
	entries []archiveLayoutEntry
}

func inspectArchiveLayout(files []*zip.File) (archiveLayout, error) {
	type candidate struct {
		file *zip.File
		name string
	}
	manifests := []candidate{}
	for _, f := range files {
		name, err := validateArchiveName(f)
		if err != nil {
			return archiveLayout{}, err
		}
		if name == "plugin.json" || strings.HasSuffix(name, "/plugin.json") {
			manifests = append(manifests, candidate{f, name})
		}
	}
	if len(manifests) != 1 {
		return archiveLayout{}, archiveErrorf("ZIP must contain exactly one unambiguous plugin.json")
	}
	manifest := manifests[0]
	prefix := ""
	if manifest.name != "plugin.json" {
		parts := strings.Split(manifest.name, "/")
		if len(parts) != 2 || parts[0] == "" {
			return archiveLayout{}, archiveErrorf("plugin.json must be at the archive root or one wrapper folder down")
		}
		prefix = parts[0] + "/"
		for _, f := range files {
			n, err := validateArchiveName(f)
			if err != nil {
				return archiveLayout{}, err
			}
			if n != strings.TrimSuffix(n, "/") && !strings.HasPrefix(n, prefix) {
				return archiveLayout{}, archiveErrorf("mixed root and wrapped archive entries")
			}
			if !strings.HasPrefix(n, prefix) && n != "" {
				return archiveLayout{}, archiveErrorf("mixed root and wrapped archive entries")
			}
		}
	}
	rc, err := manifest.file.Open()
	if err != nil {
		return archiveLayout{}, archiveErrorf("open plugin.json: %v", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(rc, maxManifestBytes+1))
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		return archiveLayout{}, archiveErrorf("read plugin.json: %v", errors.Join(readErr, closeErr))
	}
	if len(raw) > maxManifestBytes {
		return archiveLayout{}, archiveErrorf("plugin.json exceeds %d bytes", maxManifestBytes)
	}
	meta, err := ParseManifest(raw)
	if err != nil {
		return archiveLayout{}, archiveErrorf("invalid plugin.json: %v", err)
	}
	layout := archiveLayout{meta: meta}
	destTypes := map[string]bool{}
	for _, f := range files {
		name, err := validateArchiveName(f)
		if err != nil {
			return archiveLayout{}, err
		}
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				return archiveLayout{}, archiveErrorf("mixed root and wrapped archive entries")
			}
			name = strings.TrimPrefix(name, prefix)
		} else if strings.HasPrefix(name, "plugin.json/") {
			return archiveLayout{}, archiveErrorf("file and directory path conflict")
		}
		if name == "" {
			continue
		}
		dir := f.FileInfo().IsDir() || strings.HasSuffix(name, "/")
		name = strings.TrimSuffix(name, "/")
		clean := path.Clean(name)
		if clean == "." || clean == "" || clean != name {
			return archiveLayout{}, archiveErrorf("entry %q has a non-normalized path", f.Name)
		}
		if _, exists := destTypes[clean]; exists {
			return archiveLayout{}, archiveErrorf("duplicate or normalized-colliding archive path %q", clean)
		}
		destTypes[clean] = dir
		skip := false
		for _, seg := range strings.Split(clean, "/") {
			if strings.HasPrefix(seg, ".") {
				skip = true
				break
			}
		}
		layout.entries = append(layout.entries, archiveLayoutEntry{file: f, rel: clean, skip: skip, directory: dir})
	}
	if err := validatePathConflicts(layout.entries); err != nil {
		return archiveLayout{}, err
	}
	return layout, nil
}

func validatePathConflicts(entries []archiveLayoutEntry) error {
	kinds := map[string]bool{}
	for _, e := range entries {
		kinds[e.rel] = e.file.FileInfo().IsDir() || strings.HasSuffix(e.file.Name, "/")
	}
	for name, isDir := range kinds {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if v, ok := kinds[parent]; ok && !v {
				return archiveErrorf("file and directory path conflict at %q", parent)
			}
		}
		if !isDir {
			for child := range kinds {
				if strings.HasPrefix(child, name+"/") {
					return archiveErrorf("file and directory path conflict at %q", name)
				}
			}
		}
	}
	return nil
}

func validateArchiveName(f *zip.File) (string, error) {
	name := f.Name
	if strings.ContainsAny(name, "\\\x00") {
		return "", archiveErrorf("entry %q has an invalid name", name)
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", archiveErrorf("entry %q is an absolute path", name)
	}
	if f.Mode()&fs.ModeSymlink != 0 {
		return "", archiveErrorf("entry %q is a symbolic link", name)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if seg == ".." || strings.Contains(seg, ":") {
			return "", archiveErrorf("entry %q has an unsafe path", name)
		}
	}
	return name, nil
}

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
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %q: %w", f.Name, err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, MaxUploadEntryBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("write %q: %w", f.Name, errors.Join(copyErr, closeErr))
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
