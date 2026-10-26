package backup

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/update"
	"gorm.io/gorm"
)

// Names inside the archive payload.
const (
	manifestEntry     = "manifest.json"
	authSecretsEntry  = "auth-secrets.json"
	attachmentsPrefix = "attachments/"
	dbPrefix          = "db/"
	dbSuffix          = ".jsonl"
)

const (
	// contentDir is where a restore unpacks the payload before it touches data.
	contentDir = "content"

	maxManifestBytes = 64 << 20
	maxJSONLine      = 256 << 20
	insertBatchRows  = 500
	insertBatchBytes = 4 << 20

	// maintenanceOptionPrefix marks options that describe the running site.
	// A restore keeps the current values, so maintenance mode survives it.
	maintenanceOptionPrefix = "maintenance_"
)

var tableNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// RestoreRequest asks for a restore of backup ID.
type RestoreRequest struct {
	ID         int64
	Confirm    string
	Passphrase string
	Actor      uint
	// EnterMaintenance runs after the pre_restore backup has been written and
	// before any data changes. An error stops the restore with nothing changed.
	EnterMaintenance func() error
}

// RestoreResult reports what a restore did.
type RestoreResult struct {
	PreRestoreID int64
}

// Restore restores backup id after the operator types RESTORE. The archive is
// verified first (checksum, decryption, manifest, every file's checksum). Then
// a pre_restore backup is written, maintenance mode is enabled and the data is
// replaced in one transaction. Maintenance mode stays on afterwards; an
// operator turns it off once the site has been checked.
func Restore(ctx context.Context, req RestoreRequest) (RestoreResult, error) {
	if req.Confirm != RestoreConfirmation {
		return RestoreResult{}, ErrConfirmRequired
	}
	if data.DB == nil {
		return RestoreResult{}, ErrNotInstalled
	}
	if !runMu.TryLock() {
		return RestoreResult{}, ErrBusy
	}
	defer runMu.Unlock()

	row, err := loadRecord(ctx, req.ID)
	if err != nil {
		return RestoreResult{}, err
	}
	if row.State != stateDone || row.SHA256 == nil {
		return RestoreResult{}, ErrNotDone
	}
	archive, release, err := ensureLocalArchive(ctx, row)
	if err != nil {
		return RestoreResult{}, err
	}
	defer release()
	if err := prepareDir(Directory); err != nil {
		return RestoreResult{}, err
	}
	stage, err := os.MkdirTemp(Directory, ".restore-*")
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup: create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	manifest, err := stageArchive(archive, *row.SHA256, req.Passphrase, stage)
	if err != nil {
		return RestoreResult{}, err
	}

	preID, err := runLocked(ctx, Request{
		Contents: []string{ContentDatabase, ContentAttachments, ContentSettings},
		Note:     fmt.Sprintf("before restoring backup %d", req.ID),
		Kind:     KindPreRestore,
		Actor:    req.Actor,
	})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("pre-restore backup: %w", err)
	}
	if req.EnterMaintenance != nil {
		if err := req.EnterMaintenance(); err != nil {
			return RestoreResult{PreRestoreID: preID}, fmt.Errorf("enter maintenance mode: %w", err)
		}
	}
	journal, err := prepareRestoreJournal(manifest)
	if err != nil {
		return RestoreResult{PreRestoreID: preID}, err
	}
	if err := applyDatabaseAndFiles(ctx, manifest, stage, journal); err != nil {
		if journal != "" {
			markerID, markerErr := restoreJournalMarkerID(journal)
			if markerErr != nil {
				return RestoreResult{PreRestoreID: preID}, errors.Join(err, fmt.Errorf("read restore journal after transaction failure: %w", markerErr))
			}
			committed, markerErr := restoreCommitMarkerExists(data.DB, markerID)
			if markerErr != nil {
				// The commit outcome cannot be proved. Keep both the journal and
				// preserved files for startup recovery instead of guessing.
				return RestoreResult{PreRestoreID: preID}, errors.Join(err, fmt.Errorf("check restore commit after transaction failure: %w", markerErr))
			}
			if committed {
				requestRestoreRestart(manifest)
				if cleanupErr := completeRestoreJournal(journal); cleanupErr != nil {
					return RestoreResult{PreRestoreID: preID}, errors.Join(err, fmt.Errorf("complete committed restore journal: %w", cleanupErr))
				}
				return RestoreResult{PreRestoreID: preID}, nil
			}
		}
		if recoveryErr := rollbackRestoreJournal(journal); recoveryErr != nil {
			return RestoreResult{PreRestoreID: preID}, errors.Join(err, fmt.Errorf("recover restore files: %w", recoveryErr))
		}
		return RestoreResult{PreRestoreID: preID}, err
	}
	requestRestoreRestart(manifest)
	if err := completeRestoreJournal(journal); err != nil {
		return RestoreResult{PreRestoreID: preID}, fmt.Errorf("complete restore journal: %w", err)
	}
	return RestoreResult{PreRestoreID: preID}, nil
}

// Verify checks backup id without changing anything and returns its manifest.
func Verify(ctx context.Context, id int64, passphrase string) (Manifest, error) {
	row, err := loadRecord(ctx, id)
	if err != nil {
		return Manifest{}, err
	}
	if row.State != stateDone || row.SHA256 == nil {
		return Manifest{}, ErrNotDone
	}
	archive, release, err := ensureLocalArchive(ctx, row)
	if err != nil {
		return Manifest{}, err
	}
	defer release()
	if err := prepareDir(Directory); err != nil {
		return Manifest{}, err
	}
	stage, err := os.MkdirTemp(Directory, ".verify-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	return stageArchive(archive, *row.SHA256, passphrase, stage)
}

// stageArchive checks the file against its recorded checksum, decrypts it into
// stage, and checks every entry against the manifest. Nothing outside stage is
// written.
func stageArchive(archive, wantSHA, passphrase, stage string) (Manifest, error) {
	f, err := os.Open(archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if st.Size() > MaxArchiveBytes {
		return Manifest{}, fmt.Errorf("%w: archive is larger than the limit", ErrInvalidArchive)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), wantSHA) {
		return Manifest{}, ErrChecksum
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, err
	}

	dec, err := newDecryptor(f, passphrase)
	if err != nil {
		return Manifest{}, err
	}
	zipPath := filepath.Join(stage, "payload.zip")
	out, err := os.OpenFile(zipPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: stage payload: %w", err)
	}
	n, err := io.Copy(out, io.LimitReader(dec, MaxArchiveBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if errors.Is(err, ErrInvalidArchive) || errors.Is(err, ErrPassphraseRequired) {
			return Manifest{}, err
		}
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if n > MaxArchiveBytes {
		return Manifest{}, fmt.Errorf("%w: payload is larger than the limit", ErrInvalidArchive)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: payload is not a zip file", ErrInvalidArchive)
	}
	defer func() { _ = zr.Close() }()
	return extractArchive(zr, stage)
}

// extractArchive checks the entry names and sizes, reads the manifest, and
// extracts each declared entry under stage/content while checking its size and
// checksum against the manifest.
func extractArchive(zr *zip.ReadCloser, stage string) (Manifest, error) {
	entries := make(map[string]*zip.File, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		if !validEntryName(f.Name) {
			return Manifest{}, fmt.Errorf("%w: %q", ErrUnsafeEntry, f.Name)
		}
		if _, dup := entries[f.Name]; dup {
			return Manifest{}, fmt.Errorf("%w: duplicate entry %q", ErrUnsafeEntry, f.Name)
		}
		if f.UncompressedSize64 > 0 {
			compressed := max(f.CompressedSize64, 1)
			if f.UncompressedSize64/compressed > MaxExpansionRatio {
				return Manifest{}, fmt.Errorf("%w: %q expands too much", ErrUnsafeEntry, f.Name)
			}
		}
		total += f.UncompressedSize64
		if total > MaxArchiveBytes {
			return Manifest{}, fmt.Errorf("%w: archive expands past the size limit", ErrUnsafeEntry)
		}
		entries[f.Name] = f
	}

	manifestFile, ok := entries[manifestEntry]
	if !ok {
		return Manifest{}, fmt.Errorf("%w: manifest is missing", ErrInvalidArchive)
	}
	raw, err := readEntry(manifestFile, maxManifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: manifest is not valid JSON", ErrInvalidArchive)
	}
	if m.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("%w: unsupported manifest version %d", ErrInvalidArchive, m.Version)
	}

	type expect struct {
		sum  string
		size int64
	}
	declared := map[string]expect{}
	for name, info := range m.Tables {
		if !tableNamePattern.MatchString(name) || info.File != dbPrefix+name+dbSuffix {
			return Manifest{}, fmt.Errorf("%w: table entry %q is not allowed", ErrUnsafeEntry, name)
		}
		declared[info.File] = expect{info.SHA256, info.Size}
	}
	for _, fi := range m.Files {
		if fi.Path == manifestEntry || strings.HasPrefix(fi.Path, dbPrefix) || !validEntryName(fi.Path) {
			return Manifest{}, fmt.Errorf("%w: %q", ErrUnsafeEntry, fi.Path)
		}
		declared[fi.Path] = expect{fi.SHA256, fi.Size}
	}
	for name := range entries {
		if name == manifestEntry {
			continue
		}
		if _, ok := declared[name]; !ok {
			return Manifest{}, fmt.Errorf("%w: undeclared entry %q", ErrInvalidArchive, name)
		}
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		if _, ok := entries[name]; !ok {
			return Manifest{}, fmt.Errorf("%w: declared entry %q is missing", ErrInvalidArchive, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	root := filepath.Join(stage, contentDir)
	for _, name := range names {
		want := declared[name]
		got, err := extractEntry(entries[name], root, name, want.size)
		if err != nil {
			return Manifest{}, err
		}
		if !strings.EqualFold(got, want.sum) {
			return Manifest{}, ErrChecksum
		}
	}
	return m, nil
}

// extractEntry writes one zip entry under root and returns its SHA-256.
func extractEntry(f *zip.File, root, name string, size int64) (string, error) {
	dst := filepath.Join(root, filepath.FromSlash(name))
	if !isInside(root, dst) {
		return "", fmt.Errorf("%w: %q", ErrUnsafeEntry, name)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", fmt.Errorf("backup: stage %s: %w", name, err)
	}
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("backup: stage %s: %w", name, err)
	}
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, digest), io.LimitReader(rc, size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if n != size {
		return "", fmt.Errorf("%w: %q has the wrong size", ErrInvalidArchive, name)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: %q is too large", ErrInvalidArchive, f.Name)
	}
	return raw, nil
}

// validEntryName allows only the entry names this package writes. Anything
// with a path separator, a dot segment or an absolute path is refused, which
// stops a zip-slip entry before any file is created.
func validEntryName(name string) bool {
	switch {
	case name == manifestEntry, name == authSecretsEntry:
		return true
	case strings.HasPrefix(name, dbPrefix) && strings.HasSuffix(name, dbSuffix):
		return tableNamePattern.MatchString(strings.TrimSuffix(strings.TrimPrefix(name, dbPrefix), dbSuffix))
	case strings.HasPrefix(name, attachmentsPrefix):
		rel := strings.TrimPrefix(name, attachmentsPrefix)
		return rel != "" &&
			!strings.HasSuffix(rel, "/") &&
			!strings.Contains(rel, `\`) &&
			path.Clean(rel) == rel &&
			filepath.IsLocal(filepath.FromSlash(rel))
	}
	return false
}

// isInside reports whether p lies under root after cleaning.
func isInside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && filepath.IsLocal(rel)
}

// applyDatabase replaces the rows of every table in the manifest inside one
// transaction. Tables are cleared children first and filled parents first, so
// foreign keys hold at every step.
func applyDatabase(ctx context.Context, m Manifest, stage string) error {
	return applyDatabaseAndFiles(ctx, m, stage, "")
}

func applyDatabaseAndFiles(ctx context.Context, m Manifest, stage, journal string) error {
	names := make([]string, 0, len(m.Tables))
	for name := range m.Tables {
		names = append(names, name)
	}
	sort.Strings(names)

	return data.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := schemaVersion(tx)
		if err != nil {
			return err
		}
		if m.SchemaVersion != current {
			return fmt.Errorf("%w: archive is schema %d, database is schema %d", ErrSchemaTooNew, m.SchemaVersion, current)
		}
		existing, err := allTables(tx)
		if err != nil {
			return err
		}
		have := make(map[string]bool, len(existing))
		for _, t := range existing {
			have[t] = true
		}
		for _, name := range names {
			if !have[name] {
				return fmt.Errorf("%w: table %s is not in this database", ErrInvalidArchive, name)
			}
		}
		order, err := restoreOrder(tx, names)
		if err != nil {
			return err
		}
		selfReferences, err := deferSelfReferences(tx, names)
		if err != nil {
			return err
		}
		// Stop concurrent readers and writers from observing an intermediate
		// dataset. The locks are transaction scoped and held through file swap.
		for _, name := range names {
			if err := tx.Exec("LOCK TABLE " + quoteIdent(name) + " IN ACCESS EXCLUSIVE MODE").Error; err != nil {
				return fmt.Errorf("backup: lock %s: %w", name, err)
			}
		}
		triggers, err := disableRestoreTriggers(tx)
		if err != nil {
			return err
		}
		for i := len(order) - 1; i >= 0; i-- {
			if err := clearTable(tx, order[i]); err != nil {
				return err
			}
		}
		for _, name := range order {
			info := m.Tables[name]
			file := filepath.Join(stage, contentDir, filepath.FromSlash(info.File))
			if err := restoreTable(tx, name, file, info.Rows); err != nil {
				return err
			}
		}
		if _, restoringPosts := m.Tables["posts"]; restoringPosts {
			if err := data.RebuildAllPostSearchIndex(tx); err != nil {
				return fmt.Errorf("backup: rebuild restored post search index: %w", err)
			}
		}
		if err := resetSequences(tx, order); err != nil {
			return err
		}
		if err := restoreSelfReferenceConstraints(tx, selfReferences); err != nil {
			return err
		}
		if _, err := applyFilesWithJournal(m, stage, journal); err != nil {
			return err
		}
		markerID, err := restoreJournalMarkerID(journal)
		if err != nil {
			return err
		}
		if err := writeRestoreCommitMarker(tx, markerID); err != nil {
			return err
		}
		return restoreTriggerStates(tx, triggers)
	})
}

func requestRestoreRestart(m Manifest) {
	for _, fi := range m.Files {
		if fi.Path == authSecretsEntry {
			// The key file is now on disk, but process-wide auth state remains
			// unchanged until the graceful restart reloads it.
			update.RequestRestart()
			return
		}
	}
}

type selfReferenceConstraint struct {
	Table      string `gorm:"column:table_name"`
	Name       string `gorm:"column:constraint_name"`
	Deferrable bool   `gorm:"column:is_deferrable"`
}

func deferSelfReferences(tx *gorm.DB, tables []string) ([]selfReferenceConstraint, error) {
	var constraints []selfReferenceConstraint
	if err := tx.Raw(`SELECT c.relname AS table_name, f.conname AS constraint_name,
		f.condeferrable AS is_deferrable
		FROM pg_constraint f JOIN pg_class c ON c.oid=f.conrelid
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE f.contype='f' AND f.conrelid=f.confrelid
		AND n.nspname=current_schema() AND c.relname IN ?`, tables).Scan(&constraints).Error; err != nil {
		return nil, fmt.Errorf("backup: inspect self-referencing foreign keys: %w", err)
	}
	for _, fk := range constraints {
		if !fk.Deferrable {
			if err := tx.Exec("ALTER TABLE " + quoteIdent(fk.Table) + " ALTER CONSTRAINT " + quoteIdent(fk.Name) + " DEFERRABLE INITIALLY DEFERRED").Error; err != nil {
				return nil, fmt.Errorf("backup: defer self-reference %s: %w", fk.Name, err)
			}
		}
	}
	if len(constraints) > 0 {
		if err := tx.Exec("SET CONSTRAINTS ALL DEFERRED").Error; err != nil {
			return nil, fmt.Errorf("backup: defer restore constraints: %w", err)
		}
	}
	return constraints, nil
}

func restoreSelfReferenceConstraints(tx *gorm.DB, constraints []selfReferenceConstraint) error {
	if len(constraints) == 0 {
		return nil
	}
	if err := tx.Exec("SET CONSTRAINTS ALL IMMEDIATE").Error; err != nil {
		return fmt.Errorf("backup: validate restored constraints: %w", err)
	}
	for _, fk := range constraints {
		if fk.Deferrable {
			continue
		}
		if err := tx.Exec("ALTER TABLE " + quoteIdent(fk.Table) + " ALTER CONSTRAINT " + quoteIdent(fk.Name) + " NOT DEFERRABLE").Error; err != nil {
			return fmt.Errorf("backup: restore self-reference constraint %s: %w", fk.Name, err)
		}
	}
	return nil
}

type restoreTrigger struct {
	Table   string `gorm:"column:table_name"`
	Name    string `gorm:"column:trigger_name"`
	Enabled string `gorm:"column:enabled_state"`
}

// Only these application triggers enforce immutable ledgers or create audit
// snapshots. Foreign keys and all other triggers remain active during restore.
func disableRestoreTriggers(tx *gorm.DB) ([]restoreTrigger, error) {
	var found []restoreTrigger
	if err := tx.Raw(`SELECT c.relname AS table_name, t.tgname AS trigger_name, t.tgenabled AS enabled_state
		FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE NOT t.tgisinternal AND n.nspname=current_schema()
		AND t.tgname IN ?`, []string{
		"stock_movements_append_only", "widget_versions_immutable",
		"widget_versions_audit_change", "widgets_audit_change", "oauth_identities_audit_change",
	}).Scan(&found).Error; err != nil {
		return nil, fmt.Errorf("backup: inspect restore triggers: %w", err)
	}
	for _, trigger := range found {
		if trigger.Enabled == "D" {
			continue
		}
		if err := tx.Exec("ALTER TABLE " + quoteIdent(trigger.Table) + " DISABLE TRIGGER " + quoteIdent(trigger.Name)).Error; err != nil {
			return nil, fmt.Errorf("backup: disable restore trigger %s: %w", trigger.Name, err)
		}
	}
	return found, nil
}

func restoreTriggerStates(tx *gorm.DB, triggers []restoreTrigger) error {
	for _, trigger := range triggers {
		var stmt string
		switch trigger.Enabled {
		case "O":
			stmt = "ENABLE TRIGGER"
		case "A":
			stmt = "ENABLE ALWAYS TRIGGER"
		case "R":
			stmt = "ENABLE REPLICA TRIGGER"
		case "D":
			continue
		default:
			return fmt.Errorf("backup: unknown trigger state %q for %s", trigger.Enabled, trigger.Name)
		}
		if err := tx.Exec("ALTER TABLE " + quoteIdent(trigger.Table) + " " + stmt + " " + quoteIdent(trigger.Name)).Error; err != nil {
			return fmt.Errorf("backup: restore trigger %s: %w", trigger.Name, err)
		}
	}
	return nil
}

// restoreOrder returns names with every referenced table before the tables that
// reference it. Self references are ignored. A cycle between tables is an error.
func restoreOrder(tx *gorm.DB, names []string) ([]string, error) {
	type fkEdge struct {
		Child  string `gorm:"column:child"`
		Parent string `gorm:"column:parent"`
	}
	var edges []fkEdge
	if err := tx.Raw(`SELECT rc.relname AS child, pc.relname AS parent
		FROM pg_constraint c
		JOIN pg_class rc ON rc.oid = c.conrelid
		JOIN pg_class pc ON pc.oid = c.confrelid
		JOIN pg_namespace n ON n.oid = rc.relnamespace
		WHERE c.contype = 'f' AND n.nspname = current_schema()`).Scan(&edges).Error; err != nil {
		return nil, fmt.Errorf("backup: read foreign keys: %w", err)
	}
	in := make(map[string]bool, len(names))
	for _, n := range names {
		in[n] = true
	}
	deps := map[string]map[string]bool{}
	for _, e := range edges {
		if !in[e.Child] || !in[e.Parent] || e.Child == e.Parent {
			continue
		}
		if deps[e.Child] == nil {
			deps[e.Child] = map[string]bool{}
		}
		deps[e.Child][e.Parent] = true
	}

	placed := make(map[string]bool, len(names))
	order := make([]string, 0, len(names))
	for len(order) < len(names) {
		progress := false
		for _, n := range names {
			if placed[n] {
				continue
			}
			ready := true
			for p := range deps[n] {
				if !placed[p] {
					ready = false
					break
				}
			}
			if ready {
				placed[n] = true
				order = append(order, n)
				progress = true
			}
		}
		if !progress {
			return nil, fmt.Errorf("%w: foreign keys form a cycle between restored tables", ErrInvalidArchive)
		}
	}
	return order, nil
}

// clearTable deletes the rows that a restore replaces. The options table keeps
// its maintenance rows, so the maintenance state set before the restore stays.
func clearTable(tx *gorm.DB, table string) error {
	stmt := "DELETE FROM " + quoteIdent(table)
	if table == "options" {
		stmt += ` WHERE key NOT LIKE 'maintenance!_%' ESCAPE '!'`
	}
	if err := tx.Exec(stmt).Error; err != nil {
		return fmt.Errorf("backup: clear %s: %w", table, err)
	}
	return nil
}

// restoreTable inserts the rows of one JSONL file. json_populate_recordset
// maps each object onto the table's own column types, so no per-table code is
// needed. wantRows is the count from the manifest.
func restoreTable(tx *gorm.DB, table, file string, wantRows int64) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer func() { _ = f.Close() }()

	q := quoteIdent(table)
	stmt := "INSERT INTO " + q + " SELECT * FROM json_populate_recordset(NULL::" + q + ", ?)"

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxJSONLine)
	var batch []string
	var batchBytes int
	var seen int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		payload := "[" + strings.Join(batch, ",") + "]"
		if err := tx.Exec(stmt, payload).Error; err != nil {
			return fmt.Errorf("backup: restore %s: %w", table, err)
		}
		batch = batch[:0]
		batchBytes = 0
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++
		if table == "options" && isMaintenanceOption(line) {
			continue
		}
		batch = append(batch, line)
		batchBytes += len(line)
		if len(batch) >= insertBatchRows || batchBytes >= insertBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if err := flush(); err != nil {
		return err
	}
	if seen != wantRows {
		return fmt.Errorf("%w: %s has %d rows, manifest says %d", ErrInvalidArchive, table, seen, wantRows)
	}
	return nil
}

func isMaintenanceOption(line string) bool {
	var kv struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(line), &kv); err != nil {
		return false
	}
	return strings.HasPrefix(kv.Key, maintenanceOptionPrefix)
}

// resetSequences moves each identity or serial sequence past the restored rows,
// so new rows do not collide with restored IDs.
func resetSequences(tx *gorm.DB, tables []string) error {
	type seqColumn struct {
		TableName  string `gorm:"column:table_name"`
		ColumnName string `gorm:"column:column_name"`
	}
	var cols []seqColumn
	if err := tx.Raw(`SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = current_schema()
		AND (is_identity = 'YES' OR column_default LIKE 'nextval(%')`).Scan(&cols).Error; err != nil {
		return fmt.Errorf("backup: read sequences: %w", err)
	}
	want := make(map[string]bool, len(tables))
	for _, t := range tables {
		want[t] = true
	}
	for _, c := range cols {
		if !want[c.TableName] {
			continue
		}
		stmt := fmt.Sprintf(
			"SELECT setval(pg_get_serial_sequence(?, ?), (SELECT COALESCE(MAX(%s), 0) + 1 FROM %s), false)",
			quoteIdent(c.ColumnName), quoteIdent(c.TableName))
		if err := tx.Exec(stmt, quoteIdent(c.TableName), c.ColumnName).Error; err != nil {
			return fmt.Errorf("backup: reset sequence for %s: %w", c.TableName, err)
		}
	}
	return nil
}

// applyFiles puts back attachments and, when the archive has it, auth-secrets.json.
func applyFiles(m Manifest, stage string) error {
	_, err := applyFilesWithJournal(m, stage, "")
	return err
}

func applyFilesWithJournal(m Manifest, stage, journal string) (bool, error) {
	attachments := false
	secretReplaced := false
	for _, content := range m.Contents {
		if content == ContentAttachments {
			attachments = true
		}
	}
	if attachments {
		if err := replaceAttachments(m, stage, journal); err != nil {
			return false, err
		}
	}
	for _, fi := range m.Files {
		src := filepath.Join(stage, contentDir, filepath.FromSlash(fi.Path))
		var dst string
		var mode os.FileMode
		switch {
		case fi.Path == authSecretsEntry:
			dst, mode = authSecretsFile, 0o600
		case strings.HasPrefix(fi.Path, attachmentsPrefix):
			continue
		default:
			continue
		}
		if fi.Path == authSecretsEntry && journal != "" {
			old := filepath.Join(journal, "auth-secrets-old")
			if _, err := os.Stat(dst); err == nil {
				if err := os.Rename(dst, old); err != nil {
					return false, fmt.Errorf("backup: preserve old authentication secrets: %w", err)
				}
				if err := syncDirectory(journal); err != nil {
					return false, fmt.Errorf("backup: sync preserved authentication secrets: %w", err)
				}
				if err := syncDirectory(filepath.Dir(dst)); err != nil {
					return false, fmt.Errorf("backup: sync authentication secrets directory: %w", err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
		}
		if err := copyFileAtomic(src, dst, mode); err != nil {
			return false, err
		}
		if fi.Path == authSecretsEntry {
			secretReplaced = true
			if err := setRestoreResourceApplied(journal, "secrets"); err != nil {
				return false, fmt.Errorf("backup: record restored secrets: %w", err)
			}
		}
	}
	return secretReplaced, nil
}

func replaceAttachments(m Manifest, stage, journal string) error {
	parent := filepath.Dir(attachmentsDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("backup: prepare attachments: %w", err)
	}
	newDir := filepath.Join(parent, ".uploads-restore-new")
	if err := os.RemoveAll(newDir); err != nil {
		return fmt.Errorf("backup: clear attachment staging: %w", err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return fmt.Errorf("backup: stage attachments: %w", err)
	}
	for _, fi := range m.Files {
		if !strings.HasPrefix(fi.Path, attachmentsPrefix) {
			continue
		}
		rel := strings.TrimPrefix(fi.Path, attachmentsPrefix)
		dst := filepath.Join(newDir, filepath.FromSlash(rel))
		if err := copyFileAtomic(filepath.Join(stage, contentDir, filepath.FromSlash(fi.Path)), dst, 0o644); err != nil {
			_ = os.RemoveAll(newDir)
			return err
		}
	}
	oldDir := attachmentsDir + ".restore-old"
	if journal != "" {
		oldDir = filepath.Join(journal, "uploads-old")
	}
	if err := os.RemoveAll(oldDir); err != nil {
		_ = os.RemoveAll(newDir)
		return err
	}
	if _, err := os.Stat(attachmentsDir); err == nil {
		if err := os.Rename(attachmentsDir, oldDir); err != nil {
			_ = os.RemoveAll(newDir)
			return fmt.Errorf("backup: preserve old attachments: %w", err)
		}
		if err := syncDirectory(parent); err != nil {
			_ = os.Rename(oldDir, attachmentsDir)
			return fmt.Errorf("backup: sync preserved attachments: %w", err)
		}
		if journal != "" {
			if err := syncDirectory(journal); err != nil {
				_ = os.Rename(oldDir, attachmentsDir)
				return fmt.Errorf("backup: sync attachments journal: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.RemoveAll(newDir)
		return err
	}
	if err := os.Rename(newDir, attachmentsDir); err != nil {
		_ = os.Rename(oldDir, attachmentsDir)
		return fmt.Errorf("backup: place restored attachments: %w", err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("backup: sync restored attachments: %w", err)
	}
	if err := setRestoreResourceApplied(journal, "attachments"); err != nil {
		return fmt.Errorf("backup: record restored attachments: %w", err)
	}
	return nil
}

// copyFileAtomic writes src to dst through a temporary file in dst's directory,
// so readers never see a partial file.
func copyFileAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("backup: open staged file: %w", err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("backup: prepare %s: %w", filepath.Dir(dst), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-*")
	if err != nil {
		return fmt.Errorf("backup: write %s: %w", dst, err)
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("backup: write %s: %w", dst, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("backup: write %s: %w", dst, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("backup: write %s: %w", dst, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("backup: write %s: %w", dst, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("backup: place %s: %w", dst, err)
	}
	if err := syncDirectory(filepath.Dir(dst)); err != nil {
		return fmt.Errorf("backup: sync %s directory: %w", dst, err)
	}
	done = true
	return nil
}
