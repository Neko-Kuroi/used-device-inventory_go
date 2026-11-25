package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type werkzeugHash struct {
	Password string `json:"password"`
	Hash     string `json:"hash"`
}

// Every hash Werkzeug produced must verify with the right password
// and fail with a wrong one.
func TestCheckAgainstWerkzeugHashes(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "werkzeug_hashes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []werkzeugHash
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no vectors")
	}
	for _, e := range entries {
		if !Check(e.Hash, e.Password) {
			t.Errorf("Check(%q) = false, want true", e.Password)
		}
		if Check(e.Hash, e.Password+"-wrong") {
			t.Errorf("Check(%q with wrong password) = true, want false", e.Password)
		}
	}
}

var scryptFormat = regexp.MustCompile(
	`^scrypt:32768:8:1\$[A-Za-z0-9]{16}\$[0-9a-f]{128}$`)

func TestHashFormatAndRoundTrip(t *testing.T) {
	h, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !scryptFormat.MatchString(h) {
		t.Fatalf("Hash format = %q, want Werkzeug scrypt format", h)
	}
	if !Check(h, "correct horse battery") {
		t.Error("own hash does not verify")
	}
	if Check(h, "correct horse batter") {
		t.Error("near-miss password verified")
	}
	h2, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if h == h2 {
		t.Error("two hashes of one password are identical (salt not random)")
	}
}

func TestCheckRejectsMalformed(t *testing.T) {
	for _, stored := range []string{
		"", "garbage", "scrypt:32768:8:1$onlytwo", "md5$x$y",
		"scrypt:a:b:c$salt$00ff", "pbkdf2:md5:1000$salt$00ff",
		"pbkdf2:sha256:-5$salt$00ff", "scrypt:32768:8:1$salt$zz",
	} {
		if Check(stored, "password") {
			t.Errorf("Check(%q) = true, want false", stored)
		}
	}
}

func TestSecretKeyLifecycle(t *testing.T) {
	dir := t.TempDir()
	key1, err := LoadOrCreateSecretKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(key1) != 64 {
		t.Fatalf("key length = %d, want 64 hex chars", len(key1))
	}
	key2, err := LoadOrCreateSecretKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if key1 != key2 {
		t.Error("second load returned a different key")
	}
	info, err := os.Stat(filepath.Join(dir, "secret_key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret_key mode = %v, want 0600", info.Mode().Perm())
	}
	if strings.ContainsAny(key1, " \n") {
		t.Error("key contains whitespace")
	}
}
