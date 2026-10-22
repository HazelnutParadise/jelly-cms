package plugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSandboxFixture writes one fixture below a temporary website directory.
// The caller has already changed into that directory to model the website cwd.
func writeSandboxFixture(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join("data", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}

	return path
}

func assertFixtureUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s after plugin load: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("fixture %s was modified: got %q, want %q", path, got, want)
	}
}

func assertHookStillRuns(t *testing.T, runtime *Runtime, hook Hook, pluginID, markerPrefix string) {
	t.Helper()

	output := captureStdout(t, func() {
		runtime.CallHook(hook, map[string]interface{}{"plugin": pluginID})
	})
	assertContains(t, output, markerPrefix+pluginID)
}

// TestPluginSandboxBlocksWebsiteFileReads requires a plugin to be unable to
// read a fake website secret through the inherited QuickJS cwd. Loading the
// plugin must still succeed and its hook must keep working; only the read has
// to be denied or produce null.
func TestPluginSandboxBlocksWebsiteFileReads(t *testing.T) {
	websiteDir := t.TempDir()
	t.Chdir(websiteDir)

	secret := "JELLY_SANDBOX_READ_SECRET_2f6c9a4b"
	fixture := []byte(`{"api_key":"` + secret + `"}`)
	fixturePath := writeSandboxFixture(t, "config.json", fixture)

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeTestPlugin(t, `
try {
  var content = std.loadFile("data/config.json");
  if (content === null || content === undefined) {
    JellyCMS.log.info("sandbox-read:null");
  } else {
    JellyCMS.log.info("sandbox-read:leaked:" + content);
  }
} catch (e) {
  JellyCMS.log.info("sandbox-read:denied");
}
JellyCMS.registerHook("OnSandboxRead", function (data) {
  JellyCMS.log.info("sandbox-hook:read:" + data.plugin);
});
`)

	output := captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})

	if strings.Contains(output, secret) || strings.Contains(output, "sandbox-read:leaked") {
		t.Errorf("plugin read the website fixture and logged it; output = %q", output)
	}

	denied := strings.Contains(output, "sandbox-read:denied") ||
		strings.Contains(output, "sandbox-read:null")
	if !denied {
		t.Errorf("plugin read was not reported as denied or null; output = %q", output)
	}

	assertFixtureUnchanged(t, fixturePath, fixture)

	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Errorf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}
	assertHookStillRuns(t, runtime, Hook("OnSandboxRead"), meta.ID, "sandbox-hook:read:")
}

// TestPluginSandboxBlocksWebsiteFileWrites requires a plugin to be unable to
// overwrite a fake website file through the inherited QuickJS cwd. The write
// only targets this test's own fixture.
func TestPluginSandboxBlocksWebsiteFileWrites(t *testing.T) {
	websiteDir := t.TempDir()
	t.Chdir(websiteDir)

	original := []byte("JELLY_SANDBOX_WRITE_ORIGINAL\n")
	fixturePath := writeSandboxFixture(t, "sandbox-write-target.txt", original)

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeTestPlugin(t, `
try {
  var file = std.open("data/sandbox-write-target.txt", "w");
  if (file === null || file === undefined) {
    JellyCMS.log.info("sandbox-write:denied");
  } else {
    file.puts("JELLY_SANDBOX_WRITE_OVERWRITTEN");
    file.close();
    JellyCMS.log.info("sandbox-write:wrote");
  }
} catch (e) {
  JellyCMS.log.info("sandbox-write:denied");
}
JellyCMS.registerHook("OnSandboxWrite", function (data) {
  JellyCMS.log.info("sandbox-hook:write:" + data.plugin);
});
`)

	output := captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})

	if strings.Contains(output, "sandbox-write:wrote") {
		t.Errorf("plugin was allowed to open the website fixture for writing; output = %q", output)
	}
	if !strings.Contains(output, "sandbox-write:denied") {
		t.Errorf("plugin write was not reported as denied; output = %q", output)
	}

	assertFixtureUnchanged(t, fixturePath, original)

	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Errorf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}
	assertHookStillRuns(t, runtime, Hook("OnSandboxWrite"), meta.ID, "sandbox-hook:write:")
}

// TestPluginSandboxHidesEnvironmentSecrets checks the existing worker env
// allowlist: a private token set in the parent must not reach std.getenv.
// This case is expected to pass on the current implementation.
func TestPluginSandboxHidesEnvironmentSecrets(t *testing.T) {
	token := "JELLY_SANDBOX_PRIVATE_TOKEN_8d1e5c7a"
	t.Setenv("JELLY_TEST_PRIVATE_TOKEN", token)

	runtime, err := NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pluginDir, meta := writeTestPlugin(t, `
var token;
try {
  token = std.getenv("JELLY_TEST_PRIVATE_TOKEN");
} catch (e) {
  token = undefined;
}
if (token === undefined || token === null || token === "") {
  JellyCMS.log.info("sandbox-env:missing");
} else {
  JellyCMS.log.info("sandbox-env:leaked:" + token);
}
JellyCMS.registerHook("OnSandboxEnv", function (data) {
  JellyCMS.log.info("sandbox-hook:env:" + data.plugin);
});
`)

	output := captureStdout(t, func() {
		if err := runtime.Load(context.Background(), pluginDir); err != nil {
			t.Errorf("Load() error = %v", err)
		}
	})

	if strings.Contains(output, token) || strings.Contains(output, "sandbox-env:leaked") {
		t.Errorf("plugin saw the private environment token; output = %q", output)
	}
	if !strings.Contains(output, "sandbox-env:missing") {
		t.Errorf("std.getenv did not report the token as missing; output = %q", output)
	}

	if _, err := runtime.GetPlugin(meta.ID); err != nil {
		t.Errorf("GetPlugin(%q) after Load() error = %v", meta.ID, err)
	}
	assertHookStillRuns(t, runtime, Hook("OnSandboxEnv"), meta.ID, "sandbox-hook:env:")
}
