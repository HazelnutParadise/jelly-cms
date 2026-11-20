package server

import (
	"context"
	"errors"
)

// Plugin archives in PostgreSQL (ticket 37 R3). An archive is keyed by plugin
// id and the tree digest of its extracted directory (pluginTreeDigest), never
// changes once written, and is stored as 1 MiB chunks so memory use does not
// grow with the archive size. plugins.archive_tree_sha256 points at the
// archive in use.

var errPluginArchiveNotImplemented = errors.New("plugin archive store is not implemented")

// pluginArchiveChunkSize is the size of one stored chunk.
const pluginArchiveChunkSize = 1 << 20

// storePluginArchive streams the zip at zipPath into plugin_archives and
// plugin_archive_chunks under (id, treeSHA256). It is a no-op when that key
// already exists. It returns the SHA-256 of the zip.
func storePluginArchive(ctx context.Context, id, treeSHA256, zipPath string) (zipSHA256 string, err error) {
	return "", errPluginArchiveNotImplemented
}

// openPluginArchive streams the archive (id, treeSHA256) into a new temporary
// file and returns its path. The SHA-256 of the assembled file must match the
// stored one; otherwise the file is removed and an error is returned.
func openPluginArchive(ctx context.Context, id, treeSHA256 string) (zipPath string, err error) {
	return "", errPluginArchiveNotImplemented
}

// prunePluginArchives deletes archives of id that no plugins row references.
func prunePluginArchives(ctx context.Context, id string) error {
	return errPluginArchiveNotImplemented
}
