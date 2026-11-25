package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"used-device-inventory-go/internal/db"
)

// csvServer builds a web server over a copy of the populated
// golden database from the service tests.
func csvServer(t *testing.T) *testClient {
	t.Helper()
	src := filepath.Join("..", "service", "testdata", "python_populated.db")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "golden.db")
	if err := os.WriteFile(dbPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Init(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(database, "test-secret-key", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &testClient{t: t, base: ts.URL, client: &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// The Go export must equal the Python version's real output for
// the same database, byte for byte.
func TestCSVMatchesPythonOutput(t *testing.T) {
	c := csvServer(t)
	c.login("admin", "admin-pass-1", "")
	for name, path := range map[string]string{
		"csv_all.csv":      "/export.csv",
		"csv_kind_pc.csv":  "/export.csv?kind=pc",
		"csv_q_iphone.csv": "/export.csv?q=iPhone",
	} {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		resp := c.doRaw("GET", path, "", nil, nil)
		if resp.status != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.status)
		}
		if !bytes.Equal([]byte(resp.body), want) {
			t.Errorf("CSV for %s differs from the Python output:\n got: %q\nwant: %q",
				path, resp.body, string(want))
		}
		if ct := resp.header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
			t.Errorf("content type = %q", ct)
		}
		if cd := resp.header.Get("Content-Disposition"); cd != "attachment; filename=devices.csv" {
			t.Errorf("content disposition = %q", cd)
		}
	}
}

func TestCSVRequiresLogin(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.logout()
	resp := c.get("/export.csv")
	if resp.status != http.StatusFound ||
		!strings.HasPrefix(resp.location, "/login") {
		t.Errorf("logged-out CSV = %d %q", resp.status, resp.location)
	}
}

// The mirror of the review test: formula-like text is neutralized,
// a negative profit (an integer) is left alone.
func TestCSVFormulaInjection(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(map[string]string{
		"maker": "=SUM(A1:A2)", "notes": "=2+5",
		"sale_price": "20000", "sale_date": "2026-11-12",
	})
	resp := c.doRaw("GET", "/export.csv", "", nil, nil)
	body := resp.body
	if !strings.Contains(body, "'=SUM(A1:A2)") {
		t.Error("formula maker not neutralized")
	}
	if !strings.Contains(body, "'=2+5") {
		t.Error("formula notes not neutralized")
	}
	if !strings.Contains(body, ",-10000,") { // 20000-30000, an integer
		t.Error("negative profit missing or altered")
	}
	var _ = url.Values{}
}
