package plugin

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeLoadsPluginAndRegistersJellyCMSAPI(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	pluginDir, meta := writeTestPlugin(t, `
if (typeof JellyCMS !== "object" ||
    typeof JellyCMS.registerHook !== "function" ||
    typeof JellyCMS.log.info !== "function" ||
    typeof JellyCMS.config.get !== "function") {
  throw new Error("JellyCMS API is incomplete");
}
JellyCMS.log.info("plugin-index-ran:api-available");
`)

	output := captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})
	if !strings.Contains(output, "plugin-index-ran:api-available") {
		t.Errorf("index.js did not execute through JellyCMS.log.info; output = %q", output)
	}
	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Errorf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}
}

func TestRuntimeCallsPluginHooksAndPreservesThemOnFailedReload(t *testing.T) {
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}

	pluginDir, meta := writeTestPlugin(t, `
JellyCMS.registerHook("OnBoot", function (data) {
  JellyCMS.log.info("callback-result:boot:" + data.plugin);
});
JellyCMS.registerHook("OnShutdown", function (data) {
  JellyCMS.log.info("callback-result:shutdown:" + data.plugin);
});
JellyCMS.registerHook("OnCalculate", function (data) {
  JellyCMS.log.info("callback-result:calculate:" + (data.n + 1));
});
`)

	loadOutput := captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})
	assertContains(t, loadOutput, "callback-result:boot:"+meta.ID)

	callOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnCalculate"), map[string]interface{}{"n": 41})
	})
	assertContains(t, callOutput, "callback-result:calculate:42")

	if err := os.WriteFile(filepath.Join(pluginDir, meta.Entrypoint), []byte(`throw new Error("broken replacement");`), 0o600); err != nil {
		t.Fatalf("replace plugin entrypoint: %v", err)
	}
	reloadOutput := captureStdout(t, func() {
		if err := runtime.Reload(context.Background(), pluginDir); err == nil {
			t.Error("Reload() error = nil, want failing replacement script error")
		}
	})
	if strings.Contains(reloadOutput, "callback-result:boot:") {
		t.Errorf("failed Reload() unexpectedly ran replacement OnBoot hook; output = %q", reloadOutput)
	}

	retainedOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnCalculate"), map[string]interface{}{"n": 7})
	})
	assertContains(t, retainedOutput, "callback-result:calculate:8")

	shutdownOutput := captureStdout(t, func() {
		if err := runtime.Unload(context.Background(), meta.ID); err != nil {
			t.Errorf("Unload() error = %v", err)
		}
	})
	assertContains(t, shutdownOutput, "callback-result:shutdown:"+meta.ID)

	afterUnloadOutput := captureStdout(t, func() {
		runtime.CallHook(Hook("OnCalculate"), map[string]interface{}{"n": 16})
	})
	if strings.Contains(afterUnloadOutput, "callback-result:calculate:17") {
		t.Errorf("CallHook() ran plugin callback after Unload(); output = %q", afterUnloadOutput)
	}
}

func writeTestPlugin(t *testing.T, entrypoint string) (string, PluginMetadata) {
	t.Helper()
	pluginDir := t.TempDir()
	meta := PluginMetadata{
		ID: "observable-plugin", Name: "Observable plugin", Version: "1.0.0", Entrypoint: "index.js",
	}
	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, meta.Entrypoint), []byte(entrypoint), 0o600); err != nil {
		t.Fatalf("write plugin entrypoint: %v", err)
	}
	return pluginDir, meta
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writer
	run()
	if err := writer.Close(); err != nil {
		t.Errorf("close stdout writer: %v", err)
	}
	os.Stdout = previous
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Errorf("read captured stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("close stdout reader: %v", err)
	}
	return string(output)
}

func assertContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Errorf("output does not contain callback result %q; output = %q", want, output)
	}
}
