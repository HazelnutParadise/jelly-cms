package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/i18n"
)

func TestRunRejectsAuthStartup(t *testing.T) {
	const sentinel = "sentinel-fake-auth-secret-must-not-leak"

	t.Run("corrupt auth secrets file", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		previousConfig := config.GlobalConfig
		previousDB := data.DB
		previousBundle := i18n.Bundle
		config.GlobalConfig = nil
		data.DB = nil
		i18n.Bundle = nil
		t.Cleanup(func() {
			config.GlobalConfig = previousConfig
			data.DB = previousDB
			i18n.Bundle = previousBundle
		})

		t.Setenv("SESSION_SECRET", "")
		t.Setenv("JWT_SECRET", "")

		if err := os.MkdirAll("data", 0700); err != nil {
			t.Fatalf("mkdir data: %v", err)
		}
		secretPath := "data/auth-secrets.json"
		original := []byte(`{"version":1,"session_secret":"` + sentinel + `","jwt_secret":"not-json`)
		if err := os.WriteFile(secretPath, original, 0600); err != nil {
			t.Fatalf("write corrupt secret file: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := run(ctx)
		if err == nil {
			t.Fatal("run() = nil, want an authentication initialization error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "auth") {
			t.Fatalf("run() error = %v, want it to identify authentication initialization", err)
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("run() error leaked the secret sentinel: %v", err)
		}
		if strings.Contains(err.Error(), string(original)) {
			t.Fatalf("run() error leaked the secret file contents: %v", err)
		}
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "listen") || strings.Contains(lower, "bind") {
			t.Fatalf("run() returned a listener error instead of the auth error: %v", err)
		}

		after, readErr := os.ReadFile(secretPath)
		if readErr != nil {
			t.Fatalf("read secret file after run: %v", readErr)
		}
		if string(after) != string(original) {
			t.Fatalf("secret file changed during run")
		}
	})

	t.Run("data path occupied by a regular file", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		previousConfig := config.GlobalConfig
		previousDB := data.DB
		previousBundle := i18n.Bundle
		config.GlobalConfig = nil
		data.DB = nil
		i18n.Bundle = nil
		t.Cleanup(func() {
			config.GlobalConfig = previousConfig
			data.DB = previousDB
			i18n.Bundle = previousBundle
		})

		t.Setenv("SESSION_SECRET", "")
		t.Setenv("JWT_SECRET", "")

		if err := os.WriteFile("data", []byte("not a directory"), 0600); err != nil {
			t.Fatalf("write data file: %v", err)
		}
		beforeData, err := os.ReadFile("data")
		if err != nil {
			t.Fatalf("read data file before run: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = run(ctx)
		if err == nil {
			t.Fatal("run() = nil, want an authentication initialization error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "auth") {
			t.Fatalf("run() error = %v, want it to identify authentication initialization", err)
		}
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "listen") || strings.Contains(lower, "bind") {
			t.Fatalf("run() returned a listener error instead of the auth error: %v", err)
		}

		info, statErr := os.Stat("data")
		if statErr != nil || info.IsDir() {
			t.Fatalf("data path changed type: %v", statErr)
		}
		afterData, readErr := os.ReadFile("data")
		if readErr != nil {
			t.Fatalf("read data file after run: %v", readErr)
		}
		if string(afterData) != string(beforeData) {
			t.Fatalf("data path content changed during run")
		}
	})
}
