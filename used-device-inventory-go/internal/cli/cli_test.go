package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.txt"),
		[]byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := Resolve(dir, "")
	want := filepath.Join(dir, "instance", "devices.db")
	if cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "# comment\naddr=100.64.0.2:5000\nother=x\n")

	if cfg := Resolve(dir, ""); cfg.Addr != "100.64.0.2:5000" {
		t.Errorf("config.txt addr: got %q", cfg.Addr)
	}
	t.Setenv("DEVICE_ADDR", "100.64.0.3:5000")
	if cfg := Resolve(dir, ""); cfg.Addr != "100.64.0.3:5000" {
		t.Errorf("env should beat config.txt: got %q", cfg.Addr)
	}
	if cfg := Resolve(dir, "100.64.0.4:5000"); cfg.Addr != "100.64.0.4:5000" {
		t.Errorf("flag should beat env: got %q", cfg.Addr)
	}
}

func TestResolveDBPathFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEVICE_DB", "/somewhere/else.db")
	if cfg := Resolve(dir, ""); cfg.DBPath != "/somewhere/else.db" {
		t.Errorf("DBPath = %q, want the DEVICE_DB value", cfg.DBPath)
	}
}

// Startup preparation creates instance/, the database
// (user_version 5) and the secret key under the base dir, and a
// second run reuses them.
func TestPrepareFoundation(t *testing.T) {
	dir := t.TempDir()
	cfg, database, secretKey, err := Prepare(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := os.Stat(filepath.Join(dir, "instance", "devices.db")); err != nil {
		t.Errorf("database not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "instance", "secret_key")); err != nil {
		t.Errorf("secret key not created: %v", err)
	}
	if secretKey == "" || cfg.DBPath == "" {
		t.Errorf("cfg/secret = %q / %q", cfg.DBPath, secretKey)
	}
	cfg2, database2, secretKey2, err := Prepare(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer database2.Close()
	if secretKey2 != secretKey || cfg2.DBPath != cfg.DBPath {
		t.Error("second Prepare did not reuse the existing setup")
	}
}

func TestRunHelpAndUnknown(t *testing.T) {
	var out, errOut strings.Builder
	if code := Run([]string{"help"}, t.TempDir(), &out, &errOut); code != 0 {
		t.Errorf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "reset-password") {
		t.Error("usage does not mention reset-password")
	}
	if code := Run([]string{"bogus"}, t.TempDir(), &out, &errOut); code != 1 {
		t.Errorf("unknown command exit = %d, want 1", code)
	}
}
