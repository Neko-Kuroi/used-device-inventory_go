package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"used-device-inventory-go/internal/auth"
	"used-device-inventory-go/internal/db"
)

// These tests mirror the Python version's phase 1 auth tests and
// the login-next review tests, plus the design's own additions:
// CSRF on every POST and session invalidation on password change.

type testClient struct {
	t      *testing.T
	client *http.Client
	base   string
}

func newTestServer(t *testing.T) (*httptest.Server, *testClient) {
	t.Helper()
	ts, client, _ := newTestServerDirs(t)
	return ts, client
}

// newTestServerDirs also returns the instance directory, for tests
// that check files on disk (photos).
func newTestServerDirs(t *testing.T) (*httptest.Server, *testClient, string) {
	t.Helper()
	instanceDir := t.TempDir()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Init(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(database, "test-secret-key", instanceDir)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &testClient{t: t, base: ts.URL, client: &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	return ts, client, instanceDir
}

// doRaw sends a request with a raw body and optional headers.
func (c *testClient) doRaw(method, path, contentType string, body []byte, headers map[string]string) response {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: string(data),
		location: resp.Header.Get("Location"), cookies: resp.Cookies(),
		header: resp.Header}
}

type response struct {
	status   int
	body     string
	location string
	cookies  []*http.Cookie
	header   http.Header
}

func (c *testClient) do(method, path string, form url.Values) response {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, body: string(data),
		location: resp.Header.Get("Location"), cookies: resp.Cookies(),
		header: resp.Header}
}

func (c *testClient) get(path string) response { return c.do("GET", path, nil) }

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([0-9a-f]+)"`)

func (c *testClient) csrfFor(path string) string {
	c.t.Helper()
	resp := c.get(path)
	m := csrfRe.FindStringSubmatch(resp.body)
	if m == nil {
		c.t.Fatalf("no csrf token on %s (status %d)", path, resp.status)
	}
	return m[1]
}

// post submits a form with the CSRF token taken from the given page.
func (c *testClient) post(path, tokenPage string, form url.Values) response {
	c.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf_token", c.csrfFor(tokenPage))
	return c.do("POST", path, form)
}

func (c *testClient) setupAdmin() {
	c.t.Helper()
	resp := c.post("/setup", "/setup", url.Values{
		"username": {"admin"}, "password": {"admin-pass-1"}})
	if resp.status != http.StatusFound || resp.location != "/" {
		c.t.Fatalf("setup: status %d location %q", resp.status, resp.location)
	}
}

func (c *testClient) logout() {
	c.t.Helper()
	resp := c.post("/logout", "/", nil)
	if resp.status != http.StatusFound || resp.location != "/login" {
		c.t.Fatalf("logout: status %d location %q", resp.status, resp.location)
	}
}

func (c *testClient) login(username, password, next string) response {
	c.t.Helper()
	path := "/login"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	return c.post(path, "/login", url.Values{
		"username": {username}, "password": {password}})
}

func TestRootRedirectsToSetupWhenNoUsers(t *testing.T) {
	_, c := newTestServer(t)
	resp := c.get("/")
	if resp.status != http.StatusFound || !strings.Contains(resp.location, "/setup") {
		t.Errorf("GET / = %d %q, want 302 to /setup", resp.status, resp.location)
	}
}

func TestSetupCreatesAdminAndLogsIn(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	home := c.get("/")
	if home.status != http.StatusOK {
		t.Fatalf("GET / after setup = %d", home.status)
	}
	if !strings.Contains(home.body, "管理者を作成しました。") {
		t.Error("setup flash not shown on the landing page")
	}
	// The flash is consumed: a second view does not repeat it.
	home = c.get("/")
	if strings.Contains(home.body, "管理者を作成しました。") {
		t.Error("flash shown twice")
	}
	// Setup is closed once a user exists.
	resp := c.get("/setup")
	if resp.status != http.StatusFound || resp.location != "/login" {
		t.Errorf("GET /setup with users = %d %q", resp.status, resp.location)
	}
}

func TestSetupRejectsShortPassword(t *testing.T) {
	_, c := newTestServer(t)
	resp := c.post("/setup", "/setup", url.Values{
		"username": {"a"}, "password": {"short"}})
	if resp.status != http.StatusOK {
		t.Fatalf("short password setup = %d, want 200", resp.status)
	}
	if !strings.Contains(resp.body, "ユーザー名と、8文字以上のパスワードを入れてください。") {
		t.Error("validation flash missing")
	}
	// Still no users: /login bounces to /setup.
	resp = c.get("/login")
	if resp.status != http.StatusFound || resp.location != "/setup" {
		t.Errorf("GET /login = %d %q, want 302 to /setup", resp.status, resp.location)
	}
	// The length rule counts characters, not bytes (Python len()).
	resp = c.post("/setup", "/setup", url.Values{
		"username": {"a"}, "password": {"あああああああ"}}) // 7 characters
	if resp.status != http.StatusOK {
		t.Errorf("7-character password accepted: %d", resp.status)
	}
	resp = c.post("/setup", "/setup", url.Values{
		"username": {"a"}, "password": {"ああああああああ"}}) // 8 characters
	if resp.status != http.StatusFound {
		t.Errorf("8-character password rejected: %d", resp.status)
	}
}

func TestLoginRequired(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.logout()
	resp := c.get("/")
	if resp.status != http.StatusFound ||
		!strings.Contains(resp.location, "/login") ||
		!strings.Contains(resp.location, "next=") {
		t.Errorf("GET / logged out = %d %q", resp.status, resp.location)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.logout()
	resp := c.login("admin", "wrong-password", "")
	if resp.status != http.StatusOK {
		t.Fatalf("wrong password login = %d, want 200", resp.status)
	}
	if !strings.Contains(resp.body, "ユーザー名かパスワードが違います。") {
		t.Error("failure flash missing")
	}
	if resp := c.get("/"); resp.status != http.StatusFound {
		t.Errorf("GET / after failed login = %d, want 302", resp.status)
	}
}

func TestLoginNextRules(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.logout()
	// An absolute URL is ignored (open-redirect guard).
	resp := c.login("admin", "admin-pass-1", "https://evil.example/")
	if resp.location != "/" {
		t.Errorf("external next -> %q, want /", resp.location)
	}
	c.logout()
	resp = c.login("admin", "admin-pass-1", "//evil.example/")
	if resp.location != "/" {
		t.Errorf("scheme-relative next -> %q, want /", resp.location)
	}
	c.logout()
	// An internal path still works.
	resp = c.login("admin", "admin-pass-1", "/admin/users")
	if resp.location != "/admin/users" {
		t.Errorf("internal next -> %q, want /admin/users", resp.location)
	}
}

func TestCSRFRequiredOnEveryPost(t *testing.T) {
	_, c := newTestServer(t)
	// No token at all.
	resp := c.do("POST", "/setup", url.Values{
		"username": {"admin"}, "password": {"admin-pass-1"}})
	if resp.status != http.StatusBadRequest {
		t.Errorf("POST without token = %d, want 400", resp.status)
	}
	// A bogus token.
	form := url.Values{"username": {"admin"}, "password": {"admin-pass-1"},
		"csrf_token": {"0000deadbeef"}}
	resp = c.do("POST", "/setup", form)
	if resp.status != http.StatusBadRequest {
		t.Errorf("POST with bogus token = %d, want 400", resp.status)
	}
	// No user was created by the rejected posts.
	if resp := c.get("/"); !strings.Contains(resp.location, "/setup") {
		t.Errorf("GET / = %q, want redirect to /setup", resp.location)
	}
	// The login form carries the token field.
	c.setupAdmin()
	c.logout()
	if body := c.get("/login").body; !strings.Contains(body, `name="csrf_token"`) {
		t.Error("login form has no csrf_token field")
	}
}

func TestTamperedSessionCookie(t *testing.T) {
	ts, c := newTestServer(t)
	c.setupAdmin()
	// Replace the session cookie with a corrupted value.
	u, _ := url.Parse(ts.URL)
	for _, cookie := range c.client.Jar.Cookies(u) {
		if cookie.Name == "session" {
			cookie.Value = cookie.Value[:len(cookie.Value)-4] + "AAAA"
			c.client.Jar.SetCookies(u, []*http.Cookie{cookie})
		}
	}
	resp := c.get("/")
	if resp.status != http.StatusFound || !strings.Contains(resp.location, "/login") {
		t.Errorf("GET / with tampered cookie = %d %q, want 302 to /login",
			resp.status, resp.location)
	}
}

func TestPasswordChangeInvalidatesSessions(t *testing.T) {
	ts, c := newTestServer(t)
	c.setupAdmin()
	if resp := c.get("/"); resp.status != http.StatusOK {
		t.Fatalf("GET / before reset = %d", resp.status)
	}
	// Simulate a password reset: the stored hash changes.
	srv := ts.Config.Handler.(*Server)
	newHash, err := auth.Hash("brand-new-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.ExecContext(context.Background(),
		"UPDATE user SET password_hash = ? WHERE username = 'admin'",
		newHash); err != nil {
		t.Fatal(err)
	}
	resp := c.get("/")
	if resp.status != http.StatusFound || !strings.Contains(resp.location, "/login") {
		t.Errorf("GET / after password change = %d %q, want 302 to /login",
			resp.status, resp.location)
	}
	// The new password logs in.
	resp = c.login("admin", "brand-new-password", "")
	if resp.status != http.StatusFound {
		t.Errorf("login with new password = %d", resp.status)
	}
}

func TestDeletedUserSessionEnds(t *testing.T) {
	ts, c := newTestServer(t)
	c.setupAdmin()
	srv := ts.Config.Handler.(*Server)
	if _, err := srv.db.ExecContext(context.Background(),
		"DELETE FROM user"); err != nil {
		t.Fatal(err)
	}
	// With no users left, the guard sends us to setup, as in the
	// Python version.
	resp := c.get("/")
	if resp.status != http.StatusFound || resp.location != "/setup" {
		t.Errorf("GET / after user deletion = %d %q", resp.status, resp.location)
	}
}

func TestAdminUsers(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	// Add a non-admin user.
	resp := c.post("/admin/users", "/admin/users", url.Values{
		"username": {"staff"}, "password": {"staff-pass-1"}})
	if resp.status != http.StatusOK ||
		!strings.Contains(resp.body, "ユーザーを追加しました。") ||
		!strings.Contains(resp.body, "staff") {
		t.Errorf("add user = %d, body missing flash or user", resp.status)
	}
	// Duplicate name.
	resp = c.post("/admin/users", "/admin/users", url.Values{
		"username": {"staff"}, "password": {"staff-pass-2"}})
	if !strings.Contains(resp.body, "そのユーザー名は既に使われています。") {
		t.Error("duplicate flash missing")
	}
	// Short password.
	resp = c.post("/admin/users", "/admin/users", url.Values{
		"username": {"third"}, "password": {"short"}})
	if !strings.Contains(resp.body, "ユーザー名と、8文字以上のパスワードを入れてください。") {
		t.Error("short-password flash missing")
	}
	// The new user can log in but is not an admin.
	c.logout()
	resp = c.login("staff", "staff-pass-1", "")
	if resp.status != http.StatusFound {
		t.Fatalf("staff login = %d", resp.status)
	}
	resp = c.get("/admin/users")
	if resp.status != http.StatusForbidden {
		t.Errorf("non-admin GET /admin/users = %d, want 403", resp.status)
	}
	// Logged out entirely: redirect to login.
	c.logout()
	resp = c.get("/admin/users")
	if resp.status != http.StatusFound || !strings.Contains(resp.location, "/login") {
		t.Errorf("logged-out GET /admin/users = %d %q", resp.status, resp.location)
	}
}

func TestLogoutRequiresPost(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	resp := c.get("/logout")
	if resp.status != http.StatusMethodNotAllowed {
		t.Errorf("GET /logout = %d, want 405", resp.status)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	_, c := newTestServer(t)
	resp := c.post("/setup", "/setup", url.Values{
		"username": {"admin"}, "password": {"admin-pass-1"}})
	var sessionCookie *http.Cookie
	for _, cookie := range resp.cookies {
		if cookie.Name == "session" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie set by setup")
	}
	if !sessionCookie.HttpOnly || sessionCookie.Path != "/" ||
		sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie attrs = %+v", sessionCookie)
	}
	// An untouched session re-issues no cookie. (Visit / first to
	// consume the setup flash; consuming a flash does modify the
	// session, as in Flask.)
	c.get("/")
	resp = c.get("/admin/users")
	for _, cookie := range resp.cookies {
		if cookie.Name == "session" {
			t.Error("unmodified request re-issued the session cookie")
		}
	}
}
