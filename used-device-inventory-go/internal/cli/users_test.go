package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"used-device-inventory-go/internal/auth"
	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/service"
	"used-device-inventory-go/internal/web"
)

// Mirrors of the Python version's test_reset_password.py, plus an
// end-to-end check of design difference D4 (a reset ends existing
// web sessions).

// makeUserDB builds a database with an admin (admin-pass-1) and
// returns its path.
func makeUserDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "devices.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := db.Init(ctx, database); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.Hash("admin-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateUser(ctx, database, "admin", hash, true); err != nil {
		t.Fatal(err)
	}
	return path
}

func addUser(t *testing.T, dbPath, username, password string, isAdmin bool) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	hash, err := auth.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateUser(context.Background(), database,
		username, hash, isAdmin); err != nil {
		t.Fatal(err)
	}
}

// passwordWorks reports whether the password currently logs the
// user in (checked against the stored hash, as the login does).
func passwordWorks(t *testing.T, dbPath, username, password string) bool {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	user, err := service.UserByUsername(context.Background(), database, username)
	if err != nil {
		t.Fatal(err)
	}
	return auth.Check(user.PasswordHash, password)
}

func stubPasswords(t *testing.T, answers ...string) {
	t.Helper()
	original := readPassword
	next := 0
	readPassword = func(string) (string, error) {
		answer := answers[next]
		next++
		return answer, nil
	}
	t.Cleanup(func() { readPassword = original })
}

func TestResetLetsUserLogInWithNewPassword(t *testing.T) {
	path := makeUserDB(t)
	if err := resetPassword(path, "admin", "brand-new-pass"); err != nil {
		t.Fatal(err)
	}
	if passwordWorks(t, path, "admin", "admin-pass-1") {
		t.Error("old password still works after the reset")
	}
	if !passwordWorks(t, path, "admin", "brand-new-pass") {
		t.Error("new password does not work after the reset")
	}
}

func TestResetChangesOnlyThatUser(t *testing.T) {
	path := makeUserDB(t)
	addUser(t, path, "u1", "user-pass-1", false)
	if err := resetPassword(path, "u1", "another-pass-9"); err != nil {
		t.Fatal(err)
	}
	if !passwordWorks(t, path, "admin", "admin-pass-1") {
		t.Error("the admin's password changed too")
	}
	if !passwordWorks(t, path, "u1", "another-pass-9") {
		t.Error("u1's new password does not work")
	}
}

func TestShortPasswordRejectedAndNothingChanged(t *testing.T) {
	path := makeUserDB(t)
	err := resetPassword(path, "admin", "short")
	if err == nil || !strings.Contains(err.Error(), "パスワードは8文字以上にしてください。") {
		t.Fatalf("err = %v", err)
	}
	if !passwordWorks(t, path, "admin", "admin-pass-1") {
		t.Error("password changed despite the rejection")
	}
}

func TestUnknownUserIsAnError(t *testing.T) {
	path := makeUserDB(t)
	err := resetPassword(path, "nobody", "long-enough-1")
	if err == nil || !strings.Contains(err.Error(), "そのユーザーはいません: nobody") {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingDatabaseIsNotCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "none.db")
	err := resetPassword(path, "admin", "long-enough-1")
	if err == nil || !strings.Contains(err.Error(), "データベースが見つかりません") {
		t.Fatalf("reset err = %v", err)
	}
	if _, err := listUsers(path); err == nil {
		t.Error("listUsers on a missing database succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a database file was created")
	}
}

func TestListUsersShowsAdminFlag(t *testing.T) {
	path := makeUserDB(t)
	addUser(t, path, "staff", "staff-pass-1", false)
	users, err := listUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "admin" || !users[0].IsAdmin ||
		users[1].Username != "staff" || users[1].IsAdmin {
		t.Fatalf("users = %+v", users)
	}
	t.Setenv("DEVICE_DB", path)
	var out, errOut strings.Builder
	if code := Run([]string{"list-users"}, t.TempDir(), &out, &errOut); code != 0 {
		t.Fatalf("list-users exit = %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ユーザー一覧 (* は管理者):") ||
		!strings.Contains(out.String(), "  * admin") ||
		!strings.Contains(out.String(), "   staff") {
		t.Errorf("list output:\n%s", out.String())
	}
}

func TestRunMismatchedConfirmationChangesNothing(t *testing.T) {
	path := makeUserDB(t)
	t.Setenv("DEVICE_DB", path)
	stubPasswords(t, "new-pass-aaa", "new-pass-bbb")
	var out, errOut strings.Builder
	code := Run([]string{"reset-password", "admin"}, t.TempDir(), &out, &errOut)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "2回の入力が違います。何も変更していません。") {
		t.Errorf("stdout = %q", out.String())
	}
	if !passwordWorks(t, path, "admin", "admin-pass-1") {
		t.Error("password changed despite the mismatch")
	}
}

func TestRunResetPasswordSuccess(t *testing.T) {
	path := makeUserDB(t)
	t.Setenv("DEVICE_DB", path)
	stubPasswords(t, "new-pass-ok1", "new-pass-ok1")
	var out, errOut strings.Builder
	code := Run([]string{"reset-password", "admin"}, t.TempDir(), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "admin のパスワードを変更しました。") {
		t.Errorf("stdout = %q", out.String())
	}
	if !passwordWorks(t, path, "admin", "new-pass-ok1") {
		t.Error("new password does not work")
	}
}

func TestRunResetPasswordNeedsOneUsername(t *testing.T) {
	var out, errOut strings.Builder
	if code := Run([]string{"reset-password"}, t.TempDir(), &out, &errOut); code != 1 {
		t.Errorf("no-username exit = %d, want 1", code)
	}
	if code := Run([]string{"reset-password", "a", "b"}, t.TempDir(), &out, &errOut); code != 1 {
		t.Errorf("two-username exit = %d, want 1", code)
	}
}

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// D4 end to end: a user logged in on the web is logged out by a
// CLI password reset, because the session's password fingerprint
// no longer matches.
func TestRunResetInvalidatesWebSession(t *testing.T) {
	path := makeUserDB(t)
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	server, err := web.NewServer(database, "test-secret-key", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(
		*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(path string) (int, string, http.Header) {
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header
	}
	_, loginPage, _ := get("/login")
	match := csrfPattern.FindStringSubmatch(loginPage)
	if match == nil {
		t.Fatal("no CSRF token on the login page")
	}
	form := url.Values{"csrf_token": {match[1]}, "username": {"admin"},
		"password": {"admin-pass-1"}}
	resp, err := client.PostForm(ts.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	if status, _, _ := get("/"); status != http.StatusOK {
		t.Fatalf("GET / after login = %d", status)
	}

	t.Setenv("DEVICE_DB", path)
	stubPasswords(t, "brand-new-pass", "brand-new-pass")
	var out, errOut strings.Builder
	if code := Run([]string{"reset-password", "admin"},
		t.TempDir(), &out, &errOut); code != 0 {
		t.Fatalf("reset exit = %d, stderr %q", code, errOut.String())
	}

	status, _, header := get("/")
	if status != http.StatusFound ||
		!strings.HasPrefix(header.Get("Location"), "/login") {
		t.Errorf("GET / after the reset = %d %q, want a redirect to /login",
			status, header.Get("Location"))
	}
}
