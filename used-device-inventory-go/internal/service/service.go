// Package service implements the use cases of used-device-inventory:
// one operation is one write transaction, and the observable
// behaviour (validation order, messages, history wording, side
// effects) mirrors the Python version's handlers.
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/util"
)

// pyStrip is Python's str.strip(), used for every form value and
// search text in the Python version.
func pyStrip(s string) string { return util.PyStrip(s) }

// ErrNotFound is returned when the requested device or expense does
// not exist (the Python version's 404).
var ErrNotFound = errors.New("not found")

// ErrDuplicateSerial is a device insert/update that hit the unique
// serial number constraint.
var ErrDuplicateSerial = errors.New("そのシリアル番号は既に登録されています。")

// ErrReasonRequired is a status change that needs a reason.
var ErrReasonRequired = errors.New("この変更には理由が必要です。")

// ValidationError carries a user-facing validation message, exactly
// as the Python version flashes it.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// InvalidTransitionError is a status change the lifecycle graph
// does not allow.
type InvalidTransitionError struct{ Current, Target string }

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("%s から %s へは進めません。",
		domain.StatusLabel(e.Current), domain.StatusLabel(e.Target))
}

// BlockedError is a move to 販売可能 refused by the ready gate.
type BlockedError struct{ Blockers []string }

func (e *BlockedError) Error() string {
	return "販売可能にできません: " + strings.Join(e.Blockers, " / ")
}

// LockedError is an edit refused by the ready/reserved lock. Values
// holds the parsed submission, so the web layer can re-render the
// form with what the user typed (as the Python version does).
type LockedError struct {
	Labels []string
	Values *domain.Device
}

func (e *LockedError) Error() string {
	return "販売可能・取り置きの間は " + strings.Join(e.Labels, "、") +
		" は変更できません。先に状態を戻してください。"
}

const deviceColumns = "id, kind, maker, model, serial_number, imei," +
	" storage, ram, color, battery_health, sim_status, activation_lock," +
	" network_restriction, cpu, gpu, os_name, battery_cycles," +
	" condition_rank, status, cosmetic_damage, accessories," +
	" inspection_done, data_erasure_at, data_erasure_method, data_erasure_by," +
	" purchase_date, purchase_source, purchase_price, planned_price," +
	" sale_date, sale_channel, sale_price, warranty_days, notes," +
	" created_by, created_at, updated_at"

func scanDevice(row interface{ Scan(...any) error }) (*domain.Device, error) {
	var d domain.Device
	var imei sql.NullString
	var batteryHealth, batteryCycles sql.NullInt64
	var purchasePrice, plannedPrice, salePrice, warrantyDays sql.NullInt64
	var erasureBy, createdBy sql.NullInt64
	err := row.Scan(
		&d.ID, &d.Kind, &d.Maker, &d.Model, &d.SerialNumber, &imei,
		&d.Storage, &d.RAM, &d.Color, &batteryHealth, &d.SIMStatus,
		&d.ActivationLock, &d.NetworkRestriction,
		&d.CPU, &d.GPU, &d.OSName, &batteryCycles,
		&d.ConditionRank, &d.Status, &d.CosmeticDamage, &d.Accessories,
		&d.InspectionDone, &d.DataErasureAt, &d.DataErasureMethod, &erasureBy,
		&d.PurchaseDate, &d.PurchaseSource, &purchasePrice, &plannedPrice,
		&d.SaleDate, &d.SaleChannel, &salePrice, &warrantyDays, &d.Notes,
		&createdBy, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if imei.Valid {
		d.IMEI = &imei.String
	}
	d.BatteryHealth = nullIntPtr(batteryHealth)
	d.BatteryCycles = nullIntPtr(batteryCycles)
	d.PurchasePrice = nullIntPtr(purchasePrice)
	d.PlannedPrice = nullIntPtr(plannedPrice)
	d.SalePrice = nullIntPtr(salePrice)
	d.WarrantyDays = nullIntPtr(warrantyDays)
	d.DataErasureBy = nullIntPtr(erasureBy)
	d.CreatedBy = nullIntPtr(createdBy)
	return &d, nil
}

func nullIntPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// GetDevice loads one device by id.
func GetDevice(ctx context.Context, database *sql.DB, id int64) (*domain.Device, error) {
	d, err := scanDevice(database.QueryRowContext(ctx,
		"SELECT "+deviceColumns+" FROM device WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func getDeviceTx(tx *db.WriteTx, id int64) (*domain.Device, error) {
	d, err := scanDevice(tx.QueryRow(
		"SELECT "+deviceColumns+" FROM device WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ExpenseTotal is the sum of a device's expenses (0 when none).
func ExpenseTotal(ctx context.Context, database *sql.DB, deviceID int64) (int64, error) {
	var total int64
	err := database.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(amount), 0) FROM expense WHERE device_id = ?",
		deviceID).Scan(&total)
	return total, err
}

// PhotoCount is the number of photos a device has.
func PhotoCount(ctx context.Context, database *sql.DB, deviceID int64) (int64, error) {
	var count int64
	err := database.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM device_photo WHERE device_id = ?",
		deviceID).Scan(&count)
	return count, err
}

// ListFilter is the device list's filter set.
type ListFilter struct {
	Query  string
	Kind   string
	Status string
}

// ListRow is a device plus the values the list and CSV compute.
type ListRow struct {
	Device       *domain.Device
	ExpenseTotal int64
	Profit       *int64
	PhotoCount   int64
}

// ListDevices mirrors the Python version's filtered_devices plus the
// per-row computations of the list page: the query text is stripped,
// LIKE wildcards in it are matched literally, kind/status filters
// apply only when they name a real value, newest first.
func ListDevices(ctx context.Context, database *sql.DB, filter ListFilter) ([]ListRow, error) {
	query := pyStrip(filter.Query)
	sqlText := "SELECT " + deviceColumns + " FROM device WHERE 1 = 1"
	var args []any
	if query != "" {
		esc := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query)
		sqlText += " AND (maker LIKE ? ESCAPE '\\' OR model LIKE ? ESCAPE '\\'" +
			" OR serial_number LIKE ? ESCAPE '\\' OR notes LIKE ? ESCAPE '\\')"
		like := "%" + esc + "%"
		args = append(args, like, like, like, like)
	}
	if _, ok := domain.Kinds[filter.Kind]; ok {
		sqlText += " AND kind = ?"
		args = append(args, filter.Kind)
	}
	if _, ok := domain.Statuses[filter.Status]; ok {
		sqlText += " AND status = ?"
		args = append(args, filter.Status)
	}
	sqlText += " ORDER BY id DESC"
	rows, err := database.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListRow
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		total, err := ExpenseTotal(ctx, database, d.ID)
		if err != nil {
			return nil, err
		}
		count, err := PhotoCount(ctx, database, d.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ListRow{Device: d, ExpenseTotal: total,
			Profit: domain.GrossProfit(d, total), PhotoCount: count})
	}
	return out, rows.Err()
}

// HistoryEntry is one history row with the acting user's name.
type HistoryEntry struct {
	ID        int64
	DeviceID  int64
	UserID    *int64
	Username  string
	Action    string
	Detail    string
	CreatedAt string
}

// History lists a device's history, newest first.
func History(ctx context.Context, database *sql.DB, deviceID int64) ([]HistoryEntry, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT h.id, h.device_id, h.user_id, COALESCE(u.username, ''),"+
			" h.action, h.detail, h.created_at FROM history h"+
			" LEFT JOIN user u ON u.id = h.user_id"+
			" WHERE h.device_id = ? ORDER BY h.id DESC", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var uid sql.NullInt64
		if err := rows.Scan(&e.ID, &e.DeviceID, &uid, &e.Username,
			&e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.UserID = nullIntPtr(uid)
		out = append(out, e)
	}
	return out, rows.Err()
}

// InspectionResults loads the recorded results (item key -> result)
// for one device.
func InspectionResults(ctx context.Context, database *sql.DB, deviceID int64) (map[string]string, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT item_key, result FROM inspection WHERE device_id = ?", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, result string
		if err := rows.Scan(&key, &result); err != nil {
			return nil, err
		}
		out[key] = result
	}
	return out, rows.Err()
}

// InspectionStateFor computes a device's inspection state from the
// stored results.
func InspectionStateFor(ctx context.Context, database *sql.DB, d *domain.Device) (domain.InspectionState, error) {
	results, err := InspectionResults(ctx, database, d.ID)
	if err != nil {
		return domain.InspectionState{}, err
	}
	return domain.ComputeInspectionState(d.Kind, results), nil
}

func addHistory(tx *db.WriteTx, deviceID, userID int64, action, detail string) error {
	_, err := tx.Exec(
		"INSERT INTO history (device_id, user_id, action, detail) VALUES (?, ?, ?, ?)",
		deviceID, userID, action, detail)
	return err
}

// isSerialDuplicate reports whether err is the unique-constraint
// failure on device.serial_number (the one expected insert/update
// failure, as in the Python version).
func isSerialDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(),
		"UNIQUE constraint failed: device.serial_number")
}
