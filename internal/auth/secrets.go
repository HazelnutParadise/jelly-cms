package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const (
	secretFileName    = "auth-secrets.json"
	maxSecretFileSize = 64 * 1024
	minSecretKeyBytes = 32
)

type secretFile struct {
	Version       int    `json:"version"`
	SessionSecret string `json:"session_secret"`
	JWTSecret     string `json:"jwt_secret"`
}

func secretFilePath() string {
	return filepath.Join("data", secretFileName)
}

func loadOrCreateSecretsFile(path string) ([]byte, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, nil, err
		}
		sessionCandidate, err := generateSecret()
		if err != nil {
			return nil, nil, err
		}
		jwtCandidate, err := generateSecret()
		if err != nil {
			return nil, nil, err
		}
		if err := saveSecretsFile(path, sessionCandidate, jwtCandidate); err != nil {
			return nil, nil, err
		}
		return readSecretsFile(path)
	}

	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("auth secret file is not a regular file")
	}

	return readSecretsFile(path)
}

func readSecretsFile(path string) ([]byte, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("auth secret file is not a regular file")
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm()&0o077 != 0 {
			return nil, nil, fmt.Errorf("auth secret file has group or other permissions")
		}
	}
	if info.Size() > maxSecretFileSize {
		return nil, nil, fmt.Errorf("auth secret file is too large")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxSecretFileSize {
		return nil, nil, fmt.Errorf("auth secret file is too large")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	var doc secretFile
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("invalid auth secret file")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, nil, fmt.Errorf("trailing data in auth secret file")
	}

	if doc.Version != 1 {
		return nil, nil, fmt.Errorf("unsupported auth secret file version")
	}
	sessionBytes, err := decodeSecret(doc.SessionSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid session secret")
	}
	if len(sessionBytes) < minSecretKeyBytes {
		return nil, nil, fmt.Errorf("session secret is too short")
	}
	jwtBytes, err := decodeSecret(doc.JWTSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid jwt secret")
	}
	if len(jwtBytes) < minSecretKeyBytes {
		return nil, nil, fmt.Errorf("jwt secret is too short")
	}

	// Persist directory entries after the complete, valid secret has been read.
	// This covers files created by another process whose rename/link may not yet
	// be durable, and prevents returning a key that is not yet on disk.
	if err := syncSecretDirectories(path); err != nil {
		return nil, nil, err
	}

	return sessionBytes, jwtBytes, nil
}

func decodeSecret(raw string) ([]byte, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty secret")
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("invalid base64 secret")
}

func generateSecret() ([]byte, error) {
	buf := make([]byte, minSecretKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func saveSecretsFile(path string, session, jwt []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("auth secret directory is not a directory")
	}

	doc := secretFile{
		Version:       1,
		SessionSecret: base64.StdEncoding.EncodeToString(session),
		JWTSecret:     base64.StdEncoding.EncodeToString(jwt),
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	temp, err := os.CreateTemp(dir, ".auth-secrets-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }

	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		cleanup()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return err
	}

	if err := os.Link(tempName, path); err != nil {
		if os.IsExist(err) {
			cleanup()
			return nil
		}
		cleanup()
		return err
	}
	cleanup()
	return nil
}

// syncDirectory fsyncs a directory so newly created or renamed entries become
// durable. On Unix this step is required, not best-effort: any failure means the
// secret key must not be published. On Windows there is no portable directory
// fsync, so this step is intentionally skipped there; tempfile fsync still
// provides what the platform can guarantee, and crash durability of the
// directory entry itself is not guaranteed on that platform.
func syncDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		// Windows has no portable directory fsync; crash durability of the
		// directory entry itself is not guaranteed on this platform.
		return nil
	}
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

func syncSecretDirectories(path string) error {
	dir := filepath.Dir(path)
	if err := syncDirectory(dir); err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if parent != dir {
		if err := syncDirectory(parent); err != nil {
			return err
		}
	}
	return nil
}
