package cli

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"used-device-inventory-go/internal/auth"
	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/service"
)

// The user commands are the Go counterparts of the Python version's
// reset_password.py. They work on an existing database only: like
// the Python version, they refuse to create one by accident (a
// mistyped path must not silently start an empty database).

// openExistingDB opens the database at dbPath, or reports it
// missing with the Python version's message.
func openExistingDB(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("データベースが見つかりません: %s", dbPath)
	}
	return db.Open(dbPath)
}

func listUsers(dbPath string) ([]*service.User, error) {
	database, err := openExistingDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	return service.ListUsers(context.Background(), database)
}

// resetPassword replaces one user's password hash. The checks and
// messages follow the Python version: minimum length first (in
// characters, as Python's len counts them), then the database must
// exist, then the user must exist.
//
// Design difference D4: changing the hash also ends the user's
// existing web sessions, because the session carries a fingerprint
// of the hash (see the web package). The Python version leaves
// sessions logged in.
func resetPassword(dbPath, username, password string) error {
	if utf8.RuneCountInString(password) < auth.MinLength {
		return fmt.Errorf("パスワードは%d文字以上にしてください。", auth.MinLength)
	}
	database, err := openExistingDB(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	hash, err := auth.Hash(password)
	if err != nil {
		return err
	}
	result, err := database.ExecContext(context.Background(),
		"UPDATE user SET password_hash = ? WHERE username = ?",
		hash, username)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("そのユーザーはいません: %s", username)
	}
	return nil
}

func runListUsers(baseDir string, stdout, stderr io.Writer) int {
	cfg := Resolve(baseDir, "")
	users, err := listUsers(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "ユーザー一覧 (* は管理者):")
	for _, user := range users {
		mark := " "
		if user.IsAdmin {
			mark = "*"
		}
		fmt.Fprintf(stdout, "  %s %s\n", mark, user.Username)
	}
	fmt.Fprintln(stdout, "パスワードを作り直す: inventory reset-password <ユーザー名>")
	return 0
}

func runResetPassword(args []string, baseDir string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: inventory reset-password <ユーザー名>")
		return 1
	}
	username := args[0]
	password, err := readPassword("新しいパスワード: ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	again, err := readPassword("もう一度: ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if password != again {
		fmt.Fprintln(stdout, "2回の入力が違います。何も変更していません。")
		return 1
	}
	cfg := Resolve(baseDir, "")
	if err := resetPassword(cfg.DBPath, username, password); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s のパスワードを変更しました。\n", username)
	fmt.Fprintln(stdout, "ログイン中のセッションは無効になります。次に開いたときは、新しいパスワードでログインし直してください。")
	return 0
}

// readPassword asks for one password without echoing it, as
// Python's getpass does. It is a variable so tests can substitute
// scripted answers; the real implementation is platform-specific
// (password_linux.go / password_windows.go / password_other.go).
var readPassword = readPasswordFromTerminal

// stdinReader is shared by the password readers: a fresh buffered
// reader per prompt could swallow the next answer when the input
// is a pipe.
var stdinReader = bufio.NewReader(os.Stdin)

// readPasswordLine reads one line from stdin (the fallback when no
// terminal is available, and the reading half of the terminal
// implementations). Only the trailing newline is stripped.
func readPasswordLine() (string, error) {
	line, err := stdinReader.ReadString('\n')
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}
