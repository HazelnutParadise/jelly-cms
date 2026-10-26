package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
)

// errUnsupportedURL is returned for attachment links that are not http or https.
var errUnsupportedURL = errors.New("attachment url must be http or https")

// attachmentLocalName is the file name an attachment is stored under. It is a
// pure function of the source URL, so a rerun finds the same file without
// downloading it again. The hash prefix keeps two files with one base name apart.
func attachmentLocalName(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	base := sanitizeFileName(path.Base(attachmentPath(raw)))
	return hex.EncodeToString(sum[:4]) + "-" + base
}

// attachmentLocalURL is the public URL of an attachment after import.
func attachmentLocalURL(jobID int64, raw string) string {
	return "/uploads/imports/" + strconv.FormatInt(jobID, 10) + "/" + attachmentLocalName(raw)
}

// attachmentPath returns the URL path of raw, or "" when it has none.
func attachmentPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

// sanitizeFileName keeps ASCII letters, digits, dot, dash and underscore.
// Anything else becomes an underscore, and the result is at most 100 bytes.
func sanitizeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		out = "file"
	}
	if len(out) > 100 {
		out = out[len(out)-100:]
	}
	return out
}

// fetchAttachment downloads raw into the job's uploads folder and returns its
// public URL. Addresses that safehttp refuses come back wrapping
// safehttp.ErrBlockedAddress.
func fetchAttachment(ctx context.Context, jobID int64, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errUnsupportedURL
	}
	client := safehttp.Client(safehttp.Options{Timeout: 60 * time.Second, MaxBytes: MaxAttachmentBytes})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("attachment answered %d", resp.StatusCode)
	}

	dir := filepath.Join(UploadsDir, "imports", strconv.FormatInt(jobID, 10))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, attachmentLocalName(raw))
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, MaxAttachmentBytes+1))
	closeErr := f.Close()
	if copyErr == nil && n > MaxAttachmentBytes {
		copyErr = safehttp.ErrBodyTooLarge
	}
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return attachmentLocalURL(jobID, raw), nil
}

// isBlocked reports whether err means the address was refused by safehttp.
func isBlocked(err error) bool {
	return errors.Is(err, safehttp.ErrBlockedAddress)
}
