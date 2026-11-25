package service

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
)

// The tests in this file mirror the Python version's phase 2, phase
// 3 and review-fix tests at the service layer (the HTTP-level parts
// are covered by the web tests in later steps).

func newTestDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx := context.Background()
	if err := db.Init(ctx, database); err != nil {
		t.Fatal(err)
	}
	res, err := database.ExecContext(ctx,
		"INSERT INTO user (username, password_hash, is_admin) VALUES ('admin', 'x', 1)")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return database, uid
}

// deviceForm mirrors the Python test suite's DEVICE fixture.
func deviceForm(overrides Form) Form {
	form := Form{
		"kind": "smartphone", "maker": "Apple", "model": "iPhone 12",
		"serial_number": "SN0001", "imei": "123456789012345",
		"storage": "128GB", "ram": "4GB", "color": "黒",
		"battery_health": "89", "sim_status": "SIMフリー",
		"activation_lock": "解除済み", "network_restriction": "○",
		"cpu": "", "gpu": "", "os_name": "iOS 17", "battery_cycles": "",
		"condition_rank": "A", "status": "stocked",
		"cosmetic_damage": "角に小さな傷", "accessories": "箱、ケーブル",
		"data_erasure_at": "2026-11-02", "data_erasure_method": "本体の初期化",
		"purchase_date": "2026-11-01", "purchase_source": "店頭買取",
		"purchase_price": "30000", "planned_price": "45000",
		"sale_date": "", "sale_channel": "", "sale_price": "",
		"warranty_days": "90", "notes": "テスト個体",
	}
	for k, v := range overrides {
		form[k] = v
	}
	return form
}

func mustCreate(t *testing.T, database *sql.DB, uid int64, form Form) int64 {
	t.Helper()
	id, err := CreateDevice(context.Background(), database, uid, form)
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	return id
}

func mustGet(t *testing.T, database *sql.DB, id int64) *domain.Device {
	t.Helper()
	d, err := GetDevice(context.Background(), database, id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func completeInspection(t *testing.T, database *sql.DB, uid, id int64, kind string) {
	t.Helper()
	inputs := map[string]InspectionInput{}
	for _, item := range domain.Checklists[kind] {
		inputs[item.Key] = InspectionInput{Result: "pass"}
	}
	outcome, err := SaveInspection(context.Background(), database, uid, id, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.State.Complete {
		t.Fatalf("inspection not complete: %+v", outcome.State)
	}
}

func mustMove(t *testing.T, database *sql.DB, uid, id int64, target, reason string) {
	t.Helper()
	if err := ChangeStatus(context.Background(), database, uid, id, target, reason); err != nil {
		t.Fatalf("ChangeStatus(%s): %v", target, err)
	}
}

// --- creation and form parsing ---

func TestCreateStartsStockedAndRecordsHistory(t *testing.T) {
	database, uid := newTestDB(t)
	id := mustCreate(t, database, uid, deviceForm(Form{"status": "sold"}))
	d := mustGet(t, database, id)
	if d.Status != "stocked" {
		t.Errorf("status = %s, want stocked (form status is ignored)", d.Status)
	}
	if d.DataErasureBy == nil || *d.DataErasureBy != uid {
		t.Errorf("erasure recorder on creation = %v, want the creator", d.DataErasureBy)
	}
	if d.CreatedBy == nil || *d.CreatedBy != uid {
		t.Errorf("created_by = %v", d.CreatedBy)
	}
	history, err := History(context.Background(), database, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Action != "create" ||
		history[0].Detail != "Apple iPhone 12 を登録" {
		t.Errorf("history = %+v", history)
	}
}

func TestParseValidationMessages(t *testing.T) {
	cases := []struct {
		name      string
		overrides Form
		want      string
	}{
		{"bad kind", Form{"kind": "watch"}, "種別が正しくありません。"},
		{"bad rank", Form{"condition_rank": "Z"}, "状態ランクが正しくありません。"},
		{"no serial", Form{"serial_number": " "}, "シリアル番号は必須です。"},
		{"int text", Form{"purchase_price": "abc"}, "purchase_price は数字で入れてください。"},
		{"int negative", Form{"purchase_price": "-1"}, "purchase_price は0以上で入れてください。"},
		{"imei fullwidth", Form{"imei": "１２３４５６７８９０１２３４５"}, "IMEI は15桁の半角数字で入れてください（判別できない場合は空欄のまま）。"},
		{"imei superscript", Form{"imei": strings.Repeat("²", 15)}, "IMEI は15桁の半角数字で入れてください（判別できない場合は空欄のまま）。"},
		{"imei short", Form{"imei": "1234"}, "IMEI は15桁の半角数字で入れてください（判別できない場合は空欄のまま）。"},
		{"bad restriction", Form{"network_restriction": "?"}, "ネットワーク利用制限の値が正しくありません。"},
	}
	for _, c := range cases {
		_, err := ParseDeviceForm(deviceForm(c.overrides), "stocked", 0)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Msg != c.want {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestParseQuirks(t *testing.T) {
	// Python's int() accepts full-width digits and underscores;
	// the port keeps that (design decision D5).
	d, err := ParseDeviceForm(deviceForm(Form{
		"purchase_price": "３００００", "planned_price": "45_000"}), "stocked", 0)
	if err != nil {
		t.Fatal(err)
	}
	if *d.PurchasePrice != 30000 || *d.PlannedPrice != 45000 {
		t.Errorf("prices = %v/%v", d.PurchasePrice, d.PlannedPrice)
	}
	// IMEI with spaces and hyphens is normalized.
	d, err = ParseDeviceForm(deviceForm(Form{
		"imei": "12345 6789-0123 45"}), "stocked", 0)
	if err != nil {
		t.Fatal(err)
	}
	if *d.IMEI != "123456789012345" {
		t.Errorf("imei = %s", *d.IMEI)
	}
	// No IMEI on a smartphone: restriction becomes 確認できず.
	d, err = ParseDeviceForm(deviceForm(Form{
		"imei": "", "network_restriction": ""}), "stocked", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.NetworkRestriction != "確認できず" {
		t.Errorf("smartphone restriction = %q", d.NetworkRestriction)
	}
	// ... but only on smartphones.
	d, err = ParseDeviceForm(deviceForm(Form{
		"kind": "pc", "imei": "", "network_restriction": ""}), "stocked", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.NetworkRestriction != "" {
		t.Errorf("pc restriction = %q, want empty", d.NetworkRestriction)
	}
}

func TestDuplicateSerial(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	mustCreate(t, database, uid, deviceForm(nil))
	if _, err := CreateDevice(ctx, database, uid, deviceForm(nil)); !errors.Is(err, ErrDuplicateSerial) {
		t.Errorf("second create err = %v, want ErrDuplicateSerial", err)
	}
	other := mustCreate(t, database, uid, deviceForm(Form{"serial_number": "SN0002"}))
	err := UpdateDevice(ctx, database, uid, true, other, deviceForm(nil))
	if !errors.Is(err, ErrDuplicateSerial) {
		t.Errorf("edit to duplicate err = %v, want ErrDuplicateSerial", err)
	}
}

// --- status changes ---

func TestForwardFlow(t *testing.T) {
	database, uid := newTestDB(t)
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	for _, target := range []string{"inspecting", "maintaining", "ready", "reserved", "sold"} {
		mustMove(t, database, uid, id, target, "")
	}
	if d := mustGet(t, database, id); d.Status != "sold" {
		t.Errorf("status = %s, want sold", d.Status)
	}
}

func TestIllegalTransitions(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	err := ChangeStatus(ctx, database, uid, id, "sold", "")
	var ite *InvalidTransitionError
	if !errors.As(err, &ite) {
		t.Fatalf("err = %v, want InvalidTransitionError", err)
	}
	if err.Error() != "仕入済み から 販売済み へは進めません。" {
		t.Errorf("message = %q", err.Error())
	}
	if err := ChangeStatus(ctx, database, uid, id, "returned", "理由"); !errors.As(err, &ite) {
		t.Errorf("returned from stocked err = %v", err)
	}
	if d := mustGet(t, database, id); d.Status != "stocked" {
		t.Errorf("status = %s, want stocked", d.Status)
	}
}

func TestReadyGateBlockers(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(Form{
		"data_erasure_at": "", "data_erasure_method": ""}))
	mustMove(t, database, uid, id, "inspecting", "")
	err := ChangeStatus(ctx, database, uid, id, "ready", "")
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BlockedError", err)
	}
	if len(be.Blockers) != 2 || be.Blockers[0] != "検査が完了していません" ||
		be.Blockers[1] != "データ消去の記録がありません" {
		t.Errorf("blockers = %v", be.Blockers)
	}
	if err.Error() != "販売可能にできません: 検査が完了していません / データ消去の記録がありません" {
		t.Errorf("message = %q", err.Error())
	}
	if d := mustGet(t, database, id); d.Status != "inspecting" {
		t.Errorf("status = %s, want inspecting", d.Status)
	}
}

func TestReadyGateBlocksRestrictionX(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(Form{"network_restriction": "×"}))
	completeInspection(t, database, uid, id, "smartphone")
	mustMove(t, database, uid, id, "inspecting", "")
	err := ChangeStatus(ctx, database, uid, id, "ready", "")
	var be *BlockedError
	if !errors.As(err, &be) || len(be.Blockers) != 1 ||
		be.Blockers[0] != "ネットワーク利用制限が × です" {
		t.Errorf("err = %v", err)
	}
}

func TestReasonRulesAndTerminal(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	mustMove(t, database, uid, id, "inspecting", "")
	// Backwards on the spine needs a reason.
	if err := ChangeStatus(ctx, database, uid, id, "stocked", ""); !errors.Is(err, ErrReasonRequired) {
		t.Errorf("backwards without reason err = %v", err)
	}
	mustMove(t, database, uid, id, "stocked", "登録ミスのため差し戻し")
	mustMove(t, database, uid, id, "inspecting", "")
	mustMove(t, database, uid, id, "ready", "")
	mustMove(t, database, uid, id, "sold", "")
	// Returned needs a reason too.
	if err := ChangeStatus(ctx, database, uid, id, "returned", ""); !errors.Is(err, ErrReasonRequired) {
		t.Errorf("returned without reason err = %v", err)
	}
	mustMove(t, database, uid, id, "returned", "初期不良のため返品")
	mustMove(t, database, uid, id, "disposed", "修理不能のため部品取り")
	// Disposed is terminal.
	var ite *InvalidTransitionError
	if err := ChangeStatus(ctx, database, uid, id, "stocked", "戻したい"); !errors.As(err, &ite) {
		t.Errorf("disposed -> stocked err = %v", err)
	}
}

func TestStatusHistoryWording(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	mustMove(t, database, uid, id, "inspecting", "")
	mustMove(t, database, uid, id, "ready", "")
	history, err := History(ctx, database, id)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Action == "status" {
			details = append(details, history[i].Detail)
		}
	}
	if len(details) != 2 || details[0] != "仕入済み → 検査中" ||
		details[1] != "検査中 → 販売可能" {
		t.Errorf("status history = %v", details)
	}
}

func TestReadyGateRecomputesStaleFlag(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	// Flag on, results missing: the gate recomputes and closes.
	id := mustCreate(t, database, uid, deviceForm(nil))
	if _, err := database.ExecContext(ctx,
		"UPDATE device SET inspection_done = 1 WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	mustMove(t, database, uid, id, "inspecting", "")
	var be *BlockedError
	if err := ChangeStatus(ctx, database, uid, id, "ready", ""); !errors.As(err, &be) {
		t.Fatalf("stale-on err = %v, want BlockedError", err)
	}
	if d := mustGet(t, database, id); d.InspectionDone != 0 {
		t.Errorf("flag after recompute = %d, want 0 (synced)", d.InspectionDone)
	}
	// Flag off, results complete (written directly): the gate
	// recomputes and opens.
	id2 := mustCreate(t, database, uid, deviceForm(Form{"serial_number": "SN0002"}))
	for _, item := range domain.Checklists["smartphone"] {
		if _, err := database.ExecContext(ctx,
			"INSERT INTO inspection (device_id, item_key, result, note, checked_by)"+
				" VALUES (?, ?, 'pass', '', ?)", id2, item.Key, uid); err != nil {
			t.Fatal(err)
		}
	}
	mustMove(t, database, uid, id2, "inspecting", "")
	mustMove(t, database, uid, id2, "ready", "")
	if d := mustGet(t, database, id2); d.InspectionDone != 1 {
		t.Errorf("flag after recompute = %d, want 1 (synced)", d.InspectionDone)
	}
}

// --- inspection ---

func TestInspectionSaveSemantics(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	// Partial: not complete.
	outcome, err := SaveInspection(ctx, database, uid, id, map[string]InspectionInput{
		"power": {Result: "pass"}, "camera": {Result: "pass"},
		"wifi": {Result: "na"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State.Done != 3 || outcome.State.Total != 10 || outcome.State.Complete {
		t.Errorf("partial state = %+v", outcome.State)
	}
	if d := mustGet(t, database, id); d.InspectionDone != 0 {
		t.Errorf("flag = %d, want 0", d.InspectionDone)
	}
	// A fail blocks completion and is named in the history summary.
	inputs := map[string]InspectionInput{}
	for _, item := range domain.Checklists["smartphone"] {
		inputs[item.Key] = InspectionInput{Result: "pass"}
	}
	inputs["battery"] = InspectionInput{Result: "fail"}
	outcome, err = SaveInspection(ctx, database, uid, id, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State.Complete || len(outcome.State.Fails) != 1 ||
		outcome.State.Fails[0] != "バッテリー状態" {
		t.Errorf("fail state = %+v", outcome.State)
	}
	history, _ := History(ctx, database, id)
	if history[0].Detail != "検査: 10/10 項目、不合格: バッテリー状態" {
		t.Errorf("summary = %q", history[0].Detail)
	}
	// Clearing a result deletes the row.
	inputs["battery"] = InspectionInput{Result: ""}
	outcome, err = SaveInspection(ctx, database, uid, id, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State.Done != 9 {
		t.Errorf("after clear done = %d, want 9", outcome.State.Done)
	}
	// An unknown result value is ignored.
	inputs["battery"] = InspectionInput{Result: "bogus"}
	outcome, err = SaveInspection(ctx, database, uid, id, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.State.Done != 9 {
		t.Errorf("after bogus done = %d, want 9", outcome.State.Done)
	}
	// Fixing the fail to pass completes it.
	inputs["battery"] = InspectionInput{Result: "pass"}
	outcome, err = SaveInspection(ctx, database, uid, id, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.State.Complete {
		t.Errorf("final state = %+v", outcome.State)
	}
	history, _ = History(ctx, database, id)
	if history[0].Detail != "検査: 10/10 項目（完了）" {
		t.Errorf("complete summary = %q", history[0].Detail)
	}
}

func TestInspectionDemotesReady(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	mustMove(t, database, uid, id, "inspecting", "")
	mustMove(t, database, uid, id, "ready", "")
	inputs := map[string]InspectionInput{}
	for _, item := range domain.Checklists["smartphone"] {
		inputs[item.Key] = InspectionInput{Result: "pass"}
	}
	inputs["wifi"] = InspectionInput{Result: "fail"}
	outcome, err := SaveInspection(ctx, database, uid, id, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Demoted {
		t.Fatal("not demoted")
	}
	if d := mustGet(t, database, id); d.Status != "inspecting" {
		t.Errorf("status = %s, want inspecting", d.Status)
	}
	history, _ := History(ctx, database, id)
	if history[0].Detail != "販売可能 → 検査中（検査で条件が崩れたため自動で戻しました）" {
		t.Errorf("demote history = %q", history[0].Detail)
	}
}

// --- edit rules ---

func TestReturnResetsInspectionAndErasure(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	mustMove(t, database, uid, id, "inspecting", "")
	mustMove(t, database, uid, id, "ready", "")
	mustMove(t, database, uid, id, "sold", "")
	mustMove(t, database, uid, id, "returned", "初期不良")
	d := mustGet(t, database, id)
	if d.InspectionDone != 0 || d.DataErasureAt != "" ||
		d.DataErasureMethod != "" || d.DataErasureBy != nil {
		t.Errorf("after return: %+v", d)
	}
	results, err := InspectionResults(ctx, database, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("inspection rows remain: %v", results)
	}
	history, _ := History(ctx, database, id)
	want := "販売済み → 返品・保証対応（理由: 初期不良）（返品のため検査結果とデータ消去の記録をリセットしました）"
	if history[0].Detail != want {
		t.Errorf("return history = %q, want %q", history[0].Detail, want)
	}
}

func TestKindChangeResetsInspection(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"kind": "pc"})); err != nil {
		t.Fatal(err)
	}
	d := mustGet(t, database, id)
	if d.Kind != "pc" || d.InspectionDone != 0 {
		t.Errorf("after kind change: kind=%s flag=%d", d.Kind, d.InspectionDone)
	}
	results, _ := InspectionResults(ctx, database, id)
	if len(results) != 0 {
		t.Errorf("inspection rows remain: %v", results)
	}
	history, _ := History(ctx, database, id)
	if history[0].Detail != "種別を変更したため、検査結果をリセットしました" ||
		history[1].Detail != "変更: kind" {
		t.Errorf("history = %q / %q", history[0].Detail, history[1].Detail)
	}
}

func TestEditLockWhileReady(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	completeInspection(t, database, uid, id, "smartphone")
	mustMove(t, database, uid, id, "inspecting", "")
	mustMove(t, database, uid, id, "ready", "")

	var le *LockedError
	err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"data_erasure_at": "", "data_erasure_method": ""}))
	if !errors.As(err, &le) || len(le.Labels) != 1 || le.Labels[0] != "データ消去の記録" {
		t.Fatalf("clear erasure err = %v", err)
	}
	if le.Values.DataErasureAt != "" {
		t.Error("locked error does not carry the typed values")
	}
	if err.Error() != "販売可能・取り置きの間は データ消去の記録 は変更できません。先に状態を戻してください。" {
		t.Errorf("lock message = %q", err.Error())
	}
	err = UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"network_restriction": "×"}))
	if !errors.As(err, &le) || le.Labels[0] != "ネットワーク利用制限" {
		t.Errorf("restriction x err = %v", err)
	}
	err = UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"kind": "tablet"}))
	if !errors.As(err, &le) || le.Labels[0] != "種別" {
		t.Errorf("kind err = %v", err)
	}
	err = UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"imei": "999999999999999"}))
	if !errors.As(err, &le) || le.Labels[0] != "IMEI" {
		t.Errorf("imei err = %v", err)
	}
	// Other fields are still editable while ready.
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"notes": "値下げ交渉可"})); err != nil {
		t.Errorf("notes edit while ready: %v", err)
	}
	if d := mustGet(t, database, id); d.Notes != "値下げ交渉可" {
		t.Errorf("notes = %q", d.Notes)
	}
}

func TestEditPreservesFlagAndErasureRecorder(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(Form{
		"data_erasure_at": "", "data_erasure_method": ""}))
	completeInspection(t, database, uid, id, "smartphone")
	// A plain edit must not reset the derived inspection flag.
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"data_erasure_at": "", "data_erasure_method": "",
			"notes": "メモ"})); err != nil {
		t.Fatal(err)
	}
	d := mustGet(t, database, id)
	if d.InspectionDone != 1 {
		t.Errorf("flag after edit = %d, want 1", d.InspectionDone)
	}
	if d.DataErasureBy != nil {
		t.Errorf("erasure_by = %v, want nil (no erasure record)", d.DataErasureBy)
	}
	// Recording an erasure sets the recorder; clearing clears it.
	if err := UpdateDevice(ctx, database, uid, true, id, deviceForm(nil)); err != nil {
		t.Fatal(err)
	}
	if d := mustGet(t, database, id); d.DataErasureBy == nil || *d.DataErasureBy != uid {
		t.Errorf("erasure_by after recording = %v", d.DataErasureBy)
	}
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"data_erasure_at": "", "data_erasure_method": ""})); err != nil {
		t.Fatal(err)
	}
	if d := mustGet(t, database, id); d.DataErasureBy != nil {
		t.Errorf("erasure_by after clearing = %v, want nil", d.DataErasureBy)
	}
}

func TestNonAdminIMEIRestore(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	// A non-admin's form sends the IMEI empty: unchanged, and the
	// restriction is unchanged too when its field is empty.
	if err := UpdateDevice(ctx, database, uid, false, id,
		deviceForm(Form{"imei": "", "network_restriction": ""})); err != nil {
		t.Fatal(err)
	}
	d := mustGet(t, database, id)
	if d.IMEI == nil || *d.IMEI != "123456789012345" ||
		d.NetworkRestriction != "○" {
		t.Errorf("after non-admin edit: imei=%v restriction=%q", d.IMEI, d.NetworkRestriction)
	}
	// An admin clearing the IMEI really clears it (and the
	// smartphone auto-rule marks the restriction uncheckable).
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"imei": "", "network_restriction": ""})); err != nil {
		t.Fatal(err)
	}
	d = mustGet(t, database, id)
	if d.IMEI != nil || d.NetworkRestriction != "確認できず" {
		t.Errorf("after admin clear: imei=%v restriction=%q", d.IMEI, d.NetworkRestriction)
	}
}

func TestUpdateHistoryChangedFields(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"maker": "Sony", "model": "Xperia"})); err != nil {
		t.Fatal(err)
	}
	history, _ := History(ctx, database, id)
	if history[0].Detail != "変更: maker, model" {
		t.Errorf("changed detail = %q", history[0].Detail)
	}
	if err := UpdateDevice(ctx, database, uid, true, id,
		deviceForm(Form{"maker": "Sony", "model": "Xperia"})); err != nil {
		t.Fatal(err)
	}
	history, _ = History(ctx, database, id)
	if history[0].Detail != "変更なし" {
		t.Errorf("no-change detail = %q", history[0].Detail)
	}
}

// --- expenses and list ---

func TestExpenses(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	id := mustCreate(t, database, uid, deviceForm(nil))
	if err := AddExpense(ctx, database, uid, id,
		"parts", "2026-11-03", "バッテリー交換", "1500"); err != nil {
		t.Fatal(err)
	}
	if err := AddExpense(ctx, database, uid, id,
		"shipping", "2026-11-10", "", "８００"); err != nil {
		t.Fatalf("full-width amount: %v", err)
	}
	total, err := ExpenseTotal(ctx, database, id)
	if err != nil || total != 2300 {
		t.Errorf("total = %d, %v", total, err)
	}
	history, _ := History(ctx, database, id)
	if history[0].Detail != "経費追加: 送料 800円" ||
		history[1].Detail != "経費追加: 部品代 1,500円（バッテリー交換）" {
		t.Errorf("expense history = %q / %q", history[0].Detail, history[1].Detail)
	}
	var ve *ValidationError
	if err := AddExpense(ctx, database, uid, id, "bogus", "", "", "100"); !errors.As(err, &ve) {
		t.Errorf("bad category err = %v", err)
	}
	if err := AddExpense(ctx, database, uid, id, "parts", "", "", "abc"); !errors.As(err, &ve) {
		t.Errorf("bad amount err = %v", err)
	}
	if err := AddExpense(ctx, database, uid, 9999, "parts", "", "", "100"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing device err = %v", err)
	}
	// Delete: wrong device is a 404; right device records history.
	rows, _ := ListDevices(ctx, database, ListFilter{})
	_ = rows
	var expenseID int64
	if err := database.QueryRowContext(ctx,
		"SELECT id FROM expense WHERE category = 'parts'").Scan(&expenseID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteExpense(ctx, database, uid, id+100, expenseID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete with wrong device err = %v", err)
	}
	if err := DeleteExpense(ctx, database, uid, id, expenseID); err != nil {
		t.Fatal(err)
	}
	history, _ = History(ctx, database, id)
	if history[0].Detail != "経費削除: 部品代 1,500円" {
		t.Errorf("delete history = %q", history[0].Detail)
	}
	if total, _ := ExpenseTotal(ctx, database, id); total != 800 {
		t.Errorf("total after delete = %d", total)
	}
}

func TestListSearchAndFilters(t *testing.T) {
	database, uid := newTestDB(t)
	ctx := context.Background()
	mustCreate(t, database, uid, deviceForm(Form{"maker": "100%確実な店"}))
	mustCreate(t, database, uid, deviceForm(Form{
		"serial_number": "SN0002", "maker": "Sony", "model": "Xperia_1"}))
	// "%" in the query is literal, not a wildcard.
	rows, err := ListDevices(ctx, database, ListFilter{Query: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Device.Maker != "100%確実な店" {
		t.Errorf("percent search = %d rows", len(rows))
	}
	// "_" is literal too.
	rows, err = ListDevices(ctx, database, ListFilter{Query: "_"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Device.Model != "Xperia_1" {
		t.Errorf("underscore search = %d rows", len(rows))
	}
	// Newest first; kind/status filters apply only when valid.
	rows, err = ListDevices(ctx, database, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Device.SerialNumber != "SN0002" {
		t.Errorf("list order = %d rows", len(rows))
	}
	rows, _ = ListDevices(ctx, database, ListFilter{Kind: "bogus"})
	if len(rows) != 2 {
		t.Errorf("invalid kind filter = %d rows, want 2 (ignored)", len(rows))
	}
	rows, _ = ListDevices(ctx, database, ListFilter{Status: "ready"})
	if len(rows) != 0 {
		t.Errorf("status filter = %d rows", len(rows))
	}
}
