package service

import (
	"context"
	"database/sql"
	"errors"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/util"
)

// Expense is one row of the expense table.
type Expense struct {
	ID          int64
	DeviceID    int64
	ExpenseDate string
	Category    string
	Amount      int64
	Note        string
	CreatedBy   *int64
	CreatedAt   string
}

// ListExpenses returns a device's expenses in id order.
func ListExpenses(ctx context.Context, database *sql.DB, deviceID int64) ([]Expense, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT id, device_id, expense_date, category, amount, note,"+
			" created_by, created_at FROM expense WHERE device_id = ?"+
			" ORDER BY id", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Expense
	for rows.Next() {
		var e Expense
		var createdBy sql.NullInt64
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.ExpenseDate, &e.Category,
			&e.Amount, &e.Note, &createdBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.CreatedBy = nullIntPtr(createdBy)
		out = append(out, e)
	}
	return out, rows.Err()
}

// InspectionRow is one stored inspection result.
type InspectionRow struct {
	ItemKey   string
	Result    string
	Note      string
	CheckedBy *int64
	CheckedAt string
}

// InspectionRows loads the stored inspection rows for one device,
// keyed by item key.
func InspectionRows(ctx context.Context, database *sql.DB, deviceID int64) (map[string]InspectionRow, error) {
	rows, err := database.QueryContext(ctx,
		"SELECT item_key, result, note, checked_by, checked_at"+
			" FROM inspection WHERE device_id = ?", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]InspectionRow{}
	for rows.Next() {
		var row InspectionRow
		var checkedBy sql.NullInt64
		if err := rows.Scan(&row.ItemKey, &row.Result, &row.Note,
			&checkedBy, &row.CheckedAt); err != nil {
			return nil, err
		}
		row.CheckedBy = nullIntPtr(checkedBy)
		out[row.ItemKey] = row
	}
	return out, rows.Err()
}

// AddExpense records one expense for a device. The amount text is
// parsed with Python int() semantics, as the Python version does.
func AddExpense(ctx context.Context, database *sql.DB, userID, deviceID int64, category, date, note, amountText string) error {
	if _, err := GetDevice(ctx, database, deviceID); err != nil {
		return err
	}
	date = pyStrip(date)
	note = pyStrip(note)
	amount, ok := util.ToInt(pyStrip(amountText))
	_, categoryOK := domain.ExpenseCategories[category]
	if !categoryOK || !ok || amount < 0 {
		return &ValidationError{Msg: "経費の種類と金額（0以上の数字）を入れてください。"}
	}
	return db.Tx(ctx, database, func(tx *db.WriteTx) error {
		if _, err := tx.Exec(
			"INSERT INTO expense (device_id, expense_date, category, amount, note, created_by)"+
				" VALUES (?, ?, ?, ?, ?, ?)",
			deviceID, date, category, amount, note, userID); err != nil {
			return err
		}
		detail := "経費追加: " + domain.ExpenseCategories[category] +
			" " + domain.FormatComma(amount) + "円"
		if note != "" {
			detail += "（" + note + "）"
		}
		return addHistory(tx, deviceID, userID, "expense", detail)
	})
}

// DeleteExpense removes one expense of a device; an expense that
// does not belong to the device is a 404, as in the Python version.
func DeleteExpense(ctx context.Context, database *sql.DB, userID, deviceID, expenseID int64) error {
	return db.Tx(ctx, database, func(tx *db.WriteTx) error {
		var category string
		var amount int64
		err := tx.QueryRow(
			"SELECT category, amount FROM expense WHERE id = ? AND device_id = ?",
			expenseID, deviceID).Scan(&category, &amount)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"DELETE FROM expense WHERE id = ?", expenseID); err != nil {
			return err
		}
		return addHistory(tx, deviceID, userID, "expense",
			"経費削除: "+domain.ExpenseCategories[category]+
				" "+domain.FormatComma(amount)+"円")
	})
}
