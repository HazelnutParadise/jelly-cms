// Contract tests for the shipped extension points (ticket 29, PR B, B1).
//
// B1 已由骨架完成，本檔鎖住契約：ext 的出貨表與 ShippedPoints、registerExtension
// 的出貨與宣告檢查、HasExtension／CallExtension 的可用性判斷，以及
// Runtime.ExtensionCalls 的計數。這些測試在目前骨架上應直接通過。
//
// 若有人把 internal/ext/ext.go 的 shipped 表改回只有兩個點（search.engine、
// media.storage），TestExtensionPointsShippedSixPointsLoadAndCall 會失敗：
// Load 時 theme.inject 的 registerExtension 回 "is not shipped"，且
// ShippedPoints 的長度與順序斷言不符。
//
// 控制案例（正向對照，用來證明負向斷言不是空格子上的假通過）：
//   - TestExtensionPointsShippedSixPointsLoadAndCall：HasExtension 對六點皆為 true。
//   - TestExtensionPointsUndeclaredPointRejected 的 "declared point keeps its callback"：
//     同一種宣告方式下，有回呼的 search.engine 仍為 true。
//   - TestExtensionPointsCallCounter：最後一次成功呼叫會讓計數增加。
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/ext"
)

// extensionPointsSix is the shipped list the ticket names, in ShippedPoints
// (name) order.
var extensionPointsSix = []string{
	"comments.component",
	"content.render",
	"media.storage",
	"notify.channel",
	"search.engine",
	"theme.inject",
}

func newExtensionPointsRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func extensionPointsSpecs(points ...string) []ExtensionSpec {
	specs := make([]ExtensionSpec, 0, len(points))
	for _, point := range points {
		specs = append(specs, ExtensionSpec{Point: point})
	}
	return specs
}

func extensionPointsCaller(t *testing.T, runtime *Runtime) extensionCaller {
	t.Helper()
	caller, ok := any(runtime).(extensionCaller)
	if !ok {
		t.Fatal("Runtime does not implement CallExtension(context.Context, string, string, json.RawMessage)")
	}
	return caller
}

func extensionPointsPresence(t *testing.T, runtime *Runtime) extensionPresence {
	t.Helper()
	presence, ok := any(runtime).(extensionPresence)
	if !ok {
		t.Fatal("Runtime does not implement HasExtension(string, string)")
	}
	return presence
}

func extensionPointsCall(t *testing.T, caller extensionCaller, pluginID, point string, input json.RawMessage) (json.RawMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return caller.CallExtension(ctx, pluginID, point, input)
}

func TestExtensionPointsShippedSixPointsLoadAndCall(t *testing.T) {
	if got := ext.ShippedPoints(); !equalExtensionPointLists(got, extensionPointsSix) {
		t.Fatalf("ext.ShippedPoints() = %v, want the six shipped points %v", got, extensionPointsSix)
	}

	runtime := newExtensionPointsRuntime(t)
	pluginDir, meta := writeExtensionTestPlugin(t, extensionPointsSpecs(extensionPointsSix...), `
JellyCMS.registerExtension("search.engine", function (input) {
  return {ids: input.ids, provider: "six"};
});
JellyCMS.registerExtension("media.storage", function (input) {
  return {operation: input.operation};
});
JellyCMS.registerExtension("theme.inject", function (input) {
  return {head: "<meta name=\"x\">", footer: "<script></script>"};
});
JellyCMS.registerExtension("content.render", function (input) {
  return {html: input.html.split("<p>").join("<p class=\"x\">")};
});
JellyCMS.registerExtension("comments.component", function (input) {
  return {html: "<div id=\"ext-comments\"></div>"};
});
JellyCMS.registerExtension("notify.channel", function (input) {
  return {delivered: true};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() with six shipped extension callbacks: %v", err)
	}

	presence := extensionPointsPresence(t, runtime)
	for _, point := range extensionPointsSix {
		if !presence.HasExtension(meta.ID, point) {
			t.Errorf("HasExtension(%s, %s) = false, want true for a registered shipped point", meta.ID, point)
		}
	}
	if presence.HasExtension("no-such-plugin", "theme.inject") {
		t.Error("HasExtension(unloaded plugin, theme.inject) = true, want false")
	}

	caller := extensionPointsCaller(t, runtime)

	injectJSON, err := extensionPointsCall(t, caller, meta.ID, "theme.inject", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallExtension(theme.inject): %v", err)
	}
	var inject struct {
		Head   string `json:"head"`
		Footer string `json:"footer"`
	}
	if err := json.Unmarshal(injectJSON, &inject); err != nil {
		t.Fatalf("decode theme.inject result %s: %v", injectJSON, err)
	}
	if want := `<meta name="x">`; inject.Head != want {
		t.Errorf("theme.inject head = %q, want %q (result %s)", inject.Head, want, injectJSON)
	}
	if want := `<script></script>`; inject.Footer != want {
		t.Errorf("theme.inject footer = %q, want %q (result %s)", inject.Footer, want, injectJSON)
	}

	renderInput := json.RawMessage(`{"post":{"id":7,"type":"post","slug":"hello","title":"Hi","author_id":1},"html":"<p>a</p><p>b</p>"}`)
	renderJSON, err := extensionPointsCall(t, caller, meta.ID, "content.render", renderInput)
	if err != nil {
		t.Fatalf("CallExtension(content.render): %v", err)
	}
	var render struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(renderJSON, &render); err != nil {
		t.Fatalf("decode content.render result %s: %v", renderJSON, err)
	}
	if want := `<p class="x">a</p><p class="x">b</p>`; render.HTML != want {
		t.Errorf("content.render html = %q, want %q (result %s)", render.HTML, want, renderJSON)
	}
}

func TestExtensionPointsUndeclaredPointRejected(t *testing.T) {
	// media.processor is declared in ext (reserved, not shipped). A manifest
	// may name it, but registerExtension must refuse it.
	t.Run("registration of declared but not shipped point fails", func(t *testing.T) {
		runtime := newExtensionPointsRuntime(t)
		pluginDir, meta := writeExtensionTestPlugin(t, extensionPointsSpecs("media.processor"), `
JellyCMS.registerExtension("media.processor", function (input) {
  return {ok: true};
});
`)
		err := runtime.Load(context.Background(), pluginDir)
		if err == nil {
			t.Fatal("Load() accepted registerExtension(media.processor) for a point that is not shipped")
		}
		if !strings.Contains(err.Error(), "is not shipped") {
			t.Errorf("Load() error = %q, want it to contain %q", err.Error(), "is not shipped")
		}
		if got := extensionPointsPresence(t, runtime).HasExtension(meta.ID, "media.processor"); got {
			t.Error("HasExtension(media.processor) = true, want false for a non-shipped point")
		}
	})

	// Control: the same declaration style with a shipped point whose callback
	// is registered keeps working, so the false above is not an empty-skeleton
	// artefact.
	t.Run("declared point keeps its callback", func(t *testing.T) {
		runtime := newExtensionPointsRuntime(t)
		pluginDir, meta := writeExtensionTestPlugin(t, extensionPointsSpecs("search.engine", "media.processor"), `
JellyCMS.registerExtension("search.engine", function (input) {
  return {ids: input.ids};
});
`)
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Fatalf("Load() with registered search.engine and declared media.processor: %v", err)
		}
		presence := extensionPointsPresence(t, runtime)
		if !presence.HasExtension(meta.ID, "search.engine") {
			t.Error("HasExtension(search.engine) = false, want true for a registered shipped point")
		}
		if presence.HasExtension(meta.ID, "media.processor") {
			t.Error("HasExtension(media.processor) = true, want false when no callback is registered")
		}
	})
}

func TestExtensionPointsCallCounter(t *testing.T) {
	runtime := newExtensionPointsRuntime(t)
	pluginDir, meta := writeExtensionTestPlugin(t, extensionPointsSpecs("theme.inject", "notify.channel"), `
JellyCMS.registerExtension("theme.inject", function (input) {
  return {head: "<meta name=\"c\">", footer: ""};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got := runtime.ExtensionCalls(); got != 0 {
		t.Fatalf("ExtensionCalls() after Load() = %d, want 0", got)
	}
	caller := extensionPointsCaller(t, runtime)

	// Positive: each successful call increments the counter by one.
	for i := 1; i <= 2; i++ {
		if _, err := extensionPointsCall(t, caller, meta.ID, "theme.inject", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("CallExtension(theme.inject) #%d: %v", i, err)
		}
		if got := runtime.ExtensionCalls(); got != uint64(i) {
			t.Errorf("ExtensionCalls() after %d successful call(s) = %d, want %d", i, got, i)
		}
	}

	// Negative: a declared point without a callback is unavailable and is not
	// sent to the worker, so the counter stays put.
	_, err := extensionPointsCall(t, caller, meta.ID, "notify.channel", json.RawMessage(`{}`))
	if !errors.Is(err, ErrExtensionUnavailable) {
		t.Errorf("CallExtension(notify.channel without callback) error = %v, want ErrExtensionUnavailable", err)
	}
	if got := runtime.ExtensionCalls(); got != 2 {
		t.Errorf("ExtensionCalls() after unavailable call = %d, want 2", got)
	}

	// Negative: a plugin that is not loaded is not counted.
	_, err = extensionPointsCall(t, caller, "no-such-plugin", "theme.inject", json.RawMessage(`{}`))
	if !errors.Is(err, ErrPluginNotLoaded) {
		t.Errorf("CallExtension(unloaded plugin) error = %v, want ErrPluginNotLoaded", err)
	}
	if got := runtime.ExtensionCalls(); got != 2 {
		t.Errorf("ExtensionCalls() after unloaded-plugin call = %d, want 2", got)
	}

	// Positive control after the negatives: the counter still moves on a
	// successful call.
	if _, err := extensionPointsCall(t, caller, meta.ID, "theme.inject", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("CallExtension(theme.inject) after negatives: %v", err)
	}
	if got := runtime.ExtensionCalls(); got != 3 {
		t.Errorf("ExtensionCalls() after final successful call = %d, want 3", got)
	}
}

func TestExtensionPointsReplacementOnNotifyChannel(t *testing.T) {
	runtime := newExtensionPointsRuntime(t)
	pluginDir, meta := writeExtensionTestPlugin(t, extensionPointsSpecs("notify.channel"), `
JellyCMS.registerExtension("notify.channel", function (input) {
  return {provider: "first"};
});
JellyCMS.registerExtension("notify.channel", function (input) {
  return {provider: "replacement"};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() with two notify.channel registrations: %v", err)
	}
	caller := extensionPointsCaller(t, runtime)

	before := runtime.ExtensionCalls()
	result, err := extensionPointsCall(t, caller, meta.ID, "notify.channel", json.RawMessage(`{"kind":"comment.pending"}`))
	if err != nil {
		t.Fatalf("CallExtension(notify.channel): %v", err)
	}
	var got struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("decode notify.channel result %s: %v", result, err)
	}
	if got.Provider != "replacement" {
		t.Errorf("notify.channel provider = %q, want %q (the second registration replaces the first)", got.Provider, "replacement")
	}
	if after := runtime.ExtensionCalls(); after != before+1 {
		t.Errorf("ExtensionCalls() delta = %d, want 1", after-before)
	}
}

func equalExtensionPointLists(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
