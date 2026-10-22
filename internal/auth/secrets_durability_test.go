package auth_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

func TestSecretsDirectorySync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission test")
	}

	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	previousDB := data.DB
	data.DB = nil
	t.Cleanup(func() { data.DB = previousDB })

	t.Setenv("SESSION_SECRET", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("APP_ENV", "")

	dataDir := filepath.Join(dir, "data")
	if err := os.Mkdir(dataDir, 0300); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dataDir, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0700) })

	f, err := os.Open(dataDir)
	if err == nil {
		_ = f.Close()
		t.Skip("environment allows reading the directory despite mode 0300; cannot represent the permission failure")
	}
	if !os.IsPermission(err) {
		t.Fatalf("os.Open(data) error = %v, want permission denied", err)
	}

	if err := auth.Init(); err == nil {
		t.Fatal("Init() succeeded, want an error when the secrets directory cannot be opened for sync")
	}
}
