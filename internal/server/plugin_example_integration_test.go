package server

// Ticket 36 R6 guard (ticket 36): the sample plugin
// examples/plugins/hello-jelly must keep loading and behaving as its README
// promises on the real plugin runtime. The test copies the sample's plugin.json
// and index.js into the harness data directory, enables it through the admin
// API and checks the footer, escaping, the OnPostPublish counter and the stats
// action.
//
// Needs JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container. Every
// wait polls with a bounded loop.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
)

const (
	hjExampleID     = "hello-jelly"
	hjExampleFooter = `<div class="hello-jelly-banner">Hello from Jelly</div>`
	// hjExampleBudget bounds each wait for the runtime to reach the expected state.
	hjExampleBudget = 10 * time.Second
)

// hjExampleCopy copies the sample's manifest and entry script from the repository into the harness data
// directory. plgSeed writes plugin.json and the plugins row, hostWriteEntry writes index.js, and
// setGrantedPermissions records the approved permission hash the enable path checks.
func hjExampleCopy(t *testing.T, h *reloadHarness) {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "examples", "plugins", hjExampleID)
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		t.Fatalf("read sample plugin.json: %v", err)
	}
	entry, err := os.ReadFile(filepath.Join(dir, "index.js"))
	if err != nil {
		t.Fatalf("read sample index.js: %v", err)
	}
	plgSeed(t, h.accessHarness, hjExampleID, string(manifest), false)
	hostWriteEntry(t, hjExampleID, string(entry))
	setGrantedPermissions(t, h.accessHarness, hjExampleID)
}

// hjExampleWaitRunning polls the admin detail endpoint until the plugin reports state running.
func hjExampleWaitRunning(t *testing.T, h *reloadHarness, token string) {
	t.Helper()

	deadline := time.Now().Add(hjExampleBudget)
	var last string
	for {
		r := updCall(h.accessHarness, http.MethodGet, "/api/admin/plugins/"+hjExampleID, "", token)
		var detail struct {
			State     string `json:"state"`
			LastError string `json:"last_error"`
		}
		if r.code == http.StatusOK && json.Unmarshal([]byte(r.body), &detail) == nil {
			if detail.State == "running" {
				return
			}
			last = detail.State + " " + detail.LastError
		} else {
			last = "status " + http.StatusText(r.code)
		}
		if time.Now().After(deadline) {
			t.Fatalf("sample plugin did not reach state running within %s; last seen: %s", hjExampleBudget, last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hjExampleHome fetches the public home page and requires 200.
func hjExampleHome(t *testing.T, h *reloadHarness) string {
	t.Helper()

	r := h.request(http.MethodGet, "/", "", "", echo.MIMETextHTML, "")
	h.mustNoPanic(r, "GET /")
	if r.code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", r.code)
	}
	return r.body
}

// hjExampleWaitStored polls plugin_data until the sample's published counter holds want.
func hjExampleWaitStored(t *testing.T, h *reloadHarness, collection, key, want string) {
	t.Helper()

	deadline := time.Now().Add(hjExampleBudget)
	var values []string
	for {
		values = values[:0]
		if err := h.db.Raw(`SELECT value::text FROM plugin_data WHERE plugin_id = ? AND collection = ? AND key = ?`, hjExampleID, collection, key).Scan(&values).Error; err != nil {
			t.Fatalf("read plugin_data %s/%s: %v", collection, key, err)
		}
		if len(values) == 1 && values[0] == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin_data %s/%s = %v, want %s within %s", collection, key, values, want, hjExampleBudget)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPluginExampleHelloJellyRunsOnRealRuntime enables the repository sample on the real runtime and checks
// the five behaviors the sample promises: running state, footer output, escaped settings, the OnPostPublish
// counter and the stats action.
func TestPluginExampleHelloJellyRunsOnRealRuntime(t *testing.T) {
	h := newPluginReloadHarness(t)
	tm := theme.NewManager("web/themes")
	if err := tm.Activate("default"); err != nil {
		t.Fatalf("activate default theme: %v", err)
	}
	e := echo.New()
	RegisterRoutes(e, tm, h.runtime)
	h.echo = e

	hjExampleCopy(t, h)
	token := h.token(h.user("admin", "hello-jelly-example-admin"))

	enable := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+hjExampleID+"/enable", "", token)
	if enable.code != http.StatusOK {
		t.Fatalf("enable sample plugin = %d: %s", enable.code, enable.body)
	}

	// 1. The plugin reaches the running state.
	hjExampleWaitRunning(t, h, token)

	// 2. The public home page carries the default footer banner.
	if body := hjExampleHome(t, h); !strings.Contains(body, hjExampleFooter) {
		t.Fatalf("home page does not contain %q", hjExampleFooter)
	}

	// 3. A banner full of HTML special characters comes out escaped, never as live markup.
	const hostile = `<b>&"x`
	settings, err := json.Marshal(map[string]any{"values": map[string]string{"banner": hostile}})
	if err != nil {
		t.Fatalf("encode settings body: %v", err)
	}
	put := updCall(h.accessHarness, http.MethodPut, "/api/admin/plugins/"+hjExampleID+"/settings", string(settings), token)
	if put.code != http.StatusOK {
		t.Fatalf("PUT sample settings = %d: %s", put.code, put.body)
	}
	body := hjExampleHome(t, h)
	const escapedFooter = `<div class="hello-jelly-banner">&lt;b&gt;&amp;&quot;x</div>`
	if !strings.Contains(body, escapedFooter) {
		t.Fatalf("home page does not contain escaped banner %q after settings change", escapedFooter)
	}
	if strings.Contains(body, `<div class="hello-jelly-banner"><b>`) || strings.Contains(body, hostile) {
		t.Fatalf("home page contains unescaped banner markup after settings change")
	}

	// 4. Publishing a post fires OnPostPublish, which bumps the published counter to 1.
	post := h.request(http.MethodPost, "/api/admin/posts", `{"title":"Hello Jelly publish probe","slug":"hello-jelly-publish-probe","content":"<p>hello</p>","type":"post","status":"published"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(post, "publish sample post")
	if post.code < http.StatusOK || post.code >= http.StatusMultipleChoices {
		t.Fatalf("publish probe post = %d: %s", post.code, post.body)
	}
	hjExampleWaitStored(t, h, hjExampleID, "published", "1")

	// 5. The stats action is dispatched to the running plugin.
	act := updCall(h.accessHarness, http.MethodPost, "/api/admin/plugins/"+hjExampleID+"/actions/stats", "", token)
	if act.code != http.StatusOK {
		t.Fatalf("POST stats action = %d: %s", act.code, act.body)
	}
	var reply struct {
		Dispatched bool `json:"dispatched"`
	}
	if err := json.Unmarshal([]byte(act.body), &reply); err != nil || !reply.Dispatched {
		t.Fatalf("stats action reply = %s (err=%v), want dispatched true", act.body, err)
	}
}
