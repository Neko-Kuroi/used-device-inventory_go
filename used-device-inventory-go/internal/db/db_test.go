package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T, name string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, src, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

func schemaDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx,
		"SELECT type, name, tbl_name, sql FROM sqlite_master"+
			" WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	out.WriteString("user_version=" + intString(version) + "\n")
	for rows.Next() {
		var typ, name, tbl string
		var sqlText sql.NullString
		if err := rows.Scan(&typ, &name, &tbl, &sqlText); err != nil {
			t.Fatal(err)
		}
		out.WriteString(typ + "|" + name + "|" + tbl + "|" + sqlText.String + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func intString(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// A database created by Go Init must have exactly the schema of a
// database created by the Python version.
func TestInitMatchesPythonSchema(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t, "fresh.db")
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "python_schema.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := schemaDump(t, db)
	if got != string(want) {
		t.Errorf("schema mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The Python version's own database opens, reports version 5, keeps
// its rows, and Init is a no-op on it.
func TestOpenPythonDatabase(t *testing.T) {
	ctx := context.Background()
	path := copyFixture(t, "python_v5.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, err := UserVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 5 {
		t.Fatalf("user_version = %d, want 5", v)
	}
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	var username string
	var isAdmin int
	err = db.QueryRowContext(ctx,
		"SELECT username, is_admin FROM user WHERE username = 'admin'").
		Scan(&username, &isAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if username != "admin" || isAdmin != 1 {
		t.Fatalf("user row = %q/%d", username, isAdmin)
	}
}

// A v1 database gains the added columns and version 5, mirroring the
// Python version's migration test.
func TestMigrateV1(t *testing.T) {
	ctx := context.Background()
	path := copyFixture(t, "v1.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	cols := map[string]bool{}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(device)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	for _, col := range AddedColumns {
		if !cols[col.Name] {
			t.Errorf("column %s missing after migration", col.Name)
		}
	}
	v, err := UserVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 5 {
		t.Fatalf("user_version = %d, want 5", v)
	}
}

// Intentional difference from the Python version: a newer database
// keeps its version instead of being rewritten to 5.
func TestUserVersionNotLowered(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t, "newer.db")
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	v, err := UserVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 99 {
		t.Fatalf("user_version = %d, want 99 (not lowered)", v)
	}
}

func TestTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t, "tx.db")
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	err := Tx(ctx, db, func(tx *WriteTx) error {
		_, err := tx.Exec(
			"INSERT INTO user (username, password_hash, is_admin) VALUES ('a', 'h', 0)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = Tx(ctx, db, func(tx *WriteTx) error {
		if _, err := tx.Exec(
			"INSERT INTO user (username, password_hash, is_admin) VALUES ('b', 'h', 0)"); err != nil {
			return err
		}
		return errForced
	})
	if err != errForced {
		t.Fatalf("Tx error = %v, want the forced error", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want 1 (second insert rolled back)", count)
	}
}

var errForced = &forcedError{}

type forcedError struct{}

func (*forcedError) Error() string { return "forced" }

// While another connection holds the write lock, Tx waits (busy
// timeout plus retry) instead of failing at once.
func TestTxWaitsOutTheWriteLock(t *testing.T) {
	ctx := context.Background()
	db, path := openTemp(t, "busy.db")
	if err := Init(ctx, db); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- Tx(ctx, db, func(tx *WriteTx) error {
			_, err := tx.Exec(
				"INSERT INTO user (username, password_hash, is_admin) VALUES ('c', 'h', 0)")
			return err
		})
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Tx after lock release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Tx did not finish after the lock was released")
	}
}
