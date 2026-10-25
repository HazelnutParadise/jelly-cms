package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"github.com/labstack/echo/v4"
)

// uploadsRoot is the directory served under /uploads.
const uploadsRoot = "data/uploads"

// inlineImageTypes are the only types shown in the browser. Everything else,
// SVG included, is served as a download in a sandboxed context so an uploaded
// file cannot run script on the site origin.
var inlineImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/avif": true,
}

// uploadExtTypes pins the types of extensions whose built-in mapping varies
// between platforms. SVG must be recognised so it can be forced to download.
var uploadExtTypes = map[string]string{
	".svg":  "image/svg+xml",
	".webp": "image/webp",
	".avif": "image/avif",
}

// registerUploadRoutes mounts /uploads/* (GET and HEAD).
func registerUploadRoutes(e *echo.Echo) {
	e.GET("/uploads/*", serveUpload)
	e.HEAD("/uploads/*", serveUpload)
}

// serveUpload streams a file from uploadsRoot with the headers that keep
// uploads from acting as active content.
func serveUpload(c echo.Context) error {
	clean, err := storage.CleanPath(c.Param("*"))
	if err != nil {
		return echo.ErrNotFound
	}
	// Dot-prefixed names are temporary files of an unfinished write.
	for _, segment := range strings.Split(clean, "/") {
		if strings.HasPrefix(segment, ".") {
			return echo.ErrNotFound
		}
	}
	full := filepath.Join(uploadsRoot, filepath.FromSlash(clean))
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return echo.ErrNotFound
		}
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return echo.ErrNotFound
	}

	mediaType := uploadMediaType(clean, f)
	h := c.Response().Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set(echo.HeaderContentType, mediaType)
	if !inlineImageTypes[mediaType] {
		h.Set("Content-Disposition", "attachment")
		h.Set("Content-Security-Policy", "sandbox")
	}
	http.ServeContent(c.Response(), c.Request(), filepath.Base(clean), st.ModTime(), f)
	return nil
}

// uploadMediaType picks the type from the extension and falls back to sniffing
// the first 512 bytes. The file position is reset for ServeContent.
func uploadMediaType(name string, f io.ReadSeeker) string {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := uploadExtTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return strings.ToLower(strings.TrimSpace(strings.Split(t, ";")[0]))
	}
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	_, _ = f.Seek(0, io.SeekStart)
	return http.DetectContentType(buf[:n])
}
