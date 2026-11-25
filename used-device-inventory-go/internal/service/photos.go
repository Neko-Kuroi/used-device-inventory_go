package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
)

// Photo is one row of the device_photo table.
type Photo struct {
	ID        int64
	DeviceID  int64
	Filename  string
	Kind      string
	CreatedBy *int64
	CreatedAt string
}

// ListPhotos returns a device's photos in id order.
func ListPhotos(ctx context.Context, database *sql.DB, deviceID int64) ([]Photo, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT id, device_id, filename, kind, created_by, created_at"+
			" FROM device_photo WHERE device_id = ? ORDER BY id", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		var p Photo
		var createdBy sql.NullInt64
		if err := rows.Scan(&p.ID, &p.DeviceID, &p.Filename, &p.Kind,
			&createdBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.CreatedBy = nullIntPtr(createdBy)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPhoto loads one photo of a device (a photo of another device
// is a 404, as in the Python version).
func GetPhoto(ctx context.Context, database *sql.DB, deviceID, photoID int64) (*Photo, error) {
	var p Photo
	var createdBy sql.NullInt64
	err := database.QueryRowContext(ctx,
		"SELECT id, device_id, filename, kind, created_by, created_at"+
			" FROM device_photo WHERE id = ? AND device_id = ?",
		photoID, deviceID).Scan(&p.ID, &p.DeviceID, &p.Filename, &p.Kind,
		&createdBy, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedBy = nullIntPtr(createdBy)
	return &p, nil
}

// photoExtGate applies the extension-first rule: the lowercased
// extension of the uploaded name (jpeg normalized to jpg) must be
// one of the known ones. It returns the normalized extension.
func photoExtGate(filename string) (string, bool) {
	ext := ""
	if i := strings.LastIndex(filename, "."); i >= 0 {
		ext = strings.ToLower(filename[i+1:])
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	switch ext {
	case "jpg", "png", "webp", "heic", "heif":
		return ext, true
	}
	return "", false
}

// AddPhoto validates and stores one photo. The validation order and
// messages are the Python version's: extension first, then the
// count, then the size, then the content (with the HEIC rules).
// photosDir is the instance photos directory (<instance>/photos).
func AddPhoto(ctx context.Context, database *sql.DB, photosDir string, userID, deviceID int64, kind, filename string, data []byte) error {
	if _, err := GetDevice(ctx, database, deviceID); err != nil {
		return err
	}
	nameExt, ok := photoExtGate(filename)
	if !ok {
		return &ValidationError{Msg: "写真のファイル名は .jpg / .jpeg / .png / .webp / .heic / .heif のものだけです。"}
	}
	count, err := PhotoCount(ctx, database, deviceID)
	if err != nil {
		return err
	}
	if count >= domain.MaxPhotosPerDevice {
		return &ValidationError{Msg: "写真は1台あたり5枚までです。"}
	}
	if len(data) > domain.MaxPhotoBytes {
		return &ValidationError{Msg: "写真は1枚5MBまでです。"}
	}
	sniffed := domain.SniffPhotoExt(data)
	if domain.LooksLikeHEIC(data) {
		return &ValidationError{Msg: "写真が HEIC 形式でした。このプログラムでは扱えないので、JPEG に変換してから送ってください。"}
	}
	accepted := false
	if nameExt == "heic" || nameExt == "heif" {
		accepted = sniffed == "jpg"
	} else {
		accepted = sniffed == nameExt
	}
	if !accepted {
		return &ValidationError{Msg: "写真は JPEG / PNG / WebP のファイルだけです（拡張子と中身が合っている必要があります）。"}
	}
	stored := randomHex(16) + "." + sniffed
	dir := filepath.Join(photosDir, intToStr(deviceID))
	path := filepath.Join(dir, stored)
	return db.Tx(ctx, database, func(tx *db.WriteTx) error {
		// The count check above is only a fast path: the write
		// transaction serializes adds, so the limit cannot be
		// slipped past by simultaneous uploads.
		var inside int64
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM device_photo WHERE device_id = ?",
			deviceID).Scan(&inside); err != nil {
			return err
		}
		if inside >= domain.MaxPhotosPerDevice {
			return &ValidationError{Msg: "写真は1台あたり5枚までです。"}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO device_photo (device_id, filename, kind, created_by)"+
				" VALUES (?, ?, ?, ?)", deviceID, stored, kind, userID); err != nil {
			os.Remove(path)
			return err
		}
		if err := addHistory(tx, deviceID, userID, "photo",
			"写真追加: "+domain.PhotoKinds[kind]); err != nil {
			os.Remove(path)
			return err
		}
		return nil
	})
}

// DeletePhoto removes a photo's row and file and records it.
func DeletePhoto(ctx context.Context, database *sql.DB, photosDir string, userID, deviceID, photoID int64) error {
	photo, err := GetPhoto(ctx, database, deviceID, photoID)
	if err != nil {
		return err
	}
	err = db.Tx(ctx, database, func(tx *db.WriteTx) error {
		if _, err := tx.Exec("DELETE FROM device_photo WHERE id = ?", photoID); err != nil {
			return err
		}
		return addHistory(tx, deviceID, userID, "photo",
			"写真削除: "+domain.PhotoKinds[photo.Kind])
	})
	if err != nil {
		return err
	}
	// The file goes too; a missing file is not an error (the
	// Python version tolerates FileNotFoundError here).
	err = os.Remove(filepath.Join(photosDir, intToStr(deviceID), photo.Filename))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func intToStr(n int64) string { return strconv.FormatInt(n, 10) }
