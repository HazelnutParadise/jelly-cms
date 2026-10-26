package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/version"
)

// Names inside a release. The archive is the platform's tar.gz, checksums.txt
// lists the sha256 of each archive, and checksums.txt.minisig signs that file.
const (
	checksumsName     = "checksums.txt"
	signatureName     = "checksums.txt.minisig"
	binaryName        = "jelly-cms"
	binaryNameWindows = "jelly-cms.exe"
	webPrefix         = "web/"
	maxChecksumBytes  = 1 << 20
	maxSignatureBytes = 64 << 10
	maxArchiveEntries = 10000
	healthcheckWait   = 30 * time.Second
	prevSuffix        = ".prev"
	// webDir is the shipped web tree, relative to the working directory.
	webDir = "web"
	// webBackupDir keeps the web tree that the last apply replaced.
	webBackupDir = "data/update-prev/web"
)

// Apply downloads release target, verifies it with sha256 and minisign, backs
// up the running executable as <exe>.prev, swaps in the new one and runs its
// healthcheck. A failed healthcheck restores the previous files and records
// rolled_back. Downgrades are refused here; Rollback is the only way back.
func Apply(ctx context.Context, target string) error {
	unlock, err := lockOp()
	if err != nil {
		return err
	}
	defer unlock()
	ctx, end := beginOp(ctx)
	defer end()

	if err := ValidVersion(target); err != nil {
		return err
	}
	exe, err := swappableExecutable()
	if err != nil {
		return err
	}
	current := version.Version
	if !IsNewer(target, current) {
		return ErrNotNewer
	}
	rel, err := findRelease(ctx, target)
	if err != nil {
		return failApply(err)
	}
	return applyRelease(ctx, rel, exe, current)
}

// failApply records err in the state file and returns it.
func failApply(err error) error {
	_ = mutateState(func(st *State) bool {
		st.Status = StatusFailed
		st.LastError = err.Error()
		return true
	})
	return err
}

// swappableExecutable returns the executable path when this process may replace
// it. Container, Kubernetes and package-managed runtimes are refused, and so is a
// directory that does not accept new files.
func swappableExecutable() (string, error) {
	if rt := version.Runtime(); rt.Kind != version.KindBinary {
		return "", &RuntimeError{Kind: rt.Kind}
	}
	exe, err := executablePath()
	if err != nil {
		return "", err
	}
	if !dirWritable(filepath.Dir(exe)) {
		return "", ErrNotWritable
	}
	return exe, nil
}

// archiveAssetName is the release archive for this platform.
func archiveAssetName() string {
	return fmt.Sprintf("jelly-cms_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

func applyRelease(ctx context.Context, rel Release, exe, current string) error {
	if publicKeyLine() == "" {
		return failApply(ErrNoSigningKey)
	}
	archiveName := archiveAssetName()
	sumsAsset, okSums := rel.asset(checksumsName)
	sigAsset, okSig := rel.asset(signatureName)
	archAsset, okArch := rel.asset(archiveName)
	if !okSums || !okSig || !okArch {
		return failApply(fmt.Errorf("%w: release lacks %s, %s or %s", ErrVerification, checksumsName, signatureName, archiveName))
	}

	sums, err := download(ctx, sumsAsset.URL, maxChecksumBytes)
	if err != nil {
		return failApply(err)
	}
	sig, err := download(ctx, sigAsset.URL, maxSignatureBytes)
	if err != nil {
		return failApply(err)
	}
	if err := verifySignature(sums, sig); err != nil {
		return failApply(fmt.Errorf("%w: %v", ErrVerification, err))
	}
	want, err := checksumFor(sums, archiveName)
	if err != nil {
		return failApply(err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(exe), ".jelly-update-")
	if err != nil {
		return failApply(fmt.Errorf("update: create staging directory: %w", err))
	}
	defer os.RemoveAll(staging)

	archivePath := filepath.Join(staging, archiveName)
	got, err := downloadTo(ctx, archAsset.URL, archivePath, MaxDownloadBytes)
	if err != nil {
		return failApply(err)
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return failApply(fmt.Errorf("%w: sha256 of %s does not match checksums.txt", ErrVerification, archiveName))
	}

	newBin, newWeb, err := extractArchive(archivePath, staging)
	if err != nil {
		return failApply(err)
	}
	if err := ctx.Err(); err != nil {
		return failApply(err)
	}
	if beforeSwap := beforeSwapCallback(ctx); beforeSwap != nil {
		if err := beforeSwap(ctx); err != nil {
			return failApply(err)
		}
		if err := ctx.Err(); err != nil {
			return failApply(err)
		}
	}

	if err := mutateState(func(st *State) bool {
		st.Status = StatusApplying
		st.Previous = current
		st.Latest = rel.Version
		st.LastError = ""
		return true
	}); err != nil {
		return err
	}
	ins, err := install(exe, newBin, newWeb)
	if err != nil {
		_ = ins.restore()
		return failApply(err)
	}
	if err := healthcheck(ctx, exe, rel.Version); err != nil {
		restoreNote := ""
		if rerr := ins.restore(); rerr != nil {
			restoreNote = " (restore also failed: " + rerr.Error() + ")"
		}
		_ = mutateState(func(st *State) bool {
			st.Status = StatusRolledBack
			st.LastError = err.Error() + restoreNote
			return true
		})
		return fmt.Errorf("%w: %v%s", ErrHealthcheck, err, restoreNote)
	}
	return mutateState(func(st *State) bool {
		st.Status = StatusApplied
		st.Latest = rel.Version
		st.FailedBoots = 0
		st.LastError = ""
		return true
	})
}

// fetch starts a GET for a release asset. The URL must pass allowURL, and the
// body is capped at limit bytes.
func fetch(ctx context.Context, raw string, limit int64) (*http.Response, error) {
	if err := allowURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := httpClient(limit, 10*time.Minute).Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("update: download answered %d", resp.StatusCode)
	}
	return resp, nil
}

// download reads a small release file into memory.
func download(ctx context.Context, raw string, limit int64) ([]byte, error) {
	resp, err := fetch(ctx, raw, limit)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// downloadTo streams a release file to dst and returns its sha256 hex digest.
func downloadTo(ctx context.Context, raw, dst string, limit int64) (string, error) {
	resp, err := fetch(ctx, raw, limit)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("update: download: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractArchive unpacks the archive into dir. It accepts exactly one
// executable (jelly-cms or jelly-cms.exe) and regular files under web/. Any
// other entry, an unsafe name, a link, or a duplicate aborts the extraction. It
// returns the staged executable and the staged web directory ("" when the
// archive has none).
func extractArchive(archive, dir string) (bin, web string, err error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", "", fmt.Errorf("%w: not a gzip archive", ErrArchive)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var total int64
	seen := map[string]bool{}
	for n := 0; ; n++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("%w: %v", ErrArchive, err)
		}
		if n >= maxArchiveEntries {
			return "", "", fmt.Errorf("%w: too many entries", ErrArchive)
		}
		isDir := h.Typeflag == tar.TypeDir
		if !isDir && h.Typeflag != tar.TypeReg {
			return "", "", fmt.Errorf("%w: %q is neither a file nor a directory", ErrArchive, h.Name)
		}
		name, err := cleanEntry(h.Name, isDir)
		if err != nil {
			return "", "", err
		}
		if seen[name] {
			return "", "", fmt.Errorf("%w: duplicate entry %q", ErrArchive, name)
		}
		seen[name] = true

		switch {
		case isDir:
			if name != webDir && !strings.HasPrefix(name, webPrefix) {
				return "", "", fmt.Errorf("%w: unexpected directory %q", ErrArchive, name)
			}
			continue
		case name == binaryName || name == binaryNameWindows:
			if bin != "" {
				return "", "", fmt.Errorf("%w: more than one executable", ErrArchive)
			}
			dst := filepath.Join(dir, "jelly-cms.new")
			written, err := writeLimited(dst, 0o755, tr, MaxDownloadBytes-total)
			if err != nil {
				return "", "", err
			}
			total += written
			bin = dst
		case strings.HasPrefix(name, webPrefix) && len(name) > len(webPrefix):
			rel := strings.TrimPrefix(name, webPrefix)
			dst := filepath.Join(dir, webDir, filepath.FromSlash(rel))
			written, err := writeLimited(dst, 0o644, tr, MaxDownloadBytes-total)
			if err != nil {
				return "", "", err
			}
			total += written
			web = filepath.Join(dir, webDir)
		default:
			return "", "", fmt.Errorf("%w: unexpected entry %q", ErrArchive, name)
		}
	}
	if bin == "" {
		return "", "", fmt.Errorf("%w: archive has no executable", ErrArchive)
	}
	return bin, web, nil
}

// cleanEntry validates a tar entry name. Names must be relative, slash
// separated and already clean, and must not contain control characters, colons
// or backslashes.
func cleanEntry(raw string, isDir bool) (string, error) {
	name := raw
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	bad := name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:")
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			bad = true
		}
	}
	if !bad {
		clean := path.Clean(name)
		bad = clean != name || clean == "." || clean == ".." || strings.HasPrefix(clean, "../")
	}
	if bad {
		return "", fmt.Errorf("%w: unsafe entry name %q", ErrArchive, raw)
	}
	return name, nil
}

// writeLimited writes src to dst with perm, refusing more than limit bytes.
func writeLimited(dst string, perm os.FileMode, src io.Reader, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("%w: archive exceeds the size limit", ErrArchive)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(src, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("%w: %v", ErrArchive, err)
	}
	if n > limit {
		_ = os.Remove(dst)
		return n, fmt.Errorf("%w: archive exceeds the size limit", ErrArchive)
	}
	if err := os.Chmod(dst, perm); err != nil {
		return n, err
	}
	return n, nil
}

// installed records which parts of the swap happened, so restore undoes only those.
type installed struct {
	exe     string
	exeMove bool // exe was moved to exe+prevSuffix
	webMove bool // webDir was moved to webBackupDir
}

// install moves the running executable to <exe>.prev and the staged one into
// its place. Then the staged web tree replaces webDir. On failure it restores
// what it changed and returns the error.
func install(exe, newBin, newWeb string) (installed, error) {
	ins := installed{exe: exe}
	prev := exe + prevSuffix
	if err := os.RemoveAll(prev); err != nil {
		return ins, fmt.Errorf("update: clear previous executable: %w", err)
	}
	if err := os.Rename(exe, prev); err != nil {
		return ins, fmt.Errorf("update: back up executable: %w", err)
	}
	ins.exeMove = true
	if err := os.Rename(newBin, exe); err != nil {
		return ins, fmt.Errorf("update: place new executable: %w", err)
	}
	if newWeb != "" {
		if err := os.RemoveAll(webBackupDir); err != nil {
			return ins, fmt.Errorf("update: clear web backup: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(webBackupDir), 0o755); err != nil {
			return ins, err
		}
		if _, err := os.Stat(webDir); err == nil {
			if err := os.Rename(webDir, webBackupDir); err != nil {
				return ins, fmt.Errorf("update: back up web: %w", err)
			}
			ins.webMove = true
		}
		if err := os.Rename(newWeb, webDir); err != nil {
			return ins, fmt.Errorf("update: place new web: %w", err)
		}
	}
	return ins, nil
}

// restore puts back the files that install moved.
func (ins installed) restore() error {
	var errs []error
	if ins.webMove {
		if err := os.RemoveAll(webDir); err != nil {
			errs = append(errs, err)
		} else if err := os.Rename(webBackupDir, webDir); err != nil {
			errs = append(errs, err)
		}
	}
	if ins.exeMove {
		prev := ins.exe + prevSuffix
		if err := os.Remove(ins.exe); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		} else if err := os.Rename(prev, ins.exe); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// healthcheck runs the installed executable with the healthcheck subcommand. It
// passes only when the process exits 0 and reports status ok and the target version.
func healthcheck(ctx context.Context, exe, target string) error {
	hctx, cancel := context.WithTimeout(ctx, healthcheckWait)
	defer cancel()
	cmd := exec.CommandContext(hctx, exe, "healthcheck")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run healthcheck: %w", err)
	}
	var report struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		return fmt.Errorf("decode healthcheck: %w", err)
	}
	if report.Status != "ok" || normalizeVersion(report.Version) != normalizeVersion(target) {
		return fmt.Errorf("healthcheck reported %q at version %q, want ok at %s", report.Status, report.Version, target)
	}
	return nil
}
