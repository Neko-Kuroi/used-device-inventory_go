// Package db owns the SQLite connection, the write transaction, and
// schema initialisation. The schema itself is generated from the
// Python version (see migrations.go); the rules here mirror the
// Python version's behaviour, with the intentional differences noted
// in the design (user_version is never lowered, writes go through
// BEGIN IMMEDIATE).
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
)

// Open opens the database file at path. Every pooled connection gets
// foreign_keys=ON and busy_timeout=5000 (the same wait the Python
// version's sqlite3 default gives). The journal mode is left at the
// SQLite default (DELETE): backups stay a single file copy, as
// HOW_TO_USE.md promises.
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	return db, nil
}

// IsBusy reports whether err is a SQLite busy/locked error.
func IsBusy(err error) bool {
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		code := serr.Code()
		return code == 5 || code == 6 // SQLITE_BUSY, SQLITE_LOCKED
	}
	return false
}

// UserVersion reads PRAGMA user_version.
func UserVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// Init brings the database to the current schema, like the Python
// version's init_db: create tables and indexes that are missing, add
// the later device columns that are missing, and record the schema
// version. One difference: a database whose user_version is already
// newer than SchemaVersion keeps its version (the Python version
// rewrites it to its own, which would un-mark a newer database).
func Init(ctx context.Context, db *sql.DB) error {
	return Tx(ctx, db, func(tx *WriteTx) error {
		for _, stmt := range SchemaStatements {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("schema statement: %w", err)
			}
		}
		existing := map[string]bool{}
		rows, err := tx.Query("PRAGMA table_info(device)")
		if err != nil {
			return err
		}
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				return err
			}
			existing[name] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, col := range AddedColumns {
			if !existing[col.Name] {
				stmt := fmt.Sprintf("ALTER TABLE device ADD COLUMN %s %s", col.Name, col.Decl)
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("add column %s: %w", col.Name, err)
				}
			}
		}
		var version int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version < SchemaVersion {
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
				return err
			}
		}
		return nil
	})
}

// WriteTx is a single write transaction on a dedicated connection. All
// writes in the program go through it (the Python version uses
// BEGIN IMMEDIATE for the photo cap; here it covers every write, so
// check-then-write sequences cannot interleave).
type WriteTx struct {
	conn *sql.Conn
	ctx  context.Context
}

func (tx *WriteTx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.conn.ExecContext(tx.ctx, query, args...)
}

func (tx *WriteTx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.conn.QueryContext(tx.ctx, query, args...)
}

func (tx *WriteTx) QueryRow(query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(tx.ctx, query, args...)
}

// Tx runs fn inside a BEGIN IMMEDIATE transaction. If fn returns an
// error the transaction is rolled back. When SQLite reports busy,
// the whole unit is retried with exponential backoff (the file is
// shared with the Python version and with the CLI, so brief
// contention is expected and should not surface as an error).
func Tx(ctx context.Context, db *sql.DB, fn func(*WriteTx) error) error {
	var lastErr error
	backoff := 25 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 500*time.Millisecond {
				backoff = 500 * time.Millisecond
			}
		}
		err := txOnce(ctx, db, fn)
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsBusy(err) {
			return err
		}
	}
	return lastErr
}

func txOnce(ctx context.Context, db *sql.DB, fn func(*WriteTx) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	tx := &WriteTx{conn: conn, ctx: ctx}
	if err := fn(tx); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	return nil
}
