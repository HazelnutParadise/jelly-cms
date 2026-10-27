package assetcheck

// Zero-CDN scan for ticket 12 slot Z1 (docs/tickets/12-zero-cdn-jelly-ui.md). The table-driven cases pin
// what counts as an external resource load and pass now. TestRepositoryLoadsNoExternalResources scans every
// .html, .tmpl, .css and .js file under web/, design/ and cmd/ and is expected to fail until the design
// boards stop loading Google Fonts (slot Z1). Do not edit these tests to match an implementation. No
// database is needed.

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the test file location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func kinds(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Kind+" "+f.URL)
	}
	return out
}

func TestScanReportsOnlyRealExternalResourceLoads(t *testing.T) {
	cases := []struct {
		name string
		file string // file name, selects the scanner
		src  string
		want []string // "<kind> <url>" per finding, in order; nil means clean
	}{
		{"anchor to another site is not a load", "a.html", `<a href="https://x.com/jelly">X</a><a href="//facebook.com">F</a>`, nil},
		{"google fonts stylesheet", "a.html", `<link href="https://fonts.googleapis.com/css2?family=A" rel="stylesheet">`,
			[]string{"link href https://fonts.googleapis.com/css2?family=A"}},
		{"preconnect with crossorigin", "a.html", `<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>`,
			[]string{"link href https://fonts.gstatic.com"}},
		{"link without rel still counts", "a.html", `<link href="https://cdn.example.com/a.css">`,
			[]string{"link href https://cdn.example.com/a.css"}},
		{"canonical and alternate are not loads", "a.html",
			`<link rel="canonical" href="https://example.com/x"><link rel="alternate" type="application/rss+xml" href="https://example.com/feed">`, nil},
		{"icon", "a.html", `<link rel="icon" href="//cdn.example.com/f.ico">`, []string{"link href //cdn.example.com/f.ico"}},
		{"self hosted stylesheet and script", "a.html",
			`<link rel="stylesheet" href="./fonts.css"><script src="/admin/assets/jelly-ui/jelly.js" type="module"></script><script src="./support.js"></script>`, nil},
		{"external script", "a.html", `<script type="module" src="https://unpkg.com/x@1/x.js"></script>`,
			[]string{"script src https://unpkg.com/x@1/x.js"}},
		{"inline script mentioning a url is not a load", "a.html", `<script>var u = "https://cdn.example.com/a.js";</script>`, nil},
		{"inline dynamic import is a load", "a.html", `<script>import('https://cdn.example.com/a.js');</script>`,
			[]string{"js import https://cdn.example.com/a.js"}},
		{"inline script assigned src is a load", "a.html", `<script>const s = document.createElement('script'); s.src = 'https://cdn.example.com/a.js';</script>`,
			[]string{"js .src = https://cdn.example.com/a.js"}},
		{"img protocol relative", "a.html", `<img src="//cdn.example.com/a.png" alt="">`, []string{"img src //cdn.example.com/a.png"}},
		{"img data uri", "a.html", `<img src="data:image/png;base64,AAAA" alt="">`, nil},
		{"img srcset", "a.html", `<img src="/a.png" srcset="/a.png 1x, https://cdn.example.com/a2.png 2x" alt="">`,
			[]string{"img srcset https://cdn.example.com/a2.png"}},
		{"iframe", "a.html", `<iframe src="https://www.youtube.com/embed/x"></iframe>`, []string{"iframe src https://www.youtube.com/embed/x"}},
		{"source in picture", "a.html", `<picture><source srcset="https://cdn.example.com/a.webp"></picture>`,
			[]string{"source srcset https://cdn.example.com/a.webp"}},
		{"escaped example inside textarea", "a.html",
			`<textarea>&lt;script async src="https://umami.example.com/script.js"&gt;&lt;/script&gt;</textarea>`, nil},
		{"literal example inside textarea is still text", "a.html",
			`<textarea><script src="https://umami.example.com/script.js"></script></textarea>`, nil},
		{"escaped example inside code", "a.html", `<code>&lt;link href="https://fonts.googleapis.com/x" rel="stylesheet"&gt;</code>`, nil},
		{"go template value", "a.html", `<link rel="stylesheet" href="{{.Base}}/theme.css"><img src="{{.Cover}}" alt="">`, nil},
		{"uppercase scheme and leading space", "a.html", `<SCRIPT SRC=" HTTPS://cdn.example.com/a.js"></SCRIPT>`,
			[]string{"script src HTTPS://cdn.example.com/a.js"}},
		{"style block import", "a.html", "<style>\n@import url(\"https://fonts.googleapis.com/x\");\nbody{color:red}</style>",
			[]string{"css url() https://fonts.googleapis.com/x"}},
		{"style attribute url", "a.html", `<div style="background:url(https://cdn.example.com/a.png)"></div>`,
			[]string{"style attribute css url() https://cdn.example.com/a.png"}},
		{"css url protocol relative", "a.css", `@font-face{font-family:A;src:url(//cdn.example.com/a.woff2) format("woff2")}`,
			[]string{"css url() //cdn.example.com/a.woff2"}},
		{"css quoted url", "a.css", `a{background:url('https://cdn.example.com/a.png')}`, []string{"css url() https://cdn.example.com/a.png"}},
		{"css string import", "a.css", `@import "https://fonts.googleapis.com/x";`, []string{"css @import https://fonts.googleapis.com/x"}},
		{"css relative url and data uri", "a.css", `@font-face{src:url("fonts/a.woff2")}i{background:url(data:image/svg+xml;utf8,<svg/>)}`, nil},
		{"css comment is ignored", "a.css", `/* url(https://cdn.example.com/a.png) */ a{color:red}`, nil},
		{"js static import", "a.js", `import { x } from "https://esm.sh/x";`, []string{"js import https://esm.sh/x"}},
		{"js static import with uppercase HTTP scheme", "a.js", `import x from "HTTP://cdn.example.com/x.js";`, []string{"js import HTTP://cdn.example.com/x.js"}},
		{"js dynamic import with uppercase HTTPS scheme", "a.mjs", `const m = await import('HTTPS://cdn.example.com/m.js');`, []string{"js import HTTPS://cdn.example.com/m.js"}},
		{"js dynamic import", "a.mjs", `const m = await import('https://cdn.example.com/m.js');`, []string{"js import https://cdn.example.com/m.js"}},
		{"js assigned src", "a.js", `s.src = "https://cdn.example.com/a.js";`, []string{"js .src = https://cdn.example.com/a.js"}},
		{"js string with a url is not a load", "a.js", `throw new Error("see https://github.com/markedjs/marked");`, nil},
		{"unknown extension is skipped", "a.txt", `<script src="https://cdn.example.com/a.js"></script>`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := kinds(ScanFile(tc.file, []byte(tc.src)))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("findings = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanReportsLineNumbers(t *testing.T) {
	src := "<html>\n<head>\n<style>\n/* c\n*/\na{b:url(https://x.test/a.png)}\n</style>\n<script src=\"https://x.test/a.js\"></script>\n</head></html>"
	got := ScanHTML(src)
	if len(got) != 2 || got[0].Line != 6 || got[1].Line != 8 {
		t.Fatalf("findings = %+v, want a css finding on line 6 and a script finding on line 8", got)
	}
}

// scannedRoots are the trees the issue names. design/shots is local screenshot output, never committed.
var scannedRoots = []string{"web", "design", "cmd"}

func TestRepositoryLoadsNoExternalResources(t *testing.T) {
	root := repoRoot(t)
	var report []string
	scanned := map[string]int{}
	for _, top := range scannedRoots {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if d.IsDir() {
				if rel == filepath.Join("design", "shots") || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".html", ".tmpl", ".css", ".js", ".mjs":
			default:
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned[top]++
			for _, f := range ScanFile(path, content) {
				report = append(report, filepath.ToSlash(rel)+":"+itoa(f.Line)+": "+f.Kind+" "+f.URL)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	// Guard against a scan that passes because it found nothing to read.
	if scanned["web"] < 30 || scanned["design"] < 60 {
		t.Fatalf("scanned too few files (%v); the repository layout changed or the roots are wrong", scanned)
	}
	sort.Strings(report)
	if len(report) > 0 {
		shown := report
		if len(shown) > 40 {
			shown = append(append([]string{}, report[:40]...), "... and "+itoa(len(report)-40)+" more")
		}
		t.Errorf("%d external resource loads (the page must serve every script, style, font, image and frame itself):\n%s",
			len(report), strings.Join(shown, "\n"))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
