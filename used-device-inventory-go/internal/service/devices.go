package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/util"
)

// Form is a submitted device form: field name -> raw value, as the
// web layer received it. A missing field reads as "".
type Form map[string]string

func (f Form) get(field string) string { return f[field] }

// ParseDeviceForm validates a device form the way the Python
// version's parse_device_form does: same field handling, same
// validation order, same messages. Status and the inspection flag
// are never taken from the form; the caller fixes them.
func ParseDeviceForm(form Form, fixedStatus string, fixedInspectionDone int) (*domain.Device, error) {
	d := &domain.Device{}
	ints := map[string]**int64{
		"battery_health": &d.BatteryHealth, "battery_cycles": &d.BatteryCycles,
		"purchase_price": &d.PurchasePrice, "planned_price": &d.PlannedPrice,
		"sale_price": &d.SalePrice, "warranty_days": &d.WarrantyDays,
	}
	texts := map[string]*string{
		"kind": &d.Kind, "maker": &d.Maker, "model": &d.Model,
		"serial_number": &d.SerialNumber, "storage": &d.Storage,
		"ram": &d.RAM, "color": &d.Color, "sim_status": &d.SIMStatus,
		"activation_lock":     &d.ActivationLock,
		"network_restriction": &d.NetworkRestriction,
		"cpu":                 &d.CPU, "gpu": &d.GPU, "os_name": &d.OSName,
		"condition_rank":  &d.ConditionRank,
		"cosmetic_damage": &d.CosmeticDamage, "accessories": &d.Accessories,
		"data_erasure_at":     &d.DataErasureAt,
		"data_erasure_method": &d.DataErasureMethod,
		"purchase_date":       &d.PurchaseDate, "purchase_source": &d.PurchaseSource,
		"sale_date": &d.SaleDate, "sale_channel": &d.SaleChannel,
		"notes": &d.Notes,
	}
	for _, field := range domain.DeviceFields {
		switch {
		case field == "status":
			d.Status = fixedStatus
		case field == "inspection_done":
			d.InspectionDone = fixedInspectionDone
		case domain.IntFields[field]:
			raw := pyStrip(form.get(field))
			if raw == "" {
				*ints[field] = nil
				continue
			}
			v, ok := util.ToInt(raw)
			if !ok {
				return nil, &ValidationError{Msg: fmt.Sprintf("%s は数字で入れてください。", field)}
			}
			if v < 0 {
				return nil, &ValidationError{Msg: fmt.Sprintf("%s は0以上で入れてください。", field)}
			}
			*ints[field] = &v
		case field == "imei":
			raw := pyStrip(form.get(field))
			if raw == "" {
				d.IMEI = nil
			} else {
				d.IMEI = &raw
			}
		default:
			*texts[field] = pyStrip(form.get(field))
		}
	}
	if _, ok := domain.Kinds[d.Kind]; !ok {
		return nil, &ValidationError{Msg: "種別が正しくありません。"}
	}
	if !contains(domain.Ranks, d.ConditionRank) {
		return nil, &ValidationError{Msg: "状態ランクが正しくありません。"}
	}
	if _, ok := domain.Statuses[d.Status]; !ok {
		return nil, &ValidationError{Msg: "状態が正しくありません。"}
	}
	if d.SerialNumber == "" {
		return nil, &ValidationError{Msg: "シリアル番号は必須です。"}
	}
	if d.IMEI != nil {
		digits, ok := domain.NormalizeIMEI(*d.IMEI)
		if !ok {
			return nil, &ValidationError{Msg: "IMEI は15桁の半角数字で入れてください（判別できない場合は空欄のまま）。"}
		}
		d.IMEI = &digits
	}
	if !contains(domain.NetworkRestrictions, d.NetworkRestriction) {
		return nil, &ValidationError{Msg: "ネットワーク利用制限の値が正しくありません。"}
	}
	if d.IMEI == nil && d.NetworkRestriction == "" {
		// No IMEI means the restriction check cannot be done; the
		// restriction only applies to smartphones.
		if d.Kind == "smartphone" {
			d.NetworkRestriction = "確認できず"
		}
	}
	return d, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

const insertDeviceSQL = "INSERT INTO device (" +
	"kind, maker, model, serial_number, imei, storage, ram, color," +
	" battery_health, sim_status, activation_lock, network_restriction," +
	" cpu, gpu, os_name, battery_cycles, condition_rank, status," +
	" cosmetic_damage, accessories, inspection_done, data_erasure_at," +
	" data_erasure_method, purchase_date, purchase_source, purchase_price," +
	" planned_price, sale_date, sale_channel, sale_price, warranty_days," +
	" notes) VALUES (" +
	"?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?," +
	" ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

// CreateDevice registers a new device at 仕入済み from a form.
func CreateDevice(ctx context.Context, database *sql.DB, userID int64, form Form) (int64, error) {
	values, err := ParseDeviceForm(form, "stocked", 0)
	if err != nil {
		return 0, err
	}
	var deviceID int64
	err = db.Tx(ctx, database, func(tx *db.WriteTx) error {
		res, err := tx.Exec(insertDeviceSQL, values.FieldValues()...)
		if err != nil {
			if isSerialDuplicate(err) {
				return ErrDuplicateSerial
			}
			return err
		}
		deviceID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"UPDATE device SET created_by = ? WHERE id = ?", userID, deviceID); err != nil {
			return err
		}
		if values.DataErasureAt != "" {
			// The recorder is part of the erasure record, on
			// creation just as on edit.
			if _, err := tx.Exec(
				"UPDATE device SET data_erasure_by = ? WHERE id = ?",
				userID, deviceID); err != nil {
				return err
			}
		}
		return addHistory(tx, deviceID, userID, "create",
			fmt.Sprintf("%s %s を登録", values.Maker, values.Model))
	})
	if err != nil {
		return 0, err
	}
	return deviceID, nil
}

// UpdateDevice saves an edit form for an existing device. The
// device's status and inspection flag are preserved; the ready gate
// fields are locked while the device is 販売可能 or 取り置き.
func UpdateDevice(ctx context.Context, database *sql.DB, userID int64, isAdmin bool, deviceID int64, form Form) error {
	return db.Tx(ctx, database, func(tx *db.WriteTx) error {
		device, err := getDeviceTx(tx, deviceID)
		if err != nil {
			return err
		}
		values, err := ParseDeviceForm(form, device.Status, device.InspectionDone)
		if err != nil {
			return err
		}
		values.ID = deviceID
		// A non-admin cannot see the full IMEI, so the form sends
		// it empty; treat that as "unchanged", not "cleared".
		if !isAdmin && values.IMEI == nil && device.IMEI != nil {
			values.IMEI = device.IMEI
			if pyStrip(form.get("network_restriction")) == "" {
				values.NetworkRestriction = device.NetworkRestriction
			}
		}
		// While a unit is on sale, the facts the ready gate rests
		// on are locked.
		if device.Status == "ready" || device.Status == "reserved" {
			var locked []string
			if values.Kind != device.Kind {
				locked = append(locked, "種別")
			}
			if !imeiEqual(values.IMEI, device.IMEI) {
				locked = append(locked, "IMEI")
			}
			if values.NetworkRestriction != device.NetworkRestriction {
				locked = append(locked, "ネットワーク利用制限")
			}
			if values.DataErasureAt != device.DataErasureAt ||
				values.DataErasureMethod != device.DataErasureMethod {
				locked = append(locked, "データ消去の記録")
			}
			if len(locked) > 0 {
				return &LockedError{Labels: locked, Values: values}
			}
		}
		changed := domain.ChangedFields(device, values)
		setClause := make([]string, len(domain.DeviceFields))
		for i, f := range domain.DeviceFields {
			setClause[i] = f + " = ?"
		}
		args := append(values.FieldValues(), deviceID)
		if _, err := tx.Exec(
			"UPDATE device SET "+strings.Join(setClause, ", ")+
				", updated_at = datetime('now') WHERE id = ?", args...); err != nil {
			if isSerialDuplicate(err) {
				return ErrDuplicateSerial
			}
			return err
		}
		if values.DataErasureAt != device.DataErasureAt ||
			values.DataErasureMethod != device.DataErasureMethod {
			// Who recorded the erasure is part of the record.
			var by any
			if values.DataErasureAt != "" {
				by = userID
			}
			if _, err := tx.Exec(
				"UPDATE device SET data_erasure_by = ? WHERE id = ?",
				by, deviceID); err != nil {
				return err
			}
		}
		detail := "変更なし"
		if len(changed) > 0 {
			detail = "変更: " + strings.Join(changed, ", ")
		}
		if err := addHistory(tx, deviceID, userID, "update", detail); err != nil {
			return err
		}
		if values.Kind != device.Kind {
			// The checklist differs per kind, so results recorded
			// for the old kind say nothing about the new one.
			if _, err := tx.Exec(
				"DELETE FROM inspection WHERE device_id = ?", deviceID); err != nil {
				return err
			}
			if _, err := tx.Exec(
				"UPDATE device SET inspection_done = 0 WHERE id = ?",
				deviceID); err != nil {
				return err
			}
			if err := addHistory(tx, deviceID, userID, "inspection",
				"種別を変更したため、検査結果をリセットしました"); err != nil {
				return err
			}
		}
		return nil
	})
}

func imeiEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
