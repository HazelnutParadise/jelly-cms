package assets

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrCycle         = errors.New("assets: cyclic CSS imports")
	ErrInvalidMethod = errors.New("assets: invalid method")
	ErrNotFound      = errors.New("assets: not found")
	ErrSymlinkEscape = errors.New("assets: symlink escape")
	ErrMissingMount  = errors.New("assets: missing mount root")
)

type Snapshot struct{ assets map[string]*asset }

type asset struct {
	data []byte
	hash string
	etag string
}

// Build reads each mounted tree once and freezes the bytes served by the result.
func Build(mounts map[string]string) (*Snapshot, error) {
	s := &Snapshot{assets: make(map[string]*asset)}
	prefixes := make([]string, 0, len(mounts))
	roots := make(map[string]string, len(mounts))
	for prefix, root := range mounts {
		if !validMountPrefix(prefix) {
			return nil, fmt.Errorf("assets: invalid mount prefix %q", prefix)
		}
		if root == "" {
			return nil, ErrMissingMount
		}
		prefixes = append(prefixes, prefix)
		roots[prefix] = root
	}
	sort.Strings(prefixes)
	for i, p := range prefixes {
		for _, other := range prefixes[i+1:] {
			if strings.HasPrefix(other, p) {
				return nil, fmt.Errorf("assets: overlapping mounts %q and %q", p, other)
			}
		}
	}
	for _, prefix := range prefixes {
		root := roots[prefix]
		info, err := os.Stat(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, ErrMissingMount
			}
			return nil, err
		}
		if !info.IsDir() {
			return nil, ErrMissingMount
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return ErrSymlinkEscape
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("assets: non-regular file %q", file)
			}
			absFile, err := filepath.Abs(file)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(absRoot, absFile)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return ErrSymlinkEscape
			}
			public := prefix + filepath.ToSlash(rel)
			if !safePath(public) {
				return fmt.Errorf("assets: unsafe asset path %q", public)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			s.assets[public] = &asset{data: bytes.Clone(data)}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	state := make(map[string]uint8, len(s.assets))
	var finalize func(string) error
	finalize = func(name string) error {
		a := s.assets[name]
		if a == nil {
			return nil
		}
		if state[name] == 1 {
			return ErrCycle
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		data := a.data
		if strings.EqualFold(path.Ext(name), ".css") {
			refs := cssReferences(data)
			for _, ref := range refs {
				child := resolveCSSPath(name, ref.value)
				if child == "" {
					continue
				}
				if s.assets[child] != nil {
					if err := finalize(child); err != nil {
						return err
					}
				}
			}
			var out strings.Builder
			last := 0
			for _, ref := range refs {
				child := resolveCSSPath(name, ref.value)
				dep := s.assets[child]
				if dep == nil {
					continue
				}
				rewritten := withHash(ref.value, (&url.URL{Path: child}).EscapedPath(), dep.hash)
				out.Write(data[last:ref.start])
				out.WriteString(rewritten)
				last = ref.end
			}
			out.Write(data[last:])
			data = []byte(out.String())
			a.data = data
		}
		sum := sha256.Sum256(data)
		a.hash = fmt.Sprintf("%x", sum)
		a.etag = `"` + a.hash + `"`
		state[name] = 2
		return nil
	}
	for _, name := range sortedAssetNames(s.assets) {
		if err := finalize(name); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func validMountPrefix(p string) bool {
	return strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") && !strings.ContainsAny(p, "\\?#%") && path.Clean(p) == strings.TrimSuffix(p, "/") && p != "/"
}

func safePath(p string) bool {
	if strings.ContainsAny(p, "\\%") || !strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}

func sortedAssetNames(m map[string]*asset) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type cssRef struct {
	start, end int
	value      string
}

// cssReferences recognizes url() and @import values in one lexical pass. CSS
// escapes are deliberately left untouched: decoding/re-encoding them safely
// requires a complete CSS tokenizer, and leaving such references is compatible.
func cssReferences(src []byte) []cssRef {
	text := string(src)
	var refs []cssRef
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "/*") {
			i = skipCSSComment(text, i)
			continue
		}
		if text[i] == '\'' || text[i] == '"' {
			i = skipCSSString(text, i)
			continue
		}
		if text[i] == '@' && i+7 <= len(text) && strings.EqualFold(text[i:i+7], "@import") && (i+7 == len(text) || !isCSSIdent(text[i+7])) {
			j := skipCSSSpaceAndComments(text, i+7)
			if j < len(text) && (text[j] == '\'' || text[j] == '"') {
				if ref, next, ok := readCSSString(text, j); ok {
					refs = append(refs, ref)
					i = next
					continue
				}
			}
			if k, ok := cssURLAt(text, j); ok {
				refs = append(refs, k.ref)
				i = k.next
				continue
			}
			i += 7
			continue
		}
		if i+4 <= len(text) && strings.EqualFold(text[i:i+3], "url") && (i == 0 || !isCSSIdent(text[i-1])) {
			if k, ok := cssURLAt(text, i); ok {
				refs = append(refs, k.ref)
				i = k.next
				continue
			}
		}
		i++
	}
	return refs
}

type cssURL struct {
	ref  cssRef
	next int
}

func cssURLAt(s string, i int) (cssURL, bool) {
	if i+3 > len(s) || !strings.EqualFold(s[i:i+3], "url") {
		return cssURL{}, false
	}
	j := i + 3
	for j < len(s) && isCSSSpace(s[j]) {
		j++
	}
	if j >= len(s) || s[j] != '(' {
		return cssURL{}, false
	}
	j++
	for j < len(s) && isCSSSpace(s[j]) {
		j++
	}
	start, end := j, j
	if j < len(s) && (s[j] == '\'' || s[j] == '"') {
		ref, next, ok := readCSSString(s, j)
		if !ok {
			return cssURL{}, false
		}
		end = ref.end
		j = next
		for j < len(s) && isCSSSpace(s[j]) {
			j++
		}
		if j >= len(s) || s[j] != ')' {
			return cssURL{}, false
		}
		return cssURL{ref: ref, next: j + 1}, true
	}
	start = j
	for j < len(s) && s[j] != ')' {
		if s[j] == '\\' {
			return cssURL{}, false
		}
		if s[j] == '\'' || s[j] == '"' || s[j] == '(' {
			return cssURL{}, false
		}
		j++
	}
	if j >= len(s) {
		return cssURL{}, false
	}
	end = j
	for end > start && isCSSSpace(s[end-1]) {
		end--
	}
	return cssURL{ref: cssRef{start: start, end: end, value: s[start:end]}, next: j + 1}, true
}

func readCSSString(s string, quoteAt int) (cssRef, int, bool) {
	q := s[quoteAt]
	for i := quoteAt + 1; i < len(s); i++ {
		if s[i] == '\\' {
			return cssRef{}, i, false
		}
		if s[i] == q {
			return cssRef{start: quoteAt + 1, end: i, value: s[quoteAt+1 : i]}, i + 1, true
		}
	}
	return cssRef{}, len(s), false
}
func skipCSSString(s string, i int) int {
	q := s[i]
	for i++; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			return i + 1
		}
	}
	return len(s)
}
func skipCSSComment(s string, i int) int {
	if end := strings.Index(s[i+2:], "*/"); end >= 0 {
		return i + 2 + end + 2
	}
	return len(s)
}
func skipCSSSpaceAndComments(s string, i int) int {
	for i < len(s) {
		if isCSSSpace(s[i]) {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "/*") {
			i = skipCSSComment(s, i)
			continue
		}
		break
	}
	return i
}
func isCSSSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
func isCSSIdent(c byte) bool {
	return c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func resolveCSSPath(base, raw string) string {
	if raw == "" || strings.Contains(raw, "\\") || strings.HasPrefix(raw, "#") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	ref := u.Path
	if ref == "" {
		return ""
	}
	var resolved string
	if strings.HasPrefix(ref, "/") {
		resolved = path.Clean(ref)
	} else {
		resolved = path.Clean(path.Join(path.Dir(base), ref))
	}
	if !strings.HasPrefix(resolved, "/") || strings.Contains(resolved, "%") || resolved == "/.." || strings.HasPrefix(resolved, "/../") {
		return ""
	}
	return resolved
}

func withHash(raw, assetPath, hash string) string {
	frag := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		frag, raw = raw[i:], raw[:i]
	}
	oldQuery := ""
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		oldQuery = raw[i+1:]
	}
	parts := strings.Split(oldQuery, "&")
	kept := parts[:0]
	for _, part := range parts {
		key := strings.SplitN(part, "=", 2)[0]
		decoded, err := url.QueryUnescape(key)
		if err == nil && decoded == "v" {
			continue
		}
		if part != "" {
			kept = append(kept, part)
		}
	}
	kept = append(kept, "v="+hash)
	q := strings.Join(kept, "&")
	return assetPath + "?" + q + frag
}

func assetContentType(name string, data []byte) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	}
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		return t
	}
	return http.DetectContentType(data)
}

// URL adds or replaces the content version for a known asset.
func (s *Snapshot) URL(raw string) string {
	if s == nil {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return raw
	}
	if strings.HasPrefix(raw, "//") || strings.Contains(u.Path, "\\") {
		return raw
	}
	a := s.assets[u.Path]
	if a == nil {
		return raw
	}
	rawPath := raw
	if i := strings.IndexAny(rawPath, "?#"); i >= 0 {
		rawPath = rawPath[:i]
	}
	return withHash(raw, rawPath, a.hash)
}

func (s *Snapshot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, ErrInvalidMethod.Error(), http.StatusMethodNotAllowed)
		return
	}
	rawPath := r.RequestURI
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		rawPath = rawPath[:i]
	}
	rawPath = strings.ToLower(rawPath)
	if s == nil || r.URL == nil || !safePath(r.URL.Path) || strings.Contains(rawPath, "%2f") || strings.Contains(rawPath, "%5c") || strings.Contains(rawPath, "%2e") {
		notFound(w)
		return
	}
	a := s.assets[r.URL.Path]
	if a == nil {
		notFound(w)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		notFound(w)
		return
	}
	versions := query["v"]
	hashed := len(versions) == 1 && versions[0] != ""
	if len(versions) > 1 || len(versions) == 1 && versions[0] != a.hash {
		notFound(w)
		return
	}
	if hashed {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Content-Type", assetContentType(r.URL.Path, a.data))
	w.Header().Set("ETag", a.etag)
	if r.Method == http.MethodHead {
		req := r.Clone(r.Context())
		req.Header.Del("Range")
		http.ServeContent(w, req, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(a.data))
		return
	}
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(a.data))
}

func notFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
}
