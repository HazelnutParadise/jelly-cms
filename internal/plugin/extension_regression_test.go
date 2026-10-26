package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type extensionCaller interface {
	CallExtension(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
}

type extensionPresence interface {
	HasExtension(string, string) bool
}

func TestExtensionRegressionCallsDeclaredJavaScriptProviders(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeExtensionTestPlugin(t, []ExtensionSpec{
		{Point: "search.engine"},
		{Point: "media.storage"},
	}, `
JellyCMS.registerHook("OnCalculate", function (data) {
  JellyCMS.log.info("extension-regression-old-hook:" + (data.n + 1));
});
JellyCMS.registerExtension("search.engine", function (input) {
  return {ids: ["obsolete"], provider: "first"};
});
JellyCMS.registerExtension("search.engine", function (input) {
  return {ids: input.ids.slice().reverse(), provider: "replacement"};
});
JellyCMS.registerExtension("media.storage", function (input) {
  return {operation: input.operation, path: input.path, bytes: input.bytes};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() with declared extension callbacks: %v", err)
	}

	caller, ok := any(runtime).(extensionCaller)
	if !ok {
		t.Fatal("Runtime does not implement CallExtension(context.Context, string, string, json.RawMessage)")
	}
	presence, ok := any(runtime).(extensionPresence)
	if !ok {
		t.Fatal("Runtime does not implement HasExtension(string, string)")
	}
	if !presence.HasExtension(meta.ID, "search.engine") || !presence.HasExtension(meta.ID, "media.storage") {
		t.Error("HasExtension() did not report callbacks registered for declared points")
	}
	if presence.HasExtension(meta.ID, "media.processor") {
		t.Error("HasExtension() reported a declared point without a callback")
	}
	callCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	searchInput := json.RawMessage(`{"ids":["post-3","post-1","post-2"],"limit":3}`)
	searchJSON, err := caller.CallExtension(callCtx, meta.ID, "search.engine", searchInput)
	if err != nil {
		t.Fatalf("CallExtension(search.engine): %v", err)
	}
	var searchResult struct {
		IDs      []string `json:"ids"`
		Provider string   `json:"provider"`
	}
	if err := json.Unmarshal(searchJSON, &searchResult); err != nil {
		t.Fatalf("decode search extension result %s: %v", searchJSON, err)
	}
	wantIDs := []string{"post-2", "post-1", "post-3"}
	if len(searchResult.IDs) != len(wantIDs) {
		t.Fatalf("search IDs = %v, want %v", searchResult.IDs, wantIDs)
	}
	for i, want := range wantIDs {
		if searchResult.IDs[i] != want {
			t.Errorf("search IDs = %v, want ordered IDs %v", searchResult.IDs, wantIDs)
			break
		}
	}
	if searchResult.Provider != "replacement" {
		t.Errorf("search provider = %q, want replacement callback", searchResult.Provider)
	}

	storageInput := json.RawMessage(`{"operation":"stat","path":"private/backups/a.json","bytes":17}`)
	storageJSON, err := caller.CallExtension(callCtx, meta.ID, "media.storage", storageInput)
	if err != nil {
		t.Fatalf("CallExtension(media.storage): %v", err)
	}
	var storageResult struct {
		Operation string `json:"operation"`
		Path      string `json:"path"`
		Bytes     int    `json:"bytes"`
	}
	if err := json.Unmarshal(storageJSON, &storageResult); err != nil {
		t.Fatalf("decode storage extension result %s: %v", storageJSON, err)
	}
	if storageResult.Operation != "stat" || storageResult.Path != "private/backups/a.json" || storageResult.Bytes != 17 {
		t.Errorf("storage extension result = %+v, want echoed operation, path, and bytes", storageResult)
	}

	if _, err := caller.CallExtension(callCtx, meta.ID, "media.processor", json.RawMessage(`{}`)); err == nil {
		t.Error("CallExtension() accepted a known but undeclared extension point")
	}
	if _, err := caller.CallExtension(callCtx, meta.ID, "not.a.real.point", json.RawMessage(`{}`)); err == nil {
		t.Error("CallExtension() accepted an unknown extension point")
	}
	if _, err := caller.CallExtension(callCtx, meta.ID, "search.engine", json.RawMessage(`{`)); err == nil {
		t.Error("CallExtension() accepted invalid JSON input")
	}
	oversizedInput := json.RawMessage(`"` + strings.Repeat("x", maxPayloadBytes) + `"`)
	if _, err := caller.CallExtension(callCtx, meta.ID, "search.engine", oversizedInput); err == nil {
		t.Error("CallExtension() accepted input larger than the plugin payload limit")
	}

	legacyOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnCalculate"), map[string]interface{}{"n": 41})
	})
	assertContains(t, legacyOutput, "extension-regression-old-hook:42")

	if err := runtime.Unload(context.Background(), meta.ID); err != nil {
		t.Fatalf("Unload(): %v", err)
	}
	if presence.HasExtension(meta.ID, "search.engine") {
		t.Error("HasExtension() remained true after Unload()")
	}
	if _, err := caller.CallExtension(callCtx, meta.ID, "search.engine", searchInput); err == nil {
		t.Error("CallExtension() reached a provider after Unload()")
	}
}

func TestExtensionRegressionDeclarationWithoutCallbackIsNotAvailable(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeExtensionTestPlugin(t, []ExtensionSpec{{Point: "search.engine"}}, `
JellyCMS.log.info("extension-regression-declared-without-callback");
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() of callbackless declaration: %v", err)
	}
	presence, ok := any(runtime).(extensionPresence)
	if !ok {
		t.Fatal("Runtime does not implement HasExtension(string, string)")
	}
	if presence.HasExtension(meta.ID, "search.engine") {
		t.Error("HasExtension() reported a manifest declaration without an actual callback")
	}
	if err := runtime.Unload(context.Background(), meta.ID); err != nil {
		t.Fatalf("Unload(): %v", err)
	}
	if presence.HasExtension(meta.ID, "search.engine") {
		t.Error("HasExtension() reported an extension after callbackless plugin unload")
	}
}

func TestExtensionRegressionRejectsUnserializableOversizedAndDeepResults(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeExtensionTestPlugin(t, []ExtensionSpec{{Point: "search.engine"}}, `
JellyCMS.registerExtension("search.engine", function (input) {
  if (input.mode === "unserializable") return function () {};
  if (input.mode === "oversized") return "x".repeat(1 << 20);
  if (input.mode === "too-deep") {
    var value = {};
    for (var i = 0; i < 65; i++) value = {nested: value};
    return value;
  }
  return {ok: true};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() with result-limit extension: %v", err)
	}
	caller, ok := any(runtime).(extensionCaller)
	if !ok {
		t.Fatal("Runtime does not implement CallExtension(context.Context, string, string, json.RawMessage)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for _, mode := range []string{"unserializable", "oversized", "too-deep"} {
		t.Run(mode, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{"mode": mode})
			if err != nil {
				t.Fatal(err)
			}
			if result, err := caller.CallExtension(ctx, meta.ID, "search.engine", input); err == nil {
				t.Errorf("CallExtension(%s) accepted result %s", mode, result)
			}
		})
	}
}

func TestExtensionRegressionWaitForExecutionLockUsesCallDeadline(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeExtensionTestPlugin(t, []ExtensionSpec{{Point: "search.engine"}}, `
JellyCMS.registerHook("OnSlowExtensionRegression", function () {
  JellyCMS.settings.get("hold");
});
JellyCMS.registerHook("OnCalculate", function (data) {
  JellyCMS.log.info("extension-regression-legacy-hook:" + (data.n + 1));
});
JellyCMS.registerExtension("search.engine", function () {
  JellyCMS.settings.get("extension");
  return {ok: true};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() with slow-hook extension: %v", err)
	}
	caller, ok := any(runtime).(extensionCaller)
	if !ok {
		t.Fatal("Runtime does not implement CallExtension(context.Context, string, string, json.RawMessage)")
	}

	slowStarted := make(chan struct{})
	releaseHook := make(chan struct{})
	var releaseOnce sync.Once
	var extensionCalls atomic.Int32
	release := func() { releaseOnce.Do(func() { close(releaseHook) }) }
	defer release()
	runtime.SetHostHandler(func(ctx context.Context, _ PluginMetadata, method string, args json.RawMessage) (json.RawMessage, error) {
		if method != "settings.get" {
			return nil, errors.New("unexpected extension regression host method")
		}
		var keys []string
		if err := json.Unmarshal(args, &keys); err != nil || len(keys) != 1 {
			return nil, errors.New("invalid extension regression host arguments")
		}
		switch keys[0] {
		case "hold":
			close(slowStarted)
			select {
			case <-releaseHook:
				return json.RawMessage("null"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case "extension":
			extensionCalls.Add(1)
			return json.RawMessage("null"), nil
		default:
			return nil, errors.New("unknown extension regression host key")
		}
	})

	hookDone := make(chan struct{})
	go func() {
		runtime.CallHook(Hook("OnSlowExtensionRegression"), map[string]interface{}{})
		close(hookDone)
	}()
	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow hook never entered its blocking host call")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	type extensionCallResult struct {
		result  json.RawMessage
		err     error
		elapsed time.Duration
	}
	callDone := make(chan extensionCallResult, 1)
	started := time.Now()
	go func() {
		result, err := caller.CallExtension(ctx, meta.ID, "search.engine", json.RawMessage(`{}`))
		callDone <- extensionCallResult{result: result, err: err, elapsed: time.Since(started)}
	}()
	var call extensionCallResult
	select {
	case call = <-callDone:
	case <-time.After(250 * time.Millisecond):
		t.Error("CallExtension() did not return within its 100ms context deadline while execMu was occupied")
		release()
		select {
		case call = <-callDone:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("CallExtension() remained blocked after the slow hook was released")
		}
	}
	cancel()
	if call.err == nil {
		t.Errorf("CallExtension() succeeded after its context deadline: %s", call.result)
	}
	if call.elapsed > 250*time.Millisecond {
		t.Errorf("CallExtension() waited %s for execMu, want return within its 100ms deadline (250ms scheduling ceiling)", call.elapsed)
	}

	release()
	select {
	case <-hookDone:
	case <-time.After(2 * time.Second):
		t.Fatal("slow hook goroutine did not finish after its host call was released")
	}
	if got := extensionCalls.Load(); got != 0 {
		t.Errorf("expired CallExtension() ran its provider %d times after the slow hook released", got)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	result, err := caller.CallExtension(callCtx, meta.ID, "search.engine", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallExtension() after the old hook completed: %v", err)
	}
	var decoded struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil || !decoded.OK {
		t.Fatalf("CallExtension() result = %s, err=%v; want {\"ok\":true}", result, err)
	}
	if got := extensionCalls.Load(); got != 1 {
		t.Errorf("extension provider calls after one healthy invocation = %d, want 1", got)
	}
	legacyOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnCalculate"), map[string]interface{}{"n": 41})
	})
	assertContains(t, legacyOutput, "extension-regression-legacy-hook:42")
}

func TestExtensionRegressionRejectsUndeclaredProviderRegistration(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, _ := writeExtensionTestPlugin(t, []ExtensionSpec{{Point: "search.engine"}}, `
JellyCMS.registerExtension("media.storage", function (input) {
  return {path: input.path};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err == nil {
		t.Error("Load() accepted registerExtension() for a point absent from the manifest")
	}
}

func writeExtensionTestPlugin(t *testing.T, extensions []ExtensionSpec, entrypoint string) (string, PluginMetadata) {
	t.Helper()
	pluginDir, meta := writeTestPlugin(t, entrypoint)
	meta.Extensions = extensions
	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal extension plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write extension plugin manifest: %v", err)
	}
	return pluginDir, meta
}

func TestExtensionRegressionRepeatedStorageDescriptors(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeExtensionTestPlugin(t, []ExtensionSpec{{Point: "media.storage"}}, `
const base = "http://127.0.0.1:12345";
JellyCMS.registerExtension("media.storage", function (input) {
  return {url:base + "/objects/" + input.path,headers:{"X-Extension-Storage":"allowed"}};
});
`)
	if err := runtime.Load(context.Background(), pluginDir); err != nil {
		t.Fatalf("Load() with media storage descriptor: %v", err)
	}
	caller, ok := any(runtime).(extensionCaller)
	if !ok {
		t.Fatal("Runtime does not implement CallExtension(context.Context, string, string, json.RawMessage)")
	}

	for i := 0; i < 40; i++ {
		op := []string{"put", "open", "stat", "delete"}[i%4]
		path := "runtime-proof.png"
		if i > 0 {
			path = ".jelly-storage-test-0123456789abcdef0123456789abcdef"
		}
		input, err := json.Marshal(map[string]any{"operation": op, "path": path, "content_type": "application/octet-stream", "size": i})
		if err != nil {
			t.Fatal(err)
		}
		callCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, callErr := caller.CallExtension(callCtx, meta.ID, "media.storage", input)
		cancel()
		if callErr != nil {
			t.Fatalf("call %d operation=%s path_length=%d: %v", i, op, len(path), callErr)
		}
		var got struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(result, &got); err != nil {
			t.Fatalf("decode call %d operation=%s path_length=%d result=%s: %v", i, op, len(path), result, err)
		}
		wantURL := "http://127.0.0.1:12345/objects/" + path
		if got.URL != wantURL || got.Headers["X-Extension-Storage"] != "allowed" {
			t.Fatalf("call %d operation=%s path_length=%d result=%s; want URL %q and storage header", i, op, len(path), result, wantURL)
		}
	}
}
