package auth

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// LoadOrCreateSecretKey returns the session signing key stored in
// <instanceDir>/secret_key, creating the file (mode 0600, 64 hex
// characters) when it does not exist. This is the same file and the
// same format the Python version uses, so the key survives a switch
// between the two versions.
func LoadOrCreateSecretKey(instanceDir string) (string, error) {
	path := filepath.Join(instanceDir, "secret_key")
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		return "", err
	}
	return key, nil
}
