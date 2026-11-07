package server

// Review follow-ups for the site extension points (ticket 29, PR B): a
// cancelled request must not take the plugin worker down, the 1 MiB render
// input limit is measured on what is actually sent, an unavailable provider
// in the render chain is skipped rather than failing the chain, and a post
// with comments switched off shows no comments component at all.
//
// Control case: TestPluginExtensionsSiteFixCommentsOffHidesComponent (the
// gate lives in handlers_public.go, already in place). The other three fail
// on the implementation before the fixes.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/ext"
)

func pluginExtFixRenderA(t *testing.T, h *pluginEventsHarness) {
	t.Helper()
	pluginExtInstall(t, h, "render-a", []string{ext.PointContentRender}, `JellyCMS.registerExtension("content.render", function (input) {
  return {html: input.html.split("<p>").join('<p class="x">')};
});`)
}

// pluginExtFixBusy returns a callback body that burns about ms milliseconds
// before returning value, so a request can be cancelled while the plugin runs.
func pluginExtFixBusy(ms int, value string) string {
	return `var end = Date.now() + ` + strconv.Itoa(ms) + `; while (Date.now() < end) {} return ` + value + `;`
}

// TestPluginExtensionsSiteFixCancelledRequestKeepsWorkerAlive calls the three
// site cells with an already cancelled context. Whatever they return, the
// plugin must still be live afterwards and the next request must render.
func TestPluginExtensionsSiteFixCancelledRequestKeepsWorkerAlive(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	// Every callback burns 300 ms, so a 50 ms request deadline expires while
	// the plugin is still running.
	pluginExtInstall(t, h, "render-a", []string{ext.PointContentRender}, `JellyCMS.registerExtension("content.render", function (input) {
  `+pluginExtFixBusy(300, `{html: input.html.split("<p>").join('<p class="x">')}`)+`
});`)
	pluginExtInstall(t, h, "inject-a", []string{ext.PointThemeInject}, `JellyCMS.registerExtension("theme.inject", function (input) {
  `+pluginExtFixBusy(300, `{head: '<meta name="inject-a">', footer: "<!--inject-a-footer-->"}`)+`
});`)
	pluginExtInstall(t, h, "comments-ext", []string{ext.PointCommentsComponent}, `JellyCMS.registerExtension("comments.component", function (input) {
  `+pluginExtFixBusy(300, `{html: '<div id="ext-comments"></div>'}`)+`
});`)
	token := h.token(h.user("admin", "fix-cancel-admin"))
	pluginExtPutExtension(t, h, token, ext.PointCommentsComponent, `{"active":["comments-ext"]}`)
	author := h.user("author", "fix-cancel-author")
	post := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Fix Cancel", slug: "fix-cancel", content: "<p>hello</p>"})

	short := func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}
	_ = pluginRenderContent(short(), h.db, post, "<p>hello</p>")
	_ = pluginThemeInjectionFor(short(), h.db)
	_, _ = pluginCommentsComponent(short(), h.db, post)

	for _, id := range []string{"render-a", "inject-a", "comments-ext"} {
		point := map[string]string{"render-a": ext.PointContentRender, "inject-a": ext.PointThemeInject, "comments-ext": ext.PointCommentsComponent}[id]
		if !h.runtime.HasExtension(id, point) {
			t.Errorf("after a cancelled request, HasExtension(%s, %s) = false: the worker was terminated", id, point)
		}
	}
	// Positive control: the next real request renders through all three.
	body := pluginExtPage(t, h, "/post/fix-cancel")
	pluginExtWantContains(t, "GET /post/fix-cancel after cancelled request", body, `<p class="x">hello</p>`)
	pluginExtWantContains(t, "GET /post/fix-cancel after cancelled request", body, pluginExtInjectHead("inject-a"))
	pluginExtWantContains(t, "GET /post/fix-cancel after cancelled request", body, `<div id="ext-comments"></div>`)
}

// TestPluginExtensionsSiteFixRenderLimitMeasuresEncodedInput sends a 900,000
// byte article made of tags: the raw HTML is under 1 MiB and so is the JSON
// input when HTML is not escaped, so the plugin must receive it. The provider
// keeps the size (the runtime caps a plugin's result at 1 MiB too).
func TestPluginExtensionsSiteFixRenderLimitMeasuresEncodedInput(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstall(t, h, "render-a", []string{ext.PointContentRender}, `JellyCMS.registerExtension("content.render", function (input) {
  return {html: input.html.split("<p>").join("<b>")};
});`)
	author := h.user("author", "fix-limit-author")
	big := strings.Repeat("<p>a</p>", 900000/len("<p>a</p>"))
	post := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Fix Limit", slug: "fix-limit", content: big})

	before := h.runtime.ExtensionCalls()
	out := pluginRenderContent(context.Background(), h.db, post, big)
	if h.runtime.ExtensionCalls() == before {
		t.Fatalf("a %d byte article was not sent to the render provider (counter unchanged); the limit must be measured on the encoded input without HTML escaping", len(big))
	}
	if !strings.HasPrefix(out, `<b>a</p>`) {
		t.Errorf("rendered output does not start with the provider's change: %q", pluginExtClip(out))
	}

	drainPluginLogPipelines(t)
	if n := pluginExtLogCount(t, h, "render-a", pluginExtLevelError); n != 0 {
		t.Errorf("system_logs error rows for render-a = %d, want 0: %v", n, pluginExtLogMessages(t, h, "render-a"))
	}
	// Control: an article over the limit is used as is, without a call or an error log.
	over := strings.Repeat("<p>a</p>", (1<<20)/len("<p>a</p>")+16)
	before = h.runtime.ExtensionCalls()
	if got := pluginRenderContent(context.Background(), h.db, post, over); got != over {
		t.Errorf("over-limit article was changed (len %d -> %d), want it unchanged", len(over), len(got))
	}
	if h.runtime.ExtensionCalls() != before {
		t.Errorf("over-limit article was sent to the provider, want no call")
	}
	drainPluginLogPipelines(t)
	if n := pluginExtLogCount(t, h, "render-a", pluginExtLevelError); n != 0 {
		t.Errorf("system_logs error rows for render-a after over-limit article = %d, want 0", n)
	}
}

// TestPluginExtensionsSiteFixRenderChainSkipsUnavailableProvider lists two
// providers, unloads one without bumping the generation (the provider list is
// still cached), then renders new content: the live provider's change must be
// on the page.
func TestPluginExtensionsSiteFixRenderChainSkipsUnavailableProvider(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtFixRenderA(t, h)
	pluginExtInstall(t, h, "render-gone", []string{ext.PointContentRender},
		`JellyCMS.registerExtension("content.render", function (input) { return {html: input.html + "<p data-render=\"gone\"></p>"}; });`)
	author := h.user("author", "fix-gone-author")
	post := pubThemeSeedPost(t, h.accessHarness, author, pubThemePost{title: "Fix Gone", slug: "fix-gone", content: "<p>one</p>"})

	first := pluginExtPage(t, h, "/post/fix-gone")
	pluginExtWantContains(t, "GET /post/fix-gone with both providers", first, `<p class="x">one</p>`)
	pluginExtWantContains(t, "GET /post/fix-gone with both providers", first, `data-render="gone"`)

	// The provider list (cached for 5 seconds) still names render-gone.
	if err := h.runtime.Unload(context.Background(), "render-gone"); err != nil {
		t.Fatalf("unload render-gone: %v", err)
	}
	if err := h.db.Exec(`UPDATE posts SET content = ? WHERE id = ?`, "<p>two</p>", post.ID).Error; err != nil {
		t.Fatalf("update post: %v", err)
	}
	second := pluginExtPage(t, h, "/post/fix-gone")
	pluginExtWantContains(t, "GET /post/fix-gone after render-gone left", second, `<p class="x">two</p>`)
	pluginExtWantAbsent(t, "GET /post/fix-gone after render-gone left", second, `data-render="gone"`)
	drainPluginLogPipelines(t)
	if n := pluginExtLogCount(t, h, "render-a", pluginExtLevelError); n != 0 {
		t.Errorf("system_logs error rows for the live provider render-a = %d, want 0", n)
	}
}

// TestPluginExtensionsSiteFixCommentsOffHidesComponent (control): a post with
// comments switched off shows neither the plugin component nor the builtin
// block, while a post with comments on shows the component.
func TestPluginExtensionsSiteFixCommentsOffHidesComponent(t *testing.T) {
	h := newPluginEventsHarness(t)
	pluginExtUseTheme(t, h)
	pluginExtInstall(t, h, "comments-ext", []string{ext.PointCommentsComponent},
		`JellyCMS.registerExtension("comments.component", function (input) { return {html: "<div id=\"ext-comments\">" + input.post.slug + "</div>"}; });`)
	token := h.token(h.user("admin", "fix-comments-admin"))
	pluginExtPutExtension(t, h, token, ext.PointCommentsComponent, `{"active":["comments-ext"]}`)
	author := h.user("author", "fix-comments-author")
	off := false
	commentsPublishedPost(t, h.accessHarness, author, "fix-comments-off", func(p *core.Post) { p.AllowComments = &off })
	commentsPublishedPost(t, h.accessHarness, author, "fix-comments-on", nil)

	closed := pluginExtPage(t, h, "/post/fix-comments-off")
	pluginExtWantAbsent(t, "GET /post/fix-comments-off", closed, `<div id="ext-comments">`)
	pluginExtWantAbsent(t, "GET /post/fix-comments-off", closed, "comments-head")
	open := pluginExtPage(t, h, "/post/fix-comments-on")
	pluginExtWantContains(t, "GET /post/fix-comments-on", open, `<div id="ext-comments">fix-comments-on</div>`)
}

// pluginExtLogMessages lists the plugin's system_logs messages, for failure output.
func pluginExtLogMessages(t *testing.T, h *pluginEventsHarness, pluginID string) []string {
	t.Helper()
	var messages []string
	if err := h.db.Raw(`SELECT message FROM system_logs WHERE source = 'plugin' AND plugin_id = ? ORDER BY id`, pluginID).Scan(&messages).Error; err != nil {
		t.Fatalf("read plugin %s logs: %v", pluginID, err)
	}
	return messages
}
