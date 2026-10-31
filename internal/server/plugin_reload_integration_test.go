package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

func TestMain(m *testing.M) {
	plugin.RunWorkerIfRequested()
	os.Exit(m.Run())
}

var pluginReloadRunCounter atomic.Uint64

func nextPluginReloadRun() string {
	return fmt.Sprintf("rld-%d-%d", time.Now().UnixNano(), pluginReloadRunCounter.Add(1))
}

type reloadHarness struct {
	*accessHarness
	runtime *plugin.Runtime
	logs    *reloadLogCapture
}

func newPluginReloadHarness(t *testing.T) *reloadHarness {
	t.Helper()

	h := newAccessHarness(t)
	runtime, err := plugin.NewRuntime()
	if err != nil {
		t.Fatalf("create plugin runtime: %v", err)
	}
	logs := startReloadLogCapture(t)
	t.Cleanup(logs.stop)
	t.Cleanup(runtime.Close)

	e := echo.New()
	RegisterRoutes(e, theme.NewManager("web/themes"), runtime)
	h.echo = e

	return &reloadHarness{accessHarness: h, runtime: runtime, logs: logs}
}

func (h *reloadHarness) loadPlugin(t *testing.T, id string) string {
	t.Helper()

	dir := filepath.Join("data", "plugins", id)
	if err := h.runtime.Load(context.Background(), dir); err != nil {
		t.Fatalf("load plugin %s: %v", id, err)
	}

	return dir
}

func (h *reloadHarness) callCalculate(phase string, n int) {
	h.runtime.CallHook(plugin.Hook("OnCalculate"), map[string]interface{}{"phase": phase, "n": n})
}

func (h *reloadHarness) reload(t *testing.T, id, token string) accessResult {
	t.Helper()

	return h.request(http.MethodPost, "/api/admin/plugins/"+id+"/reload", "", token, "application/json", "application/json")
}

type reloadLogCapture struct {
	original *os.File
	reader   *os.File
	writer   *os.File

	mu   sync.Mutex
	text strings.Builder

	done chan struct{}
	once sync.Once
}

func startReloadLogCapture(t *testing.T) *reloadLogCapture {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create plugin log pipe: %v", err)
	}

	capture := &reloadLogCapture{
		original: os.Stdout,
		reader:   reader,
		writer:   writer,
		done:     make(chan struct{}),
	}
	os.Stdout = writer

	go func() {
		defer close(capture.done)

		buffer := make([]byte, 4096)
		for {
			n, readErr := reader.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				capture.mu.Lock()
				_, _ = capture.text.Write(chunk)
				capture.mu.Unlock()
				_, _ = capture.original.Write(chunk)
			}
			if readErr != nil {
				return
			}
		}
	}()

	return capture
}

func (c *reloadLogCapture) stop() {
	if c == nil {
		return
	}

	c.once.Do(func() {
		os.Stdout = c.original
		_ = c.writer.Close()

		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			_ = c.reader.Close()
		}

		_ = c.reader.Close()
	})
}

func (c *reloadLogCapture) snapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.text.String()
}

func (c *reloadLogCapture) contains(marker string) bool {
	return strings.Contains(c.snapshot(), marker)
}

func (c *reloadLogCapture) waitFor(t *testing.T, marker string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.contains(marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("plugin log did not contain %q; logs:\n%s", marker, c.snapshot())
}

func (c *reloadLogCapture) requireAbsent(t *testing.T, marker string) {
	t.Helper()

	if c.contains(marker) {
		t.Fatalf("plugin log unexpectedly contained %q; logs:\n%s", marker, c.snapshot())
	}
}

func writeReloadPlugin(t *testing.T, dir string, meta plugin.PluginMetadata, script string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create plugin directory %s: %v", dir, err)
	}
	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal plugin manifest for %s: %v", meta.ID, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest for %s: %v", meta.ID, err)
	}
	if script == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, meta.Entrypoint), []byte(script), 0o600); err != nil {
		t.Fatalf("write plugin entry for %s: %v", meta.ID, err)
	}
}

func writeReloadManifest(t *testing.T, dir, raw string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create plugin directory %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write malformed plugin manifest in %s: %v", dir, err)
	}
}

func reloadPluginMeta(id, version, entrypoint string) plugin.PluginMetadata {
	return plugin.PluginMetadata{ID: id, Name: id, Version: version, Entrypoint: entrypoint}
}

func reloadHookScript(run, label, expression string) string {
	return fmt.Sprintf(`
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info(%q);
});
JellyCMS.registerHook("OnCalculate", function (data) {
  JellyCMS.log.info(%q + data.phase + ":" + (%s));
});
`, "RLD-"+run+"-boot-"+label, "RLD-"+run+"-"+label+":", expression)
}

func reloadCandidateScript(run, label, expression string) string {
	return fmt.Sprintf(`
JellyCMS.log.info(%q);
`, "RLD-"+run+"-entry-"+label) + reloadHookScript(run, label, expression)
}

func reloadBootFailureScript(run, secret string) string {
	return fmt.Sprintf(`
JellyCMS.log.info(%q);
JellyCMS.registerHook("OnBoot", function () {
  throw new Error(%q);
});
JellyCMS.registerHook("OnCalculate", function (data) {
  JellyCMS.log.info(%q + data.phase + ":" + (data.n * 10));
});
`, "RLD-"+run+"-entry-candidate", secret, "RLD-"+run+"-candidate:")
}

func reloadHangingBootScript(run, label string, multiplier int) string {
	return fmt.Sprintf(`
JellyCMS.log.info(%q);
JellyCMS.registerHook("OnBoot", function () {
  JellyCMS.log.info(%q);
  while (true) {}
});
JellyCMS.registerHook("OnCalculate", function (data) {
  JellyCMS.log.info(%q + data.phase + ":" + (data.n * %d));
});
`, "RLD-"+run+"-entry-"+label, "RLD-"+run+"-boot-"+label, "RLD-"+run+"-"+label+":", multiplier)
}

func reloadForbiddenBody(t *testing.T, body, secret string) {
	t.Helper()

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read work directory: %v", err)
	}
	for _, fragment := range []string{
		secret,
		"data/plugins",
		workDir,
		"SyntaxError",
		"OnBoot",
		"entry script failed",
		"reload not fully implemented",
	} {
		if strings.Contains(body, fragment) {
			t.Fatalf("error response leaked %q: %s", fragment, body)
		}
	}
}

func TestPluginReloadSwapsVersionsOnTheSameRuntime(t *testing.T) {
	h := newPluginReloadHarness(t)
	run := nextPluginReloadRun()

	mainID := "reload-main"
	otherID := "reload-other"
	mainDir := filepath.Join("data", "plugins", mainID)
	otherDir := filepath.Join("data", "plugins", otherID)

	writeReloadPlugin(t, mainDir, reloadPluginMeta(mainID, "1.0.0", "index.js"), reloadHookScript(run, "v1", "data.n + 1"))
	writeReloadPlugin(t, otherDir, reloadPluginMeta(otherID, "1.0.0", "index.js"), reloadHookScript(run, "other", "data.n + 100"))
	h.loadPlugin(t, mainID)
	h.loadPlugin(t, otherID)

	initial := run + "-initial"
	h.callCalculate(initial, 3)
	h.logs.waitFor(t, "RLD-"+run+"-v1:"+initial+":4")
	h.logs.waitFor(t, "RLD-"+run+"-other:"+initial+":103")

	writeReloadPlugin(t, mainDir, reloadPluginMeta(mainID, "2.0.0", "index.js"), reloadHookScript(run, "v2", "data.n * 10"))

	admin := h.user("admin", "reload-admin")
	result := h.reload(t, mainID, h.token(admin))
	if result.panicked != nil {
		t.Fatalf("reload handler panicked: %v", result.panicked)
	}
	if result.code != http.StatusOK {
		t.Fatalf("reload status = %d, want 200; body = %s", result.code, result.body)
	}

	after := run + "-after"
	h.callCalculate(after, 4)
	h.logs.waitFor(t, "RLD-"+run+"-boot-v2")
	h.logs.waitFor(t, "RLD-"+run+"-v2:"+after+":40")
	h.logs.waitFor(t, "RLD-"+run+"-other:"+after+":104")

	h.logs.requireAbsent(t, "RLD-"+run+"-v1:"+after)
}

func TestPluginReloadFailuresKeepThePreviousVersion(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, dir, run, secret string)
	}{
		{
			name: "script syntax",
			mutate: func(t *testing.T, dir, run, secret string) {
				writeReloadPlugin(t, dir, reloadPluginMeta("reload-broken", "2.0.0", "index.js"), "JellyCMS.registerHook(\"OnCalculate\", function (data) {")
			},
		},
		{
			name: "onboot throws",
			mutate: func(t *testing.T, dir, run, secret string) {
				writeReloadPlugin(t, dir, reloadPluginMeta("reload-broken", "2.0.0", "index.js"), reloadBootFailureScript(run, secret))
			},
		},
		{
			name: "missing entry",
			mutate: func(t *testing.T, dir, run, secret string) {
				writeReloadPlugin(t, dir, reloadPluginMeta("reload-broken", "2.0.0", "missing.js"), "")
			},
		},
		{
			name: "manifest damaged",
			mutate: func(t *testing.T, dir, run, secret string) {
				writeReloadManifest(t, dir, `{"id":`)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newPluginReloadHarness(t)
			run := nextPluginReloadRun()
			secret := "RELOAD_SECRET_" + run

			targetID := "reload-broken"
			controlID := "reload-control"
			targetDir := filepath.Join("data", "plugins", targetID)
			controlDir := filepath.Join("data", "plugins", controlID)

			writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "1.0.0", "index.js"), reloadHookScript(run, "v1", "data.n + 1"))
			writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
			h.loadPlugin(t, targetID)
			h.loadPlugin(t, controlID)

			testCase.mutate(t, targetDir, run, secret)

			admin := h.user("admin", "reload-admin")
			result := h.reload(t, targetID, h.token(admin))
			if result.panicked != nil {
				t.Fatalf("reload handler panicked: %v", result.panicked)
			}
			if result.code != http.StatusInternalServerError {
				t.Fatalf("reload status = %d, want 500; body = %s", result.code, result.body)
			}
			reloadForbiddenBody(t, result.body, secret)

			phase := run + "-after"
			h.callCalculate(phase, 5)
			h.logs.waitFor(t, "RLD-"+run+"-v1:"+phase+":6")
			h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":105")
			h.logs.requireAbsent(t, "RLD-"+run+"-candidate:"+phase)
		})
	}
}

func TestPluginReloadRejectsMismatchedManifestID(t *testing.T) {
	h := newPluginReloadHarness(t)
	run := nextPluginReloadRun()

	firstID := "reload-first"
	secondID := "reload-second"
	firstDir := filepath.Join("data", "plugins", firstID)
	secondDir := filepath.Join("data", "plugins", secondID)

	writeReloadPlugin(t, firstDir, reloadPluginMeta(firstID, "1.0.0", "index.js"), reloadHookScript(run, "first", "data.n + 1"))
	writeReloadPlugin(t, secondDir, reloadPluginMeta(secondID, "1.0.0", "index.js"), reloadHookScript(run, "second", "data.n + 100"))
	h.loadPlugin(t, firstID)
	h.loadPlugin(t, secondID)

	secret := "RELOAD_SECRET_" + run
	writeReloadPlugin(t, firstDir, reloadPluginMeta(secondID, "9.0.0", "index.js"), reloadCandidateScript(run, "candidate", "data.n * 10"))

	admin := h.user("admin", "reload-admin")
	result := h.reload(t, firstID, h.token(admin))
	if result.panicked != nil {
		t.Fatalf("reload handler panicked: %v", result.panicked)
	}
	if result.code != http.StatusInternalServerError {
		t.Fatalf("reload status = %d, want 500; body = %s", result.code, result.body)
	}
	reloadForbiddenBody(t, result.body, secret)

	phase := run + "-after"
	h.callCalculate(phase, 7)
	h.logs.waitFor(t, "RLD-"+run+"-first:"+phase+":8")
	h.logs.waitFor(t, "RLD-"+run+"-second:"+phase+":107")
	h.logs.requireAbsent(t, "RLD-"+run+"-entry-candidate")
	h.logs.requireAbsent(t, "RLD-"+run+"-boot-candidate")
	h.logs.requireAbsent(t, "RLD-"+run+"-candidate:"+phase)
}

func TestPluginReloadWithoutRuntimeReturns503(t *testing.T) {
	h := newAccessHarness(t)

	admin := h.user("admin", "reload-admin")
	result := h.request(http.MethodPost, "/api/admin/plugins/reload-missing/reload", "", h.token(admin), "application/json", "application/json")
	if result.panicked != nil {
		t.Fatalf("reload handler panicked: %v", result.panicked)
	}
	if result.code != http.StatusServiceUnavailable {
		t.Fatalf("reload status = %d, want 503; body = %s", result.code, result.body)
	}
}

func TestPluginReloadUnknownOrForeignPluginsReturn404(t *testing.T) {
	t.Run("valid unknown id", func(t *testing.T) {
		h := newPluginReloadHarness(t)
		run := nextPluginReloadRun()

		controlID := "reload-control"
		controlDir := filepath.Join("data", "plugins", controlID)
		writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
		h.loadPlugin(t, controlID)

		admin := h.user("admin", "reload-admin")
		result := h.reload(t, "reload-never-loaded", h.token(admin))
		if result.panicked != nil {
			t.Fatalf("reload handler panicked: %v", result.panicked)
		}
		if result.code != http.StatusNotFound {
			t.Fatalf("reload status = %d, want 404; body = %s", result.code, result.body)
		}
	})

	t.Run("on disk but not loaded", func(t *testing.T) {
		h := newPluginReloadHarness(t)
		run := nextPluginReloadRun()

		orphanID := "reload-orphan"
		controlID := "reload-control"
		orphanDir := filepath.Join("data", "plugins", orphanID)
		controlDir := filepath.Join("data", "plugins", controlID)

		writeReloadPlugin(t, orphanDir, reloadPluginMeta(orphanID, "1.0.0", "index.js"), reloadCandidateScript(run, "orphan", "data.n + 1"))
		writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
		h.loadPlugin(t, controlID)

		admin := h.user("admin", "reload-admin")
		result := h.reload(t, orphanID, h.token(admin))
		if result.panicked != nil {
			t.Fatalf("reload handler panicked: %v", result.panicked)
		}
		if result.code != http.StatusNotFound {
			t.Fatalf("reload status = %d, want 404; body = %s", result.code, result.body)
		}

		phase := run + "-after"
		h.callCalculate(phase, 2)
		h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":102")
		h.logs.requireAbsent(t, "RLD-"+run+"-entry-orphan")
		h.logs.requireAbsent(t, "RLD-"+run+"-boot-orphan")
		h.logs.requireAbsent(t, "RLD-"+run+"-orphan:"+phase)
	})

	t.Run("plugin of another runtime", func(t *testing.T) {
		h := newPluginReloadHarness(t)
		run := nextPluginReloadRun()

		foreignID := "reload-foreign"
		controlID := "reload-control"
		controlDir := filepath.Join("data", "plugins", controlID)
		writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
		h.loadPlugin(t, controlID)

		foreignRuntime, err := plugin.NewRuntime()
		if err != nil {
			t.Fatalf("create second plugin runtime: %v", err)
		}
		t.Cleanup(foreignRuntime.Close)
		foreignDir := filepath.Join(t.TempDir(), foreignID)
		writeReloadPlugin(t, foreignDir, reloadPluginMeta(foreignID, "1.0.0", "index.js"), reloadHookScript(run, "foreign", "data.n + 1000"))
		if err := foreignRuntime.Load(context.Background(), foreignDir); err != nil {
			t.Fatalf("load plugin in second runtime: %v", err)
		}

		stolenDir := filepath.Join("data", "plugins", foreignID)
		writeReloadPlugin(t, stolenDir, reloadPluginMeta(foreignID, "9.0.0", "index.js"), reloadCandidateScript(run, "stolen", "data.n + 2000"))

		admin := h.user("admin", "reload-admin")
		result := h.reload(t, foreignID, h.token(admin))
		if result.panicked != nil {
			t.Fatalf("reload handler panicked: %v", result.panicked)
		}
		if result.code != http.StatusNotFound {
			t.Fatalf("reload status = %d, want 404; body = %s", result.code, result.body)
		}

		foreignPhase := run + "-foreign"
		foreignRuntime.CallHook(plugin.Hook("OnCalculate"), map[string]interface{}{"phase": foreignPhase, "n": 1})
		h.logs.waitFor(t, "RLD-"+run+"-foreign:"+foreignPhase+":1001")

		phase := run + "-after"
		h.callCalculate(phase, 2)
		h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":102")
		h.logs.requireAbsent(t, "RLD-"+run+"-entry-stolen")
		h.logs.requireAbsent(t, "RLD-"+run+"-stolen:"+phase)
	})

	t.Run("encoded path escape", func(t *testing.T) {
		h := newPluginReloadHarness(t)
		run := nextPluginReloadRun()

		controlID := "reload-control"
		controlDir := filepath.Join("data", "plugins", controlID)
		writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
		h.loadPlugin(t, controlID)

		stolenID := "reload-escape"
		stolenDir := filepath.Join("data", "plugins", stolenID)
		writeReloadPlugin(t, stolenDir, reloadPluginMeta(stolenID, "9.0.0", "index.js"), reloadCandidateScript(run, "stolen", "data.n + 2000"))

		admin := h.user("admin", "reload-admin")
		token := h.token(admin)
		for _, path := range []string{
			"/api/admin/plugins/%2e%2e%2Freload-escape/reload",
			"/api/admin/plugins/..%2Freload-escape/reload",
			"/api/admin/plugins/reload-control%2F..%2Freload-escape/reload",
		} {
			result := h.request(http.MethodPost, path, "", token, "application/json", "application/json")
			if result.panicked != nil {
				t.Fatalf("reload handler panicked for %s: %v", path, result.panicked)
			}
			if result.code != http.StatusNotFound {
				t.Fatalf("reload status for %s = %d, want 404; body = %s", path, result.code, result.body)
			}
		}

		phase := run + "-after"
		h.callCalculate(phase, 2)
		h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":102")
		h.logs.requireAbsent(t, "RLD-"+run+"-entry-stolen")
		h.logs.requireAbsent(t, "RLD-"+run+"-stolen:"+phase)
	})
}

func TestPluginReloadRequiresAdminAndLeavesVersionsAlone(t *testing.T) {
	cases := []struct {
		name  string
		token func(t *testing.T, h *reloadHarness) string
		code  int
	}{
		{
			name: "anonymous",
			token: func(t *testing.T, h *reloadHarness) string {
				return ""
			},
			code: http.StatusUnauthorized,
		},
		{
			name: "editor",
			token: func(t *testing.T, h *reloadHarness) string {
				return h.token(h.user("editor", "reload-editor"))
			},
			code: http.StatusForbidden,
		},
		{
			name: "author",
			token: func(t *testing.T, h *reloadHarness) string {
				return h.token(h.user("author", "reload-author"))
			},
			code: http.StatusForbidden,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newPluginReloadHarness(t)
			run := nextPluginReloadRun()

			targetID := "reload-target"
			controlID := "reload-control"
			targetDir := filepath.Join("data", "plugins", targetID)
			controlDir := filepath.Join("data", "plugins", controlID)

			writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "1.0.0", "index.js"), reloadHookScript(run, "v1", "data.n + 1"))
			writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
			h.loadPlugin(t, targetID)
			h.loadPlugin(t, controlID)

			writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "2.0.0", "index.js"), reloadCandidateScript(run, "candidate", "data.n * 10"))

			result := h.reload(t, targetID, testCase.token(t, h))
			if result.panicked != nil {
				t.Fatalf("reload handler panicked: %v", result.panicked)
			}
			if result.code != testCase.code {
				t.Fatalf("reload status = %d, want %d; body = %s", result.code, testCase.code, result.body)
			}
			for _, fragment := range []string{"RLD-" + run, "data/plugins", "RELOAD_SECRET"} {
				if strings.Contains(result.body, fragment) {
					t.Fatalf("unauthorised response leaked %q: %s", fragment, result.body)
				}
			}

			phase := run + "-after"
			h.callCalculate(phase, 2)
			h.logs.waitFor(t, "RLD-"+run+"-v1:"+phase+":3")
			h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":102")
			h.logs.requireAbsent(t, "RLD-"+run+"-entry-candidate")
			h.logs.requireAbsent(t, "RLD-"+run+"-boot-candidate")
			h.logs.requireAbsent(t, "RLD-"+run+"-candidate:"+phase)
		})
	}
}

func TestPluginReloadCanceledRequestKeepsTheOldVersion(t *testing.T) {
	h := newPluginReloadHarness(t)
	run := nextPluginReloadRun()

	targetID := "reload-canceled"
	controlID := "reload-control"
	targetDir := filepath.Join("data", "plugins", targetID)
	controlDir := filepath.Join("data", "plugins", controlID)

	writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "1.0.0", "index.js"), reloadHookScript(run, "v1", "data.n + 1"))
	writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
	h.loadPlugin(t, targetID)
	h.loadPlugin(t, controlID)

	candidateBoot := "RLD-" + run + "-boot-candidate"
	writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "2.0.0", "index.js"), reloadHangingBootScript(run, "candidate", 10))

	admin := h.user("admin", "reload-admin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	request := httptest.NewRequest(http.MethodPost, "/api/admin/plugins/"+targetID+"/reload", nil)
	setAccessToken(request, h.token(admin))
	request.Header.Set("X-Jelly-CSRF", "1")
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request = request.WithContext(ctx)

	recorder := httptest.NewRecorder()
	result := make(chan any, 1)
	go func() {
		defer func() { result <- recover() }()
		h.echo.ServeHTTP(recorder, request)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !h.logs.contains(candidateBoot) {
		select {
		case panicked := <-result:
			if panicked != nil {
				t.Fatalf("reload handler panicked: %v", panicked)
			}
			t.Fatalf("reload finished before the candidate boot: status = %d; body = %s", recorder.Code, recorder.Body.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("candidate boot did not start; logs:\n%s", h.logs.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case panicked := <-result:
		if panicked != nil {
			t.Fatalf("reload handler panicked: %v", panicked)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("canceled reload did not return; logs:\n%s", h.logs.snapshot())
	}
	if recorder.Code >= 200 && recorder.Code < 300 {
		t.Fatalf("canceled reload status = %d, want a failure; body = %s", recorder.Code, recorder.Body.String())
	}

	phase := run + "-after"
	h.callCalculate(phase, 3)
	h.logs.waitFor(t, "RLD-"+run+"-v1:"+phase+":4")
	h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":103")
	h.logs.requireAbsent(t, "RLD-"+run+"-candidate:"+phase)
}

func TestPluginReloadTimeoutKeepsThePreviousVersion(t *testing.T) {
	h := newPluginReloadHarness(t)
	run := nextPluginReloadRun()

	targetID := "reload-timeout"
	controlID := "reload-control"
	targetDir := filepath.Join("data", "plugins", targetID)
	controlDir := filepath.Join("data", "plugins", controlID)

	writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "1.0.0", "index.js"), reloadHookScript(run, "v1", "data.n + 1"))
	writeReloadPlugin(t, controlDir, reloadPluginMeta(controlID, "1.0.0", "index.js"), reloadHookScript(run, "control", "data.n + 100"))
	h.loadPlugin(t, targetID)
	h.loadPlugin(t, controlID)

	candidateBoot := "RLD-" + run + "-boot-candidate"
	writeReloadPlugin(t, targetDir, reloadPluginMeta(targetID, "2.0.0", "index.js"), reloadHangingBootScript(run, "candidate", 10))

	admin := h.user("admin", "reload-admin")
	token := h.token(admin)

	resultCh := make(chan accessResult, 1)
	go func() {
		resultCh <- h.reload(t, targetID, token)
	}()

	var result accessResult
	select {
	case result = <-resultCh:
	case <-time.After(6 * time.Second):
		t.Fatalf("reload with a hanging OnBoot did not return within 6s; logs:\n%s", h.logs.snapshot())
	}

	if result.panicked != nil {
		t.Fatalf("reload handler panicked: %v", result.panicked)
	}
	if result.code != http.StatusInternalServerError {
		t.Fatalf("reload status = %d, want 500; body = %s", result.code, result.body)
	}
	reloadForbiddenBody(t, result.body, "RELOAD_SECRET_"+run)
	if strings.Contains(result.body, "RLD-"+run) {
		t.Fatalf("timeout response leaked candidate output: %s", result.body)
	}

	h.logs.waitFor(t, candidateBoot)

	phase := run + "-after"
	h.callCalculate(phase, 6)
	h.logs.waitFor(t, "RLD-"+run+"-v1:"+phase+":7")
	h.logs.waitFor(t, "RLD-"+run+"-control:"+phase+":106")
	h.logs.requireAbsent(t, "RLD-"+run+"-candidate:"+phase)
}
