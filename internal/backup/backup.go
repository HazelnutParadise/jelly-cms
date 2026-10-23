// Package backup creates, verifies and restores backups, and exports content
// as Markdown zip, WXR or JSON. Archives are AES-GCM encrypted and stored
// under data/backups (mode 0700), never under /uploads.
//
// Each table is written as JSONL with a manifest that lists row counts and
// checksums. The backup does not depend on pg_dump.
package backup

import (
	"context"
	"errors"
	"time"
)

// Contents a backup may include.
const (
	ContentDatabase    = "db"
	ContentAttachments = "attachments"
	ContentSettings    = "settings"
)

// Export formats.
const (
	FormatMarkdownZip = "markdown_zip"
	FormatWXR         = "wxr"
	FormatJSON        = "json"
)

// Directory and exclusions.
const (
	Directory = "data/backups"
	// ConfigFile is never included. auth-secrets.json is included only when the
	// operator supplies the passphrase.
	ConfigFile = "config.json"
)

// Limits enforced on restore.
const (
	MaxArchiveBytes     = 4 << 30
	MaxExpansionRatio   = 100
	RestoreConfirmation = "RESTORE"
)

// ErrNotImplemented marks behaviour that the skeleton does not provide yet.
var ErrNotImplemented = errors.New("backup: not implemented")

// ErrConfirmRequired is returned when a restore is not confirmed by typing RESTORE.
var ErrConfirmRequired = errors.New("backup: restore must be confirmed with RESTORE")

// Manifest is stored beside the table files.
type Manifest struct {
	Version    int                  `json:"version"`
	CreatedAt  time.Time            `json:"created_at"`
	AppVersion string               `json:"app_version"`
	Contents   []string             `json:"contents"`
	Tables     map[string]TableInfo `json:"tables"`
}

// TableInfo describes one table file inside the archive.
type TableInfo struct {
	File   string `json:"file"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Request asks for a backup.
type Request struct {
	Contents   []string
	Note       string
	Passphrase string // when set, auth-secrets.json is included and the archive is encrypted with it
}

// Run creates a backup and returns its ID.
// TODO(08c): implement with the backup.run job.
func Run(ctx context.Context, req Request) (string, error) {
	return "", ErrNotImplemented
}

// Restore restores backup id after the operator types RESTORE. A pre_restore
// backup is taken first and maintenance mode is enabled.
// TODO(08c): implement.
func Restore(ctx context.Context, id, confirm string) error {
	if confirm != RestoreConfirmation {
		return ErrConfirmRequired
	}
	return ErrNotImplemented
}

// Export writes content in format and returns the export ID.
// TODO(08c): implement; Markdown output uses markup.Flatten from ticket 09.
func Export(ctx context.Context, format string) (string, error) {
	return "", ErrNotImplemented
}

// Prune applies the retention policy (daily, weekly and local counts).
func Prune(ctx context.Context) error {
	return ErrNotImplemented
}
