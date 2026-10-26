package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Minisign file layout (https://jedisct1.github.io/minisign/). A public key
// file is a comment line and a base64 key line. A signature file has an
// untrusted comment, a base64 signature line, a trusted comment and a base64
// global signature. The key line and signature line decode as:
//
//	public key:  "Ed" | key id (8) | public key (32)              = 42 bytes
//	signature:   "Ed" or "ED" | key id (8) | signature (64)       = 74 bytes
//
// "Ed" signs the file itself. "ED" signs its BLAKE2b-512 hash. The global
// signature covers the file signature followed by the trusted comment text.
const (
	algLegacy       = "Ed"
	algHashed       = "ED"
	untrustedPrefix = "untrusted comment:"
	trustedPrefix   = "trusted comment: "
	pubKeyLen       = 42
	sigLen          = 74
	globalSigLen    = 64
)

type signatureFile struct {
	alg     string
	keyID   [8]byte
	sig     []byte
	trusted []byte
	global  []byte
}

// verifySignature checks a minisign signature over data with the configured
// public key. Both the file signature and the trusted comment signature must hold.
func verifySignature(data, sigFile []byte) error {
	return VerifyMinisign(publicKeyLine(), data, sigFile)
}

// VerifyMinisign checks data against the minisign signature file sigFile using
// the public key line pubLine. Both the file signature and the trusted comment
// signature must hold. An empty pubLine returns ErrNoSigningKey.
func VerifyMinisign(pubLine string, data, sigFile []byte) error {
	if pubLine == "" {
		return ErrNoSigningKey
	}
	keyID, pub, err := parsePublicKey(pubLine)
	if err != nil {
		return err
	}
	sig, err := parseSignatureFile(sigFile)
	if err != nil {
		return err
	}
	if sig.keyID != keyID {
		return errors.New("minisign: signature key id does not match the public key")
	}
	msg := data
	if sig.alg == algHashed {
		h := blake2b.Sum512(data)
		msg = h[:]
	}
	if !ed25519.Verify(pub, msg, sig.sig) {
		return errors.New("minisign: file signature does not verify")
	}
	global := append(append([]byte{}, sig.sig...), sig.trusted...)
	if !ed25519.Verify(pub, global, sig.global) {
		return errors.New("minisign: trusted comment signature does not verify")
	}
	return nil
}

// parsePublicKey reads the key line of a minisign public key. Comment lines
// are skipped, so the file text can be passed as is.
func parsePublicKey(text string) ([8]byte, ed25519.PublicKey, error) {
	var id [8]byte
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, untrustedPrefix) {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(line)
		if err != nil || len(b) != pubKeyLen || string(b[:2]) != algLegacy {
			return id, nil, errors.New("minisign: public key line is malformed")
		}
		copy(id[:], b[2:10])
		return id, ed25519.PublicKey(b[10:]), nil
	}
	return id, nil, errors.New("minisign: no public key line")
}

// parseSignatureFile decodes a minisign signature file.
func parseSignatureFile(data []byte) (signatureFile, error) {
	var out signatureFile
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], untrustedPrefix) || !strings.HasPrefix(lines[2], trustedPrefix) {
		return out, errors.New("minisign: signature file is malformed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != sigLen {
		return out, errors.New("minisign: signature line is malformed")
	}
	out.alg = string(raw[:2])
	if out.alg != algLegacy && out.alg != algHashed {
		return out, errors.New("minisign: unsupported signature algorithm")
	}
	copy(out.keyID[:], raw[2:10])
	out.sig = raw[10:]
	out.trusted = []byte(strings.TrimPrefix(lines[2], trustedPrefix))
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != globalSigLen {
		return out, errors.New("minisign: global signature line is malformed")
	}
	out.global = global
	return out, nil
}

// checksumFor returns the sha256 hex digest that checksums.txt lists for name.
// The file is in the form written by sha256sum: "<hex>  <name>" per line.
func checksumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != 64 || !isHex(digest) {
			return "", ErrVerification
		}
		return digest, nil
	}
	return "", ErrVerification
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
