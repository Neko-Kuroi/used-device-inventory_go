package service

import (
	"context"
	"database/sql"
	"strings"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
)

// InspectionInput is one checklist item's submitted values.
type InspectionInput struct {
	Result string // "pass", "fail", "na", or "" to clear
	Note   string
}

// InspectionOutcome reports what a save did.
type InspectionOutcome struct {
	State   domain.InspectionState
	Demoted bool // the device was sent back to 検査中 automatically
}

// SaveInspection stores checklist results for a device: a known
// result upserts the row, an empty result deletes it, anything else
// is ignored (the Python version's semantics). The inspection flag
// is recomputed, the save is recorded in history, and a device on
// sale whose completeness broke is demoted to 検査中.
func SaveInspection(ctx context.Context, database *sql.DB, userID, deviceID int64, inputs map[string]InspectionInput) (InspectionOutcome, error) {
	var outcome InspectionOutcome
	err := db.Tx(ctx, database, func(tx *db.WriteTx) error {
		device, err := getDeviceTx(tx, deviceID)
		if err != nil {
			return err
		}
		for _, item := range domain.Checklists[device.Kind] {
			input := inputs[item.Key]
			note := pyStrip(input.Note)
			if _, ok := domain.Results[input.Result]; ok {
				if _, err := tx.Exec(
					"INSERT INTO inspection"+
						" (device_id, item_key, result, note, checked_by, checked_at)"+
						" VALUES (?, ?, ?, ?, ?, datetime('now'))"+
						" ON CONFLICT(device_id, item_key) DO UPDATE SET"+
						" result = excluded.result, note = excluded.note,"+
						" checked_by = excluded.checked_by,"+
						" checked_at = excluded.checked_at",
					deviceID, item.Key, input.Result, note, userID); err != nil {
					return err
				}
			} else if input.Result == "" {
				if _, err := tx.Exec(
					"DELETE FROM inspection WHERE device_id = ? AND item_key = ?",
					deviceID, item.Key); err != nil {
					return err
				}
			}
		}
		results, err := inspectionResultsTx(ctx, tx, deviceID)
		if err != nil {
			return err
		}
		state := domain.ComputeInspectionState(device.Kind, results)
		outcome.State = state
		newDone := 0
		if state.Complete {
			newDone = 1
		}
		if _, err := tx.Exec(
			"UPDATE device SET inspection_done = ?, updated_at = datetime('now')"+
				" WHERE id = ?", newDone, deviceID); err != nil {
			return err
		}
		summary := "検査: " + itoa(state.Done) + "/" + itoa(state.Total) + " 項目"
		if len(state.Fails) > 0 {
			summary += "、不合格: " + strings.Join(state.Fails, ", ")
		}
		if state.Complete {
			summary += "（完了）"
		}
		if err := addHistory(tx, deviceID, userID, "inspection", summary); err != nil {
			return err
		}
		// The gate must hold after the fact, not only at the
		// moment of the status change.
		if (device.Status == "ready" || device.Status == "reserved") && !state.Complete {
			if _, err := tx.Exec(
				"UPDATE device SET status = 'inspecting' WHERE id = ?",
				deviceID); err != nil {
				return err
			}
			if err := addHistory(tx, deviceID, userID, "status",
				domain.StatusLabel(device.Status)+
					" → 検査中（検査で条件が崩れたため自動で戻しました）"); err != nil {
				return err
			}
			outcome.Demoted = true
		}
		return nil
	})
	return outcome, err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var rev []byte
	for n > 0 {
		rev = append(rev, byte('0'+n%10))
		n /= 10
	}
	out := make([]byte, len(rev))
	for i, c := range rev {
		out[len(rev)-1-i] = c
	}
	return string(out)
}
