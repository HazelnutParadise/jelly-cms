package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Keys whose name ends in "_enc" are sealed: the value is AES-GCM encrypted
// before it is written to options.value. The key comes from the TOTP
// encryption key in data/auth-secrets.json (the file auth already manages),
// through an HMAC-derived subkey, so no second secret file is needed. A backup
// that includes auth-secrets.json therefore also carries the sealing key.
//
// Stored form: "enc:v1:" followed by base64(nonce || ciphertext). The key name
// is bound as additional data, so a value cannot be moved to another key.
// Rows without the prefix are legacy plaintext and are returned as they are.
const (
	sealedPrefix       = "enc:v1:"
	sealedSuffix       = "_enc"
	totpKeyField       = "totp_key"
	settingsSubkeyInfo = "jelly-settings-secret-v1"
)

// ErrKeyMissing is returned when a sealed value must be opened but the
// encryption key is not in the auth secrets file.
var ErrKeyMissing = errors.New("settings: sealing key is not available")

// keyFileMu serializes this package's reads and writes of the secrets file in
// this process. auth's own lock file is not shared with this package, so a
// first-time key creation that races with a TOTP enrollment in another
// process is not covered. That window is limited to the first sealed write.
var keyFileMu sync.Mutex

// authSecretsPath matches auth.secretFilePath.
func authSecretsPath() string {
	return filepath.Join("data", "auth-secrets.json")
}

// sealed reports whether key stores its value sealed.
func sealed(key string) bool {
	return strings.HasSuffix(key, sealedSuffix)
}

// settingsKey returns the subkey used to seal values. With create set, a
// missing totp_key is generated and written back, in the same format auth uses.
func settingsKey(create bool) ([]byte, error) {
	keyFileMu.Lock()
	defer keyFileMu.Unlock()

	path := authSecretsPath()
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrKeyMissing, path, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid auth secrets file", ErrKeyMissing)
	}

	var encoded string
	if raw, ok := doc[totpKeyField]; ok && json.Unmarshal(raw, &encoded) == nil && encoded != "" {
		return deriveSubkey(encoded)
	}
	if !create {
		return nil, ErrKeyMissing
	}

	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		return nil, err
	}
	encoded = base64.StdEncoding.EncodeToString(master)
	value, err := json.Marshal(encoded)
	if err != nil {
		return nil, err
	}
	doc[totpKeyField] = value
	updated, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if err := replaceFile(path, updated); err != nil {
		return nil, fmt.Errorf("save sealing key: %w", err)
	}
	return deriveSubkey(encoded)
}

func deriveSubkey(encodedMaster string) ([]byte, error) {
	master, err := base64.StdEncoding.DecodeString(encodedMaster)
	if err != nil || len(master) != 32 {
		return nil, fmt.Errorf("%w: invalid TOTP encryption key", ErrKeyMissing)
	}
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte(settingsSubkeyInfo))
	return mac.Sum(nil), nil
}

// replaceFile writes payload to a temporary file with mode 0600 and renames it
// over path, so readers never see a partial file.
func replaceFile(path string, payload []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".auth-secrets-settings-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// sealValue encrypts plain for key. An empty value stays empty ("not set").
func sealValue(key, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	subkey, err := settingsKey(true)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(subkey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealedBytes := gcm.Seal(nonce, nonce, []byte(plain), []byte(key))
	return sealedPrefix + base64.StdEncoding.EncodeToString(sealedBytes), nil
}

// openValue decrypts a stored value of key. Values without the prefix are
// legacy plaintext and are returned unchanged.
func openValue(key, stored string) (string, error) {
	if !strings.HasPrefix(stored, sealedPrefix) {
		return stored, nil
	}
	subkey, err := settingsKey(false)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(subkey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealedPrefix))
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("settings: malformed sealed value")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(key))
	if err != nil {
		return "", errors.New("settings: sealed value failed to open")
	}
	return string(plain), nil
}

// revealStored returns the plaintext of a stored value for server-side use.
func revealStored(key, stored string) (string, error) {
	if !sealed(key) {
		return stored, nil
	}
	return openValue(key, stored)
}

func newGCM(subkey []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(subkey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// DisplayValue returns what the admin API shows for a stored value. Sealed
// values are opened and then masked. Other values are masked only when their
// key names a secret. An unreadable sealed value shows as "****".
func DisplayValue(key, stored string) string {
	plain, err := revealStored(key, stored)
	if err != nil {
		return "****"
	}
	if sealed(key) {
		return maskTail(plain)
	}
	return Masked(key, plain)
}
