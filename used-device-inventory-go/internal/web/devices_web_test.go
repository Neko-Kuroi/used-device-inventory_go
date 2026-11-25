package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Web-level mirrors of the Python version's phase 1 (screens),
// phase 4 (expenses and profit) and the page-level parts of the
// phase 2/3 and review-fix tests. The service-level equivalents
// live in the service package's tests.

// deviceValues mirrors the Python test suite's DEVICE fixture.
func deviceValues(overrides map[string]string) url.Values {
	values := url.Values{
		"kind": {"smartphone"}, "maker": {"Apple"}, "model": {"iPhone 12"},
		"serial_number": {"SN0001"}, "imei": {"123456789012345"},
		"storage": {"128GB"}, "ram": {"4GB"}, "color": {"黒"},
		"battery_health": {"89"}, "sim_status": {"SIMフリー"},
		"activation_lock": {"解除済み"}, "network_restriction": {"○"},
		"cpu": {""}, "gpu": {""}, "os_name": {"iOS 17"}, "battery_cycles": {""},
		"condition_rank": {"A"}, "status": {"stocked"},
		"cosmetic_damage": {"角に小さな傷"}, "accessories": {"箱、ケーブル"},
		"data_erasure_at": {"2026-11-02"}, "data_erasure_method": {"本体の初期化"},
		"purchase_date": {"2026-11-01"}, "purchase_source": {"店頭買取"},
		"purchase_price": {"30000"}, "planned_price": {"45000"},
		"sale_date": {""}, "sale_channel": {""}, "sale_price": {""},
		"warranty_days": {"90"}, "notes": {"テスト個体"},
	}
	for k, v := range overrides {
		values.Set(k, v)
	}
	return values
}

func (c *testClient) createDevice(overrides map[string]string) response {
	c.t.Helper()
	return c.post("/devices/new", "/devices/new", deviceValues(overrides))
}

func (c *testClient) moveStatus(id, target, reason string) response {
	c.t.Helper()
	return c.post("/devices/"+id+"/status", "/devices/"+id, url.Values{
		"to_status": {target}, "reason": {reason}})
}

func (c *testClient) completeInspectionWeb(id string) {
	c.t.Helper()
	form := url.Values{}
	for _, key := range []string{"power", "screen_touch", "camera",
		"speaker_mic", "buttons", "charging", "wifi", "mobile",
		"biometrics", "battery"} {
		form.Set("result_"+key, "pass")
	}
	resp := c.post("/devices/"+id+"/inspection", "/devices/"+id+"/inspection", form)
	if resp.status != http.StatusFound {
		c.t.Fatalf("inspection POST = %d", resp.status)
	}
}

func TestCreateDeviceAndDetail(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	resp := c.createDevice(nil)
	if resp.status != http.StatusFound || resp.location != "/devices/1" {
		t.Fatalf("create = %d %q", resp.status, resp.location)
	}
	detail := c.get("/devices/1")
	if detail.status != http.StatusOK {
		t.Fatalf("detail = %d", detail.status)
	}
	for _, want := range []string{"iPhone 12", "123456789012345",
		"仕入済み", "登録しました。", "（実施者: admin）"} {
		if !strings.Contains(detail.body, want) {
			t.Errorf("detail missing %q", want)
		}
	}
}

func TestListMasksIMEI(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	body := c.get("/").body
	if !strings.Contains(body, "****2345") {
		t.Error("list does not show the masked IMEI")
	}
	if strings.Contains(body, "123456789012345") {
		t.Error("list shows the full IMEI")
	}
	if !strings.Contains(body, "45,000 円") {
		t.Error("list does not show the planned price")
	}
}

func TestNonAdminCannotSeeFullIMEI(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	c.post("/admin/users", "/admin/users", url.Values{
		"username": {"staff"}, "password": {"staff-pass-1"}})
	c.logout()
	c.login("staff", "staff-pass-1", "")
	detail := c.get("/devices/1")
	if strings.Contains(detail.body, "123456789012345") {
		t.Error("non-admin detail shows the full IMEI")
	}
	if !strings.Contains(detail.body, "****2345") ||
		!strings.Contains(detail.body, "※全文は管理者のみ") {
		t.Error("non-admin detail missing the mask or the note")
	}
	// Editing as the non-admin (IMEI field comes back empty) keeps
	// the stored IMEI.
	resp := c.post("/devices/1/edit", "/devices/1/edit",
		deviceValues(map[string]string{"imei": "", "network_restriction": ""}))
	if resp.status != http.StatusFound {
		t.Fatalf("non-admin edit = %d", resp.status)
	}
	c.logout()
	c.login("admin", "admin-pass-1", "")
	if body := c.get("/devices/1").body; !strings.Contains(body, "123456789012345") {
		t.Error("IMEI was lost by the non-admin edit")
	}
}

func TestDeviceWithoutIMEI(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(map[string]string{"imei": "", "network_restriction": ""})
	body := c.get("/devices/1").body
	if !strings.Contains(body, "確認できず") {
		t.Error("restriction not marked uncheckable")
	}
	if !strings.Contains(body, "（記録なし）") {
		t.Error("missing-IMEI label not shown")
	}
}

func TestInvalidIMEIAndMissingSerialViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	resp := c.createDevice(map[string]string{"imei": "１２３４５６７８９０１２３４５"})
	if resp.status != http.StatusOK ||
		!strings.Contains(resp.body, "IMEI は15桁の半角数字") {
		t.Errorf("full-width imei create = %d", resp.status)
	}
	resp = c.createDevice(map[string]string{"serial_number": " "})
	if resp.status != http.StatusOK ||
		!strings.Contains(resp.body, "シリアル番号は必須です。") {
		t.Errorf("missing serial create = %d", resp.status)
	}
	if body := c.get("/").body; !strings.Contains(body, "該当する在庫がありません。") {
		t.Error("a device was registered despite the errors")
	}
}

func TestDuplicateSerialViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	resp := c.createDevice(nil)
	if resp.status != http.StatusOK ||
		!strings.Contains(resp.body, "そのシリアル番号は既に登録されています。") {
		t.Errorf("duplicate create = %d", resp.status)
	}
}

func TestSearchAndFiltersViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	c.createDevice(map[string]string{
		"serial_number": "SN0002", "maker": "Sony", "model": "Xperia",
		"kind": "pc", "imei": "", "network_restriction": ""})
	body := c.get("/?q=Sony").body
	if !strings.Contains(body, "Xperia") || strings.Contains(body, "iPhone") {
		t.Error("search by maker failed")
	}
	body = c.get("/?kind=pc").body
	if !strings.Contains(body, "Xperia") || strings.Contains(body, "iPhone") {
		t.Error("kind filter failed")
	}
	body = c.get("/?status=ready").body
	if !strings.Contains(body, "該当する在庫がありません。") {
		t.Error("status filter failed")
	}
	body = c.get("/").body
	if !strings.Contains(body, "2 件") {
		t.Error("unfiltered list does not show both devices")
	}
}

func TestEditRecordsHistoryViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	resp := c.post("/devices/1/edit", "/devices/1/edit",
		deviceValues(map[string]string{"model": "iPhone 13"}))
	if resp.status != http.StatusFound {
		t.Fatalf("edit = %d", resp.status)
	}
	body := c.get("/devices/1").body
	if !strings.Contains(body, "iPhone 13") ||
		!strings.Contains(body, "変更: model") ||
		!strings.Contains(body, "保存しました。") {
		t.Error("detail missing the new model, the history entry, or the flash")
	}
}

func TestStatusFlowViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	// The gate refuses: flash on the detail page after redirect.
	c.moveStatus("1", "inspecting", "")
	c.moveStatus("1", "ready", "")
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "販売可能にできません") {
		t.Error("gate flash missing")
	}
	c.moveStatus("1", "stocked", "差し戻し")
	c.completeInspectionWeb("1")
	resp := c.moveStatus("1", "inspecting", "")
	if resp.status != http.StatusFound {
		t.Fatalf("move to inspecting = %d", resp.status)
	}
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "状態を変更しました。") ||
		!strings.Contains(detail.body, "検査中") {
		t.Error("status flash or new status missing")
	}
	resp = c.moveStatus("1", "ready", "")
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "販売可能") {
		t.Error("ready status missing")
	}
	// An illegal move flashes the refusal.
	c.moveStatus("1", "returned", "理由あり")
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "へは進めません。") {
		t.Error("illegal-transition flash missing")
	}
}

func TestInspectionPageViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	page := c.get("/devices/1/inspection")
	if page.status != http.StatusOK ||
		!strings.Contains(page.body, "電源が入る") ||
		!strings.Contains(page.body, "0/10 項目") {
		t.Errorf("inspection page = %d", page.status)
	}
	form := url.Values{"result_power": {"pass"}, "result_wifi": {"fail"}}
	c.post("/devices/1/inspection", "/devices/1/inspection", form)
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "2/10 項目") ||
		!strings.Contains(detail.body, "不合格: Wi-Fi") {
		t.Error("detail does not reflect the partial inspection")
	}
	if !strings.Contains(detail.body, "検査結果を保存しました。") {
		t.Error("inspection flash missing")
	}
}

func TestExpensesViaWeb(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	c.createDevice(nil)
	add := func(amount, category, note string) response {
		return c.post("/devices/1/expenses", "/devices/1", url.Values{
			"expense_date": {"2026-11-03"}, "category": {category},
			"amount": {amount}, "note": {note}})
	}
	// Profit is blank until a sale price exists.
	detail := c.get("/devices/1")
	if !strings.Contains(detail.body, "―（実売価格か仕入れ価格が未記録）") {
		t.Error("profit should be blank before the sale")
	}
	add("3000", "parts", "バッテリー交換")
	add("800", "shipping", "")
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "3,800 円") {
		t.Error("expense total 3,800 not shown")
	}
	if !strings.Contains(detail.body, "経費追加: 部品代 3,000円（バッテリー交換）") {
		t.Error("expense history entry missing")
	}
	// Invalid expenses are refused with the flash.
	add("-500", "parts", "")
	add("abc", "parts", "")
	resp := add("100", "unknown", "")
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "経費の種類と金額（0以上の数字）を入れてください。") {
		t.Error("invalid-expense flash missing")
	}
	_ = resp
	// Record the sale: profit appears on detail and list.
	c.post("/devices/1/edit", "/devices/1/edit", deviceValues(map[string]string{
		"sale_date": "2026-11-12", "sale_channel": "店舗", "sale_price": "42000"}))
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "8,200 円") { // 42000-30000-3800
		t.Error("gross profit 8,200 not shown on detail")
	}
	if body := c.get("/").body; !strings.Contains(body, "8,200 円") {
		t.Error("gross profit not shown on the list")
	}
	// Delete the shipping expense from the detail page form.
	resp = c.post("/devices/1/expenses/2/delete", "/devices/1", nil)
	if resp.status != http.StatusFound {
		t.Fatalf("expense delete = %d", resp.status)
	}
	detail = c.get("/devices/1")
	if !strings.Contains(detail.body, "経費を削除しました。") ||
		!strings.Contains(detail.body, "3,000 円") {
		t.Error("after delete: flash or new total missing")
	}
}

func TestMissingDevice404(t *testing.T) {
	_, c := newTestServer(t)
	c.setupAdmin()
	if resp := c.get("/devices/999"); resp.status != http.StatusNotFound {
		t.Errorf("GET /devices/999 = %d, want 404", resp.status)
	}
	if resp := c.get("/devices/abc"); resp.status != http.StatusNotFound {
		t.Errorf("GET /devices/abc = %d, want 404", resp.status)
	}
}
