// Package auth handles password hashes and the session signing key.
//
// Password hashes are stored in Werkzeug's formats, so the Go version
// and the Python version read each other's databases:
//
//	scrypt:32768:8:1$<salt>$<hex>     (current default)
//	pbkdf2:sha256:<iterations>$<salt>$<hex>
package auth

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"runtime/debug"
	"strconv"
	"strings"

	"used-device-inventory-go/internal/xcrypto"
)

// The parameters Werkzeug's generate_password_hash uses by default.
const (
	scryptN       = 32768
	scryptR       = 8
	scryptP       = 1
	scryptKeyLen  = 64
	pbkdf2Digest  = "sha256"
	pbkdf2Iters   = 600000
	pbkdf2KeyLen  = 32
	saltLength    = 16
	saltAlphabet  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	MinLength     = 8 // shared by the screens and reset-password
	hashSemaphore = 4 // scrypt uses ~34 MB per call; cap concurrency
)

var scryptSem = make(chan struct{}, hashSemaphore)

func runScrypt(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	scryptSem <- struct{}{}
	defer func() {
		<-scryptSem
		debug.FreeOSMemory()
	}()
	return xcrypto.Scrypt(password, salt, n, r, p, keyLen)
}

func randomSalt() (string, error) {
	buf := make([]byte, saltLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = saltAlphabet[int(b)%len(saltAlphabet)]
	}
	return string(buf), nil
}

// Hash returns a new password hash in Werkzeug's current default
// format (scrypt). Werkzeug's check_password_hash accepts it.
func Hash(password string) (string, error) {
	salt, err := randomSalt()
	if err != nil {
		return "", err
	}
	dk, err := runScrypt([]byte(password), []byte(salt),
		scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("scrypt:%d:%d:%d$%s$%s",
		scryptN, scryptR, scryptP, salt, hex.EncodeToString(dk)), nil
}

// Check reports whether password matches a stored Werkzeug-format
// hash (scrypt or pbkdf2). Malformed hashes simply do not match.
func Check(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 {
		return false
	}
	method, salt, wantHex := parts[0], parts[1], parts[2]
	want, err := hex.DecodeString(wantHex)
	if err != nil || len(want) == 0 {
		return false
	}
	fields := strings.Split(method, ":")
	var got []byte
	switch fields[0] {
	case "scrypt":
		if len(fields) != 4 {
			return false
		}
		n, err1 := strconv.Atoi(fields[1])
		r, err2 := strconv.Atoi(fields[2])
		p, err3 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil || err3 != nil {
			return false
		}
		got, err = runScrypt([]byte(password), []byte(salt), n, r, p, len(want))
		if err != nil {
			return false
		}
	case "pbkdf2":
		if len(fields) != 3 {
			return false
		}
		var h func() hash.Hash
		switch fields[1] {
		case "sha256":
			h = sha256.New
		case "sha512":
			h = sha512.New
		case "sha1":
			h = sha1.New
		default:
			return false
		}
		iters, err := strconv.Atoi(fields[2])
		if err != nil || iters <= 0 {
			return false
		}
		got = xcrypto.PBKDF2([]byte(password), []byte(salt), iters, len(want), h)
	default:
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
