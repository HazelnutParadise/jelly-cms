package backup

import (
	"archive/zip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/version"
	"gorm.io/gorm"
)

// Archive layout. The file is a short plaintext header followed by AES-256-GCM
// chunks. The inner payload is a zip with one JSONL file per table, optional
// attachment files and manifest.json.
//
// Header (30 bytes): "JBKP", format version (1), key mode (0 local key,
// 1 passphrase), salt (16 bytes, zero in local mode), nonce prefix (8 bytes).
//
// Each chunk: final flag (1 byte), ciphertext length (4 bytes, big endian),
// ciphertext with its 16-byte tag. The nonce is the prefix followed by the
// chunk counter. The header and the flag are additional data, so a changed
// header, a reordered chunk or a truncated file fails to open.
const (
	archiveMagic      = "JBKP"
	archiveFormat     = 1
	keyModeLocal      = 0
	keyModePassphrase = 1
	chunkSize         = 64 << 10
	gcmTagSize        = 16
	saltSize          = 16
	noncePrefixSize   = 8
	headerSize        = 4 + 1 + 1 + saltSize + noncePrefixSize
	keySize           = 32
	pbkdf2Rounds      = 600000
)

// excludedTables are never part of a backup. backups and jobs describe the
// backup and queue machinery itself, system_logs is disposable, and
// schema_migrations is replaced by the schema version in the manifest.
// options is included only with the settings content.
var excludedTables = map[string]bool{
	"schema_migrations": true,
	"backups":           true,
	"jobs":              true,
	"system_logs":       true,
	"options":           true,
}

// artifact is a finished archive on disk.
type artifact struct {
	Path     string
	Size     int64
	SHA256   string
	Manifest Manifest
}

// writeArchive writes an encrypted archive for req to Directory. It writes to
// a partial file first, so a crash never leaves a half archive under a final name.
func writeArchive(ctx context.Context, req Request) (artifact, error) {
	if err := prepareDir(Directory); err != nil {
		return artifact{}, err
	}
	mode, salt := byte(keyModeLocal), make([]byte, saltSize)
	var key []byte
	if req.Passphrase != "" {
		mode = keyModePassphrase
		var err error
		if salt, err = randomBytes(saltSize); err != nil {
			return artifact{}, err
		}
		if key, err = deriveKey(req.Passphrase, salt); err != nil {
			return artifact{}, err
		}
	} else {
		var err error
		if key, err = localKey(true); err != nil {
			return artifact{}, err
		}
	}
	prefix, err := randomBytes(noncePrefixSize)
	if err != nil {
		return artifact{}, err
	}

	tmp, err := os.CreateTemp(Directory, ".partial-*")
	if err != nil {
		return artifact{}, fmt.Errorf("backup: create archive file: %w", err)
	}
	tmpName := tmp.Name()
	finished := false
	defer func() {
		if !finished {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	enc, err := newEncryptor(tmp, key, mode, salt, prefix)
	if err != nil {
		return artifact{}, err
	}
	zw := zip.NewWriter(enc)
	manifest, err := writeEntries(ctx, zw, req)
	if err != nil {
		return artifact{}, err
	}
	if err := zw.Close(); err != nil {
		return artifact{}, fmt.Errorf("backup: close payload: %w", err)
	}
	if err := enc.Close(); err != nil {
		return artifact{}, fmt.Errorf("backup: finish encryption: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return artifact{}, fmt.Errorf("backup: sync archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return artifact{}, fmt.Errorf("backup: close archive: %w", err)
	}
	final := filepath.Join(Directory, newArchiveName(time.Now().UTC()))
	if err := os.Rename(tmpName, final); err != nil {
		return artifact{}, fmt.Errorf("backup: place archive: %w", err)
	}
	finished = true
	size, sum, err := fileDigest(final)
	if err != nil {
		_ = os.Remove(final)
		return artifact{}, err
	}
	return artifact{Path: final, Size: size, SHA256: sum, Manifest: manifest}, nil
}

// writeEntries writes every zip entry for req and returns the manifest. All
// table reads share one REPEATABLE READ snapshot, so the tables agree with each other.
func writeEntries(ctx context.Context, zw *zip.Writer, req Request) (Manifest, error) {
	tx := data.DB.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if tx.Error != nil {
		return Manifest{}, fmt.Errorf("backup: begin snapshot: %w", tx.Error)
	}
	defer func() { _ = tx.Rollback() }()

	m := Manifest{
		Version:    manifestVersion,
		CreatedAt:  time.Now().UTC(),
		AppVersion: version.Version,
		Contents:   req.Contents,
		Tables:     map[string]TableInfo{},
		Files:      []FileInfo{},
	}
	schema, err := schemaVersion(tx)
	if err != nil {
		return Manifest{}, err
	}
	m.SchemaVersion = schema

	tables, err := backupTables(tx, req.Contents)
	if err != nil {
		return Manifest{}, err
	}
	for _, name := range tables {
		info, err := writeTable(zw, tx, name, m.CreatedAt)
		if err != nil {
			return Manifest{}, err
		}
		m.Tables[name] = info
	}
	if slices.Contains(req.Contents, ContentAttachments) {
		files, err := writeAttachments(zw, m.CreatedAt)
		if err != nil {
			return Manifest{}, err
		}
		m.Files = append(m.Files, files...)
	}
	if req.Passphrase != "" {
		if _, statErr := os.Stat(authSecretsFile); statErr == nil {
			info, err := writeFileEntry(zw, authSecretsEntry, authSecretsFile, m.CreatedAt)
			if err != nil {
				return Manifest{}, err
			}
			m.Files = append(m.Files, info)
		}
	}

	raw, err := json.Marshal(m)
	if err != nil {
		return Manifest{}, err
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: manifestEntry, Method: zip.Deflate, Modified: m.CreatedAt})
	if err != nil {
		return Manifest{}, err
	}
	if _, err := w.Write(raw); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// writeTable writes the rows of one table as JSONL. row_to_json renders each
// row, so every table is handled the same way without a per-table column list.
func writeTable(zw *zip.Writer, tx *gorm.DB, table string, now time.Time) (TableInfo, error) {
	entry := dbPrefix + table + dbSuffix
	w, err := zw.CreateHeader(&zip.FileHeader{Name: entry, Method: zip.Deflate, Modified: now})
	if err != nil {
		return TableInfo{}, err
	}
	digest := sha256.New()
	out := io.MultiWriter(w, digest)
	info := TableInfo{File: entry}

	rows, err := tx.Raw("SELECT row_to_json(t)::text FROM " + quoteIdent(table) + " AS t").Rows()
	if err != nil {
		return TableInfo{}, fmt.Errorf("backup: read %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return TableInfo{}, fmt.Errorf("backup: scan %s: %w", table, err)
		}
		n, err := io.WriteString(out, line+"\n")
		if err != nil {
			return TableInfo{}, err
		}
		info.Rows++
		info.Size += int64(n)
	}
	if err := rows.Err(); err != nil {
		return TableInfo{}, fmt.Errorf("backup: read %s: %w", table, err)
	}
	info.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return info, nil
}

// writeAttachments adds every regular file under the uploads directory. Links
// are skipped, so an archive never follows a symlink out of the tree.
func writeAttachments(zw *zip.Writer, now time.Time) ([]FileInfo, error) {
	if _, err := os.Stat(attachmentsDir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var files []FileInfo
	err := filepath.WalkDir(attachmentsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(attachmentsDir, path)
		if err != nil {
			return err
		}
		info, err := writeFileEntry(zw, attachmentsPrefix+filepath.ToSlash(rel), path, now)
		if err != nil {
			return err
		}
		files = append(files, info)
		return nil
	})
	return files, err
}

func writeFileEntry(zw *zip.Writer, name, src string, now time.Time) (FileInfo, error) {
	f, err := os.Open(src)
	if err != nil {
		return FileInfo{}, fmt.Errorf("backup: open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
	if err != nil {
		return FileInfo{}, err
	}
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, digest), f)
	if err != nil {
		return FileInfo{}, fmt.Errorf("backup: copy %s: %w", name, err)
	}
	return FileInfo{Path: name, Size: n, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// backupTables lists the tables for the requested contents, sorted by name.
func backupTables(tx *gorm.DB, contents []string) ([]string, error) {
	var names []string
	if slices.Contains(contents, ContentDatabase) {
		all, err := allTables(tx)
		if err != nil {
			return nil, err
		}
		for _, t := range all {
			if !excludedTables[t] {
				names = append(names, t)
			}
		}
	}
	if slices.Contains(contents, ContentSettings) {
		names = append(names, "options")
	}
	sort.Strings(names)
	return names, nil
}

func allTables(tx *gorm.DB) ([]string, error) {
	var names []string
	if err := tx.Raw("SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename").Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("backup: list tables: %w", err)
	}
	return names, nil
}

func schemaVersion(tx *gorm.DB) (int, error) {
	var v int
	if err := tx.Raw("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&v).Error; err != nil {
		return 0, fmt.Errorf("backup: read schema version: %w", err)
	}
	return v, nil
}

// quoteIdent quotes a SQL identifier. Table names here come from the catalog
// or from a validated archive manifest.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// deriveKey turns a passphrase into the archive key with PBKDF2-SHA256.
func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, pbkdf2Rounds, keySize)
	if err != nil {
		return nil, fmt.Errorf("backup: derive key: %w", err)
	}
	return key, nil
}

// keyFor returns the key an archive header asks for. Local-key archives use
// data/backup-key, which stays on this installation and is not in the backup
// directory. Passphrase archives need the passphrase.
func keyFor(mode byte, passphrase string, salt []byte, create bool) ([]byte, error) {
	switch mode {
	case keyModeLocal:
		return localKey(create)
	case keyModePassphrase:
		if passphrase == "" {
			return nil, ErrPassphraseRequired
		}
		return deriveKey(passphrase, salt)
	}
	return nil, fmt.Errorf("%w: unknown key mode", ErrInvalidArchive)
}

// localKey reads data/backup-key, creating it with 0600 when create is set.
func localKey(create bool) ([]byte, error) {
	raw, err := os.ReadFile(localKeyFile)
	switch {
	case err == nil:
		if len(raw) != keySize {
			return nil, fmt.Errorf("%w: backup key file is damaged", ErrInvalidArchive)
		}
		return raw, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("backup: read key: %w", err)
	case !create:
		return nil, fmt.Errorf("%w: the backup key is missing on this installation", ErrInvalidArchive)
	}
	if err := os.MkdirAll(filepath.Dir(localKeyFile), 0o755); err != nil {
		return nil, fmt.Errorf("backup: prepare key directory: %w", err)
	}
	key, err := randomBytes(keySize)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(localKeyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return localKey(create)
	}
	if err != nil {
		return nil, fmt.Errorf("backup: create key: %w", err)
	}
	_, werr := f.Write(key)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(localKeyFile)
		return nil, fmt.Errorf("backup: write key: %w", err)
	}
	return key, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryptor writes the header and then sealed chunks. A chunk is emitted
// only when more data follows it, so the last chunk is the one Close emits
// with the final flag set.
type encryptor struct {
	out     io.Writer
	aead    cipher.AEAD
	header  []byte
	prefix  []byte
	counter uint32
	buf     []byte
}

func newEncryptor(out io.Writer, key []byte, mode byte, salt, prefix []byte) (*encryptor, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 0, headerSize)
	header = append(header, archiveMagic...)
	header = append(header, archiveFormat, mode)
	header = append(header, salt...)
	header = append(header, prefix...)
	if _, err := out.Write(header); err != nil {
		return nil, fmt.Errorf("backup: write header: %w", err)
	}
	return &encryptor{out: out, aead: aead, header: header, prefix: prefix, buf: make([]byte, 0, chunkSize)}, nil
}

func (e *encryptor) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if len(e.buf) == chunkSize {
			if err := e.seal(false); err != nil {
				return written, err
			}
		}
		n := min(chunkSize-len(e.buf), len(p))
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		written += n
	}
	return written, nil
}

// Close seals the remaining bytes as the final chunk.
func (e *encryptor) Close() error {
	return e.seal(true)
}

func (e *encryptor) seal(final bool) error {
	if e.counter == math.MaxUint32 {
		return errors.New("backup: archive is too large to encrypt")
	}
	flag := byte(0)
	if final {
		flag = 1
	}
	nonce := make([]byte, 12)
	copy(nonce, e.prefix)
	binary.BigEndian.PutUint32(nonce[8:], e.counter)
	aad := append(append([]byte{}, e.header...), flag)
	ct := e.aead.Seal(nil, nonce, e.buf, aad)

	var frame [5]byte
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:], uint32(len(ct)))
	if _, err := e.out.Write(frame[:]); err != nil {
		return err
	}
	if _, err := e.out.Write(ct); err != nil {
		return err
	}
	e.counter++
	e.buf = e.buf[:0]
	return nil
}

// decryptor reads an archive written by encryptor. It rejects a bad header,
// a changed chunk, a truncated file and bytes after the final chunk.
type decryptor struct {
	in      io.Reader
	aead    cipher.AEAD
	header  []byte
	prefix  []byte
	counter uint32
	plain   []byte
	last    bool
}

func newDecryptor(in io.Reader, passphrase string) (*decryptor, error) {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(in, header); err != nil {
		return nil, fmt.Errorf("%w: header", ErrInvalidArchive)
	}
	if string(header[:4]) != archiveMagic || header[4] != archiveFormat {
		return nil, fmt.Errorf("%w: not a backup archive", ErrInvalidArchive)
	}
	mode := header[5]
	salt := header[6 : 6+saltSize]
	key, err := keyFor(mode, passphrase, salt, false)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &decryptor{in: in, aead: aead, header: header, prefix: header[6+saltSize:]}, nil
}

func (d *decryptor) Read(p []byte) (int, error) {
	for len(d.plain) == 0 {
		if d.last {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.plain)
	d.plain = d.plain[n:]
	return n, nil
}

func (d *decryptor) next() error {
	var frame [5]byte
	if _, err := io.ReadFull(d.in, frame[:]); err != nil {
		return fmt.Errorf("%w: archive is truncated", ErrInvalidArchive)
	}
	flag := frame[0]
	if flag > 1 {
		return fmt.Errorf("%w: bad chunk frame", ErrInvalidArchive)
	}
	size := binary.BigEndian.Uint32(frame[1:])
	if size < gcmTagSize || size > chunkSize+gcmTagSize {
		return fmt.Errorf("%w: bad chunk size", ErrInvalidArchive)
	}
	ct := make([]byte, size)
	if _, err := io.ReadFull(d.in, ct); err != nil {
		return fmt.Errorf("%w: archive is truncated", ErrInvalidArchive)
	}
	if d.counter == math.MaxUint32 {
		return fmt.Errorf("%w: archive is too large", ErrInvalidArchive)
	}
	nonce := make([]byte, 12)
	copy(nonce, d.prefix)
	binary.BigEndian.PutUint32(nonce[8:], d.counter)
	aad := append(append([]byte{}, d.header...), flag)
	pt, err := d.aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return fmt.Errorf("%w: wrong passphrase or damaged archive", ErrInvalidArchive)
	}
	d.counter++
	d.plain = pt
	if flag == 1 {
		d.last = true
		var probe [1]byte
		if n, _ := d.in.Read(probe[:]); n > 0 {
			return fmt.Errorf("%w: data after the final chunk", ErrInvalidArchive)
		}
	}
	return nil
}

func prepareDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("backup: prepare %s: %w", filepath.Dir(dir), err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("backup: prepare %s: %w", dir, err)
	}
	return os.Chmod(dir, 0o700)
}

func fileDigest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	digest := sha256.New()
	n, err := io.Copy(digest, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(digest.Sum(nil)), nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("backup: random: %w", err)
	}
	return b, nil
}

func newArchiveName(now time.Time) string {
	suffix, _ := randomBytes(4)
	return fmt.Sprintf("backup-%s-%s.jbk", now.Format("20060102T150405Z"), hex.EncodeToString(suffix))
}
