package service

import (
	"context"
	"database/sql"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
)

// ChangeStatus moves a device to a new status, applying the
// lifecycle graph, the ready gate (with the stored inspection flag
// recomputed and synced first, as the Python version does), the
// reason rule, and the returned-unit reset.
func ChangeStatus(ctx context.Context, database *sql.DB, userID, deviceID int64, target, reason string) error {
	reason = pyStrip(reason)
	device, err := GetDevice(ctx, database, deviceID)
	if err != nil {
		return err
	}
	current := device.Status
	if !domain.Transitions[current][target] {
		return &InvalidTransitionError{Current: current, Target: target}
	}
	if target == "ready" {
		// Do not trust the stored flag: recompute it from the
		// checklist itself and sync the flag, so a stale flag can
		// never open (or wrongly close) the gate. The Python
		// version commits this sync even when the gate then
		// refuses the move, so it runs in its own transaction.
		err := db.Tx(ctx, database, func(tx *db.WriteTx) error {
			results, err := inspectionResultsTx(ctx, tx, deviceID)
			if err != nil {
				return err
			}
			state := domain.ComputeInspectionState(device.Kind, results)
			fresh := 0
			if state.Complete {
				fresh = 1
			}
			if fresh != device.InspectionDone {
				if _, err := tx.Exec(
					"UPDATE device SET inspection_done = ? WHERE id = ?",
					fresh, deviceID); err != nil {
					return err
				}
				device.InspectionDone = fresh
			}
			return nil
		})
		if err != nil {
			return err
		}
		if blockers := domain.ReadyBlockers(device); len(blockers) > 0 {
			return &BlockedError{Blockers: blockers}
		}
	}
	if domain.NeedsReason(current, target) && reason == "" {
		return ErrReasonRequired
	}
	return db.Tx(ctx, database, func(tx *db.WriteTx) error {
		if _, err := tx.Exec(
			"UPDATE device SET status = ?, updated_at = datetime('now') WHERE id = ?",
			target, deviceID); err != nil {
			return err
		}
		detail := domain.StatusLabel(current) + " → " + domain.StatusLabel(target)
		if reason != "" {
			detail += "（理由: " + reason + "）"
		}
		if target == "returned" {
			// A returned unit may hold the buyer's data and its
			// condition may have changed: the previous inspection
			// and erasure records no longer describe it.
			if _, err := tx.Exec(
				"DELETE FROM inspection WHERE device_id = ?", deviceID); err != nil {
				return err
			}
			if _, err := tx.Exec(
				"UPDATE device SET inspection_done = 0, data_erasure_at = '',"+
					" data_erasure_method = '', data_erasure_by = NULL WHERE id = ?",
				deviceID); err != nil {
				return err
			}
			detail += "（返品のため検査結果とデータ消去の記録をリセットしました）"
		}
		return addHistory(tx, deviceID, userID, "status", detail)
	})
}

func inspectionResultsTx(ctx context.Context, tx *db.WriteTx, deviceID int64) (map[string]string, error) {
	rows, err := tx.Query(
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
