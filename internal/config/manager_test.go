package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	configFile = "data/config.json"

	// saveHelperEnv marks the child process that performs the failing Save.
	saveHelperEnv = "JELLY_CMS_SAVE_HELPER"
	// saveHelperUnsupported is printed by the child when the platform cannot
	// cap the file size; the parent then skips the test.
	saveHelperUnsupported = "SAVE_HELPER_UNSUPPORTED"
)

// keepGlobalConfig restores the global configuration when the test ends.
func keepGlobalConfig(t *testing.T) {
	t.Helper()

	previous := GlobalConfig
	t.Cleanup(func() { GlobalConfig = previous })
	GlobalConfig = nil
}

func TestSaveWritesTheConfigFileWithOwnerOnlyPermissions(t *testing.T) {
	t.Chdir(t.TempDir())
	keepGlobalConfig(t)

	cfg := &Config{Database: DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "5432",
		User:     "jelly",
		Password: "s3cret",
		Name:     "jelly",
		Timezone: "Asia/Taipei",
	}}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	info, err := os.Stat(configFile)
	if err != nil {
		t.Fatalf("stat %s: %v", configFile, err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("Save() wrote %s with mode %v, want 0600 (the file holds the database password)", configFile, got)
	}
}

func TestSaveKeepsTheConfigContent(t *testing.T) {
	t.Chdir(t.TempDir())
	keepGlobalConfig(t)

	cfg := &Config{Database: DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "5432",
		User:     "jelly",
		Password: "s3cret",
		Name:     "jelly",
		Timezone: "Asia/Taipei",
	}}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	raw, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read %s: %v", configFile, err)
	}

	var decoded Config
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal %s: %v", configFile, err)
	}
	if decoded.Database != cfg.Database {
		t.Errorf("Save() wrote %+v, want %+v", decoded.Database, cfg.Database)
	}
}

func TestSaveCreatesTheDataDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	keepGlobalConfig(t)

	if err := Save(&Config{Database: DatabaseConfig{Host: "127.0.0.1", Port: "5432"}}); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	info, err := os.Stat("data")
	if err != nil {
		t.Fatalf("stat data: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Save() created data as %v, want a directory", info.Mode())
	}
	if _, err := os.Stat(configFile); err != nil {
		t.Errorf("stat %s: %v, want the config file to exist", configFile, err)
	}
}

func TestLoadReadsTheSavedConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	keepGlobalConfig(t)

	cfg := &Config{Database: DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "5432",
		User:     "jelly",
		Password: "s3cret",
		Name:     "jelly",
		Timezone: "Asia/Taipei",
	}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if loaded == nil {
		t.Fatal("Load() = nil, want the saved config")
	}
	if loaded.Database != cfg.Database {
		t.Errorf("Load() returned %+v, want %+v", loaded.Database, cfg.Database)
	}
	if GlobalConfig == nil {
		t.Error("Load() left GlobalConfig empty, want the loaded config")
	}
}

// TestSaveFailureKeepsTheExistingConfigFile forces the write to fail the way a
// full disk fails: the destination is already open and truncated when the
// write is rejected. The file size limit is process wide, so the failing Save
// runs in a child process and this test only prepares and checks the file.
func TestSaveFailureKeepsTheExistingConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0755); err != nil {
		t.Fatalf("create data: %v", err)
	}

	path := filepath.Join(dir, configFile)
	original := []byte("{\n  \"database\": {\n    \"host\": \"original-host\",\n    \"port\": \"5432\",\n    \"user\": \"original-user\",\n    \"password\": \"original-pass\",\n    \"name\": \"original-db\",\n    \"timezone\": \"UTC\"\n  }\n}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatalf("write the original %s: %v", path, err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSaveHelperProcess$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), saveHelperEnv+"=1")
	output, err := cmd.CombinedOutput()
	if strings.Contains(string(output), saveHelperUnsupported) {
		t.Skipf("cannot cap the file size on this platform: %s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("the helper process could not make Save fail: %v\n%s", err, output)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s after the failed Save: %v", path, err)
	}
	if !bytes.Equal(after, original) {
		t.Errorf("Save() damaged the config that was already on disk:\n got %q\nwant %q", after, original)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s after the failed Save: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("Save() changed the mode of the existing %s to %v, want 0600", configFile, got)
	}
}

// TestSaveHelperProcess is only a helper for
// TestSaveFailureKeepsTheExistingConfigFile. It runs in its own process
// because the file size limit applies to the whole process.
func TestSaveHelperProcess(t *testing.T) {
	if os.Getenv(saveHelperEnv) != "1" {
		t.Skip("helper process for TestSaveFailureKeepsTheExistingConfigFile")
	}

	const limit = int64(16)
	release, err := limitFileSize(limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, saveHelperUnsupported+": "+err.Error())
		t.Skip("file size limits are not available on this platform")
	}
	defer release()

	cfg := &Config{Database: DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "5432",
		User:     "jelly",
		Password: "s3cret",
		Name:     "jelly",
		Timezone: "Asia/Taipei",
	}}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode the config: %v", err)
	}
	if int64(len(encoded)) <= limit {
		t.Fatalf("test setup: the config is %d bytes, it must be larger than the %d byte limit", len(encoded), limit)
	}

	if err := Save(cfg); err == nil {
		t.Fatalf("Save() = nil, want the write to fail under the %d byte file size limit", limit)
	}
}

func TestSaveOverwritesAnExistingConfigFile(t *testing.T) {
	t.Chdir(t.TempDir())
	keepGlobalConfig(t)

	if err := os.MkdirAll("data", 0755); err != nil {
		t.Fatalf("create data: %v", err)
	}
	if err := os.WriteFile(configFile, []byte(`{"database":{"host":"stale"}}`), 0600); err != nil {
		t.Fatalf("write the stale %s: %v", configFile, err)
	}

	cfg := &Config{Database: DatabaseConfig{Host: "127.0.0.1", Port: "5432", User: "jelly", Password: "s3cret", Name: "jelly", Timezone: "UTC"}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	raw, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read %s: %v", configFile, err)
	}
	if bytes.Contains(raw, []byte("stale")) {
		t.Errorf("Save() kept the stale content: %q", raw)
	}

	var decoded Config
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal %s: %v", configFile, err)
	}
	if decoded.Database != cfg.Database {
		t.Errorf("Save() wrote %+v, want %+v", decoded.Database, cfg.Database)
	}
}
