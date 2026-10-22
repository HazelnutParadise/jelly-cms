package plugin

import (
	"context"
	"strings"
	"testing"
	"unsafe"
)

func TestClosedRuntimeRejectsLoadAndReload(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeTestPlugin(t, `
JellyCMS.log.info("plugin-entry-ran:closed-runtime");
`)

	runtime.Close()

	loadOutput := captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err == nil {
			t.Error("Load() after Close() error = nil, want closed runtime error")
		}
	})
	if strings.Contains(loadOutput, "plugin-entry-ran:closed-runtime") {
		t.Errorf("Load() after Close() executed the entry script; output = %q", loadOutput)
	}
	if _, err := runtime.GetPlugin(meta.ID); err == nil {
		t.Error("GetPlugin() after rejected Load() error = nil, want plugin not found")
	}

	reloadOutput := captureStdout(t, func() {
		if err := runtime.Reload(context.Background(), pluginDir); err == nil {
			t.Error("Reload() after Close() error = nil, want closed runtime error")
		}
	})
	if strings.Contains(reloadOutput, "plugin-entry-ran:closed-runtime") {
		t.Errorf("Reload() after Close() executed the entry script; output = %q", reloadOutput)
	}
	if _, err := runtime.GetPlugin(meta.ID); err == nil {
		t.Error("GetPlugin() after rejected Reload() error = nil, want plugin not found")
	}
}

func TestCloseIsIdempotentNoCallbacks(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeTestPlugin(t, `
JellyCMS.registerHook("OnShutdown", function (data) {
  JellyCMS.log.info("callback-result:shutdown:" + data.plugin);
});
JellyCMS.registerHook("AfterClose", function (data) {
  JellyCMS.log.info("callback-result:after-close:" + data.plugin);
});
`)

	captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})

	firstCloseOutput := captureStdout(t, runtime.Close)
	shutdownMarker := "callback-result:shutdown:" + meta.ID
	if count := strings.Count(firstCloseOutput, shutdownMarker); count != 1 {
		t.Errorf("first Close() ran OnShutdown %d times, want 1; output = %q", count, firstCloseOutput)
	}

	secondCloseOutput := captureStdout(t, runtime.Close)
	if count := strings.Count(secondCloseOutput, shutdownMarker); count != 0 {
		t.Errorf("second Close() ran OnShutdown %d times, want 0; output = %q", count, secondCloseOutput)
	}

	hookOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("AfterClose"), map[string]interface{}{"plugin": meta.ID})
	})
	if strings.Contains(hookOutput, "callback-result:after-close:") {
		t.Errorf("CallHook() after Close() ran a plugin callback; output = %q", hookOutput)
	}

	if _, err := runtime.GetPlugin(meta.ID); err == nil {
		t.Error("GetPlugin() after Close() error = nil, want plugin not found")
	}
}

func TestHookPayloadConversionFailsAtomically(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeTestPlugin(t, `
JellyCMS.registerHook("Atomic", function (data) {
  JellyCMS.log.info("callback-ran:atomic:" + data.valid);
});
`)

	captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})

	callOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("Atomic"), map[string]interface{}{
			"valid":   "payload-value",
			"invalid": unsafe.Pointer(nil),
		})
	})
	if strings.Contains(callOutput, "callback-ran:atomic:") {
		t.Errorf("CallHook() ran the callback with a partially converted payload; output = %q", callOutput)
	}

	validOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("Atomic"), map[string]interface{}{"valid": "still-works"})
	})
	assertContains(t, validOutput, "callback-ran:atomic:still-works")

	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Errorf("GetPlugin(%q) after failed payload conversion error = %v", meta.ID, err)
	}
}
