package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"used-device-inventory-go/internal/db"
)

// User is one row of the user table.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    string
}

// ErrDuplicateUsername is a user insert that hit the unique
// username constraint.
var ErrDuplicateUsername = errors.New("そのユーザー名は既に使われています。")

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var isAdmin int
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &isAdmin, &u.CreatedAt); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	return &u, nil
}

const userColumns = "id, username, password_hash, is_admin, created_at"

// CountUsers returns how many users exist (0 means setup is open).
func CountUsers(ctx context.Context, database *sql.DB) (int64, error) {
	var n int64
	err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM user").Scan(&n)
	return n, err
}

// UserByID loads one user.
func UserByID(ctx context.Context, database *sql.DB, id int64) (*User, error) {
	u, err := scanUser(database.QueryRowContext(ctx,
		"SELECT "+userColumns+" FROM user WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UserByUsername loads one user by name.
func UserByUsername(ctx context.Context, database *sql.DB, username string) (*User, error) {
	u, err := scanUser(database.QueryRowContext(ctx,
		"SELECT "+userColumns+" FROM user WHERE username = ?", username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ListUsers returns all users in id order.
func ListUsers(ctx context.Context, database *sql.DB) ([]*User, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT "+userColumns+" FROM user ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser inserts a user with an already-computed password hash.
func CreateUser(ctx context.Context, database *sql.DB, username, passwordHash string, isAdmin bool) (int64, error) {
	var id int64
	err := db.Tx(ctx, database, func(tx *db.WriteTx) error {
		admin := 0
		if isAdmin {
			admin = 1
		}
		res, err := tx.Exec(
			"INSERT INTO user (username, password_hash, is_admin) VALUES (?, ?, ?)",
			username, passwordHash, admin)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: user.username") {
				return ErrDuplicateUsername
			}
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}
