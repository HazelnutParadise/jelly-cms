// Package backup creates, verifies and restores backups, and exports content
// as Markdown zip, WXR or JSON. Archives are AES-GCM encrypted and stored
// under data/backups (mode 0700), never under /uploads.
//
// Each table is written as JSONL with a manifest that lists row counts and
// checksums. The backup does not depend on pg_dump.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/gorm"
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

	// ExportDirectory holds finished exports. Like backups, it is not under /uploads.
	ExportDirectory = "data/exports"

	authSecretsFile = "data/auth-secrets.json"
	localKeyFile    = "data/backup-key"
	attachmentsDir  = "data/uploads"
)

// Limits enforced on restore.
const (
	MaxArchiveBytes     = 4 << 30
	MaxExpansionRatio   = 100
	RestoreConfirmation = "RESTORE"
)

// Backup kinds. Each row records the kind it was created with.
const (
	KindManual     = "manual"
	KindScheduled  = "scheduled"
	KindPreRestore = "pre_restore"
)

// NoteLimit caps a backup note in characters.
const NoteLimit = 280

const (
	stateRunning = "running"
	stateDone    = "done"
	stateFailed  = "failed"

	manifestVersion     = 1
	passphraseMinLength = 8
	exportRetention     = 7 * 24 * time.Hour
	defaultKeepDaily    = 14
	listLimit           = 100
	lastErrorLimit      = 500
)

// Errors returned by this package. The admin API maps them to status codes.
var (
	ErrConfirmRequired    = errors.New("backup: restore must be confirmed with RESTORE")
	ErrNotInstalled       = errors.New("backup: database is not ready")
	ErrNotFound           = errors.New("backup: not found")
	ErrBusy               = errors.New("backup: another backup or restore is running")
	ErrNotDone            = errors.New("backup: archive is not ready")
	ErrNotFailed          = errors.New("backup: only failed backups can be retried")
	ErrChecksum           = errors.New("backup: archive checksum does not match")
	ErrInvalidArchive     = errors.New("backup: archive cannot be opened")
	ErrUnsafeEntry        = errors.New("backup: archive contains an entry that is not allowed")
	ErrPassphraseRequired = errors.New("backup: this archive needs its passphrase")
	ErrPassphraseTooShort = errors.New("backup: passphrase must be at least 8 characters")
	ErrInvalidContent     = errors.New("backup: unknown backup content")
	ErrInvalidKind        = errors.New("backup: unknown backup kind")
	ErrInvalidNote        = errors.New("backup: note is too long")
	ErrSchemaTooNew       = errors.New("backup: archive schema version does not match this database")
	ErrUnknownFormat      = errors.New("backup: unknown export format")
)

// Manifest is stored inside the archive as manifest.json and copied to the
// backups row. It lists every table file and extra file with checksums.
type Manifest struct {
	Version       int                  `json:"version"`
	CreatedAt     time.Time            `json:"created_at"`
	AppVersion    string               `json:"app_version"`
	SchemaVersion int                  `json:"schema_version"`
	Contents      []string             `json:"contents"`
	Tables        map[string]TableInfo `json:"tables"`
	Files         []FileInfo           `json:"files"`
}

// TableInfo describes one table file inside the archive.
type TableInfo struct {
	File   string `json:"file"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// FileInfo describes one extra file (attachment or auth-secrets.json).
type FileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Request asks for a backup.
type Request struct {
	Contents   []string
	Note       string
	Passphrase string // when set, auth-secrets.json is included and the archive is encrypted with it
	Kind       string // KindManual when empty
	Actor      uint   // user ID recorded on the row; 0 for the scheduler
}

// Summary is one backups row as the admin API shows it. The file path stays server side.
type Summary struct {
	ID         int64            `json:"id"`
	State      string           `json:"state"`
	Kind       string           `json:"kind"`
	Contents   []string         `json:"contents"`
	Note       string           `json:"note"`
	SizeBytes  int64            `json:"size_bytes"`
	Rows       map[string]int64 `json:"rows,omitempty"`
	LastError  string           `json:"last_error,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
}

// record maps the backups table (migration 0020).
type record struct {
	ID         int64           `gorm:"column:id;primaryKey"`
	State      string          `gorm:"column:state"`
	Kind       string          `gorm:"column:kind"`
	Contents   json.RawMessage `gorm:"column:contents;type:jsonb"`
	Encrypted  bool            `gorm:"column:encrypted"`
	Note       string          `gorm:"column:note"`
	FilePath   *string         `gorm:"column:file_path"`
	SizeBytes  int64           `gorm:"column:size_bytes"`
	SHA256     *string         `gorm:"column:sha256"`
	Manifest   json.RawMessage `gorm:"column:manifest;type:jsonb"`
	LastError  *string         `gorm:"column:last_error"`
	CreatedBy  *uint           `gorm:"column:created_by"`
	CreatedAt  time.Time       `gorm:"column:created_at"`
	FinishedAt *time.Time      `gorm:"column:finished_at"`
}

func (record) TableName() string { return "backups" }

// runMu allows one backup or restore at a time in this process.
var runMu sync.Mutex

// Run creates a backup and returns its ID. The archive is written before the
// call returns and the row ends as done or failed.
func Run(ctx context.Context, req Request) (int64, error) {
	if !runMu.TryLock() {
		return 0, ErrBusy
	}
	defer runMu.Unlock()
	return runLocked(ctx, req)
}

// runLocked is Run for a caller that already holds runMu (restore takes a
// pre_restore backup this way).
func runLocked(ctx context.Context, req Request) (int64, error) {
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if err := checkRequest(req); err != nil {
		return 0, err
	}
	contents, err := normalizeContents(req.Contents)
	if err != nil {
		return 0, err
	}
	kind := req.Kind
	if kind == "" {
		kind = KindManual
	}
	if !slices.Contains([]string{KindManual, KindScheduled, KindPreRestore}, kind) {
		return 0, ErrInvalidKind
	}
	raw, err := json.Marshal(contents)
	if err != nil {
		return 0, err
	}
	row := record{
		State:     stateRunning,
		Kind:      kind,
		Contents:  raw,
		Encrypted: true,
		Note:      req.Note,
		CreatedAt: time.Now().UTC(),
	}
	if req.Actor != 0 {
		actor := req.Actor
		row.CreatedBy = &actor
	}
	if err := data.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, fmt.Errorf("backup: create row: %w", err)
	}
	req.Contents = contents
	req.Kind = kind
	return row.ID, execute(ctx, row.ID, req)
}

// Retry runs a failed backup again under the same ID with its original
// contents and note. A passphrase is needed again when the original used one,
// because passphrases are never stored.
func Retry(ctx context.Context, id int64, passphrase string, actor uint) (int64, error) {
	if !runMu.TryLock() {
		return 0, ErrBusy
	}
	defer runMu.Unlock()
	if data.DB == nil {
		return 0, ErrNotInstalled
	}
	if err := checkRequest(Request{Note: "", Passphrase: passphrase}); err != nil {
		return 0, err
	}
	row, err := loadRecord(ctx, id)
	if err != nil {
		return 0, err
	}
	if row.State != stateFailed {
		return 0, ErrNotFailed
	}
	var contents []string
	if err := json.Unmarshal(row.Contents, &contents); err != nil {
		return 0, fmt.Errorf("backup: stored contents are damaged: %w", err)
	}
	if err := data.DB.WithContext(ctx).Model(&record{}).Where("id = ?", id).Updates(map[string]any{
		"state":       stateRunning,
		"last_error":  nil,
		"finished_at": nil,
	}).Error; err != nil {
		return 0, fmt.Errorf("backup: mark running: %w", err)
	}
	req := Request{Contents: contents, Note: row.Note, Passphrase: passphrase, Kind: row.Kind, Actor: actor}
	return id, execute(ctx, id, req)
}

// execute writes the archive for row id and records the outcome.
func execute(ctx context.Context, id int64, req Request) error {
	art, err := writeArchive(ctx, req)
	if err != nil {
		markFailed(ctx, id, err)
		return err
	}
	manifest, err := json.Marshal(art.Manifest)
	if err != nil {
		markFailed(ctx, id, err)
		return err
	}
	if err := data.DB.WithContext(ctx).Model(&record{}).Where("id = ?", id).Updates(map[string]any{
		"state":       stateDone,
		"file_path":   art.Path,
		"size_bytes":  art.Size,
		"sha256":      art.SHA256,
		"manifest":    string(manifest),
		"last_error":  nil,
		"finished_at": time.Now().UTC(),
	}).Error; err != nil {
		return fmt.Errorf("backup: record result: %w", err)
	}
	return nil
}

// markFailed records cause on the row. It uses a context detached from the
// request so a cancelled request still leaves the row in a final state.
func markFailed(ctx context.Context, id int64, cause error) {
	msg := cause.Error()
	if len(msg) > lastErrorLimit {
		msg = msg[:lastErrorLimit]
	}
	err := data.DB.WithContext(context.WithoutCancel(ctx)).Model(&record{}).Where("id = ?", id).Updates(map[string]any{
		"state":       stateFailed,
		"last_error":  msg,
		"finished_at": time.Now().UTC(),
	}).Error
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: mark backup %d failed: %v\n", id, err)
	}
}

func checkRequest(req Request) error {
	if utf8.RuneCountInString(req.Note) > NoteLimit {
		return ErrInvalidNote
	}
	if req.Passphrase != "" && len(req.Passphrase) < passphraseMinLength {
		return ErrPassphraseTooShort
	}
	return nil
}

// normalizeContents validates the requested contents and returns them in
// canonical order without duplicates. An empty request means the database.
func normalizeContents(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{ContentDatabase}, nil
	}
	for _, c := range in {
		if !slices.Contains([]string{ContentDatabase, ContentAttachments, ContentSettings}, c) {
			return nil, ErrInvalidContent
		}
	}
	out := make([]string, 0, 3)
	for _, c := range []string{ContentDatabase, ContentAttachments, ContentSettings} {
		if slices.Contains(in, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// List returns the newest backups first.
func List(ctx context.Context) ([]Summary, error) {
	if data.DB == nil {
		return nil, ErrNotInstalled
	}
	var rows []record
	if err := data.DB.WithContext(ctx).Order("created_at DESC, id DESC").Limit(listLimit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		s := Summary{
			ID:         r.ID,
			State:      r.State,
			Kind:       r.Kind,
			Note:       r.Note,
			SizeBytes:  r.SizeBytes,
			CreatedAt:  r.CreatedAt,
			FinishedAt: r.FinishedAt,
		}
		_ = json.Unmarshal(r.Contents, &s.Contents)
		if r.LastError != nil {
			s.LastError = *r.LastError
		}
		if len(r.Manifest) > 0 {
			var m Manifest
			if json.Unmarshal(r.Manifest, &m) == nil {
				s.Rows = make(map[string]int64, len(m.Tables))
				for name, info := range m.Tables {
					s.Rows[name] = info.Rows
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Delete removes a backup's archive file and its row. A running backup cannot be deleted.
func Delete(ctx context.Context, id int64) error {
	if !runMu.TryLock() {
		return ErrBusy
	}
	defer runMu.Unlock()
	row, err := loadRecord(ctx, id)
	if err != nil {
		return err
	}
	if row.State == stateRunning {
		return ErrBusy
	}
	if err := removeArchive(row); err != nil {
		return err
	}
	return data.DB.WithContext(ctx).Delete(&record{}, id).Error
}

// ArchivePath returns the archive file of a finished backup and the name to
// download it under.
func ArchivePath(ctx context.Context, id int64) (string, string, error) {
	row, err := loadRecord(ctx, id)
	if err != nil {
		return "", "", err
	}
	if row.State != stateDone || row.FilePath == nil {
		return "", "", ErrNotDone
	}
	path, err := archiveFile(*row.FilePath)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("backup: archive file: %w", err)
	}
	return path, fmt.Sprintf("jelly-backup-%d.jbk", id), nil
}

func loadRecord(ctx context.Context, id int64) (record, error) {
	if data.DB == nil {
		return record{}, ErrNotInstalled
	}
	var row record
	err := data.DB.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return record{}, ErrNotFound
	}
	if err != nil {
		return record{}, fmt.Errorf("backup: load row: %w", err)
	}
	return row, nil
}

// archiveFile checks that rel names a file inside Directory and returns it.
// The path comes from the database, so it is checked before any file access.
func archiveFile(rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	inside, err := filepath.Rel(filepath.FromSlash(Directory), clean)
	if err != nil || inside == "." || !filepath.IsLocal(inside) {
		return "", ErrInvalidArchive
	}
	return clean, nil
}

func removeArchive(row record) error {
	if row.FilePath == nil {
		return nil
	}
	path, err := archiveFile(*row.FilePath)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup: remove archive: %w", err)
	}
	return nil
}
