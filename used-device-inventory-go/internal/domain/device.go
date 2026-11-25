package domain

// Device is one row of the device table. Pointer fields are the
// nullable columns: the integer form fields (nil = not entered) and
// the IMEI (nil = not recorded, distinct from an empty string).
type Device struct {
	ID                 int64
	Kind               string
	Maker              string
	Model              string
	SerialNumber       string
	IMEI               *string
	Storage            string
	RAM                string
	Color              string
	BatteryHealth      *int64
	SIMStatus          string
	ActivationLock     string
	NetworkRestriction string
	CPU                string
	GPU                string
	OSName             string
	BatteryCycles      *int64
	ConditionRank      string
	Status             string
	CosmeticDamage     string
	Accessories        string
	InspectionDone     int
	DataErasureAt      string
	DataErasureMethod  string
	DataErasureBy      *int64
	PurchaseDate       string
	PurchaseSource     string
	PurchasePrice      *int64
	PlannedPrice       *int64
	SaleDate           string
	SaleChannel        string
	SalePrice          *int64
	WarrantyDays       *int64
	Notes              string
	CreatedBy          *int64
	CreatedAt          string
	UpdatedAt          string
}

// FieldValues returns the device's values for the DeviceFields
// columns, in order, as database arguments. It is used for inserts,
// updates, and field-by-field change detection.
func (d *Device) FieldValues() []any {
	return []any{
		d.Kind, d.Maker, d.Model, d.SerialNumber, d.IMEI,
		d.Storage, d.RAM, d.Color, d.BatteryHealth, d.SIMStatus,
		d.ActivationLock, d.NetworkRestriction,
		d.CPU, d.GPU, d.OSName, d.BatteryCycles,
		d.ConditionRank, d.Status, d.CosmeticDamage, d.Accessories,
		d.InspectionDone, d.DataErasureAt, d.DataErasureMethod,
		d.PurchaseDate, d.PurchaseSource, d.PurchasePrice,
		d.PlannedPrice, d.SaleDate, d.SaleChannel, d.SalePrice,
		d.WarrantyDays, d.Notes,
	}
}

// ChangedFields lists, in DeviceFields order, the fields whose
// values differ between two devices (the history change summary).
func ChangedFields(before, after *Device) []string {
	a, b := before.FieldValues(), after.FieldValues()
	var changed []string
	for i, name := range DeviceFields {
		if !fieldEqual(a[i], b[i]) {
			changed = append(changed, name)
		}
	}
	return changed
}

func fieldEqual(a, b any) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case int:
		bv, ok := b.(int)
		return ok && av == bv
	case *string:
		bv, ok := b.(*string)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == bv
		}
		return *av == *bv
	case *int64:
		bv, ok := b.(*int64)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == bv
		}
		return *av == *bv
	}
	return false
}

// ChecklistItem is one inspection checklist entry.
type ChecklistItem struct {
	Key   string
	Label string
}

// Checklists holds the per-kind inspection checklists.
var Checklists = map[string][]ChecklistItem{
	"smartphone": {
		{"power", "電源が入る"}, {"screen_touch", "画面表示とタッチ"},
		{"camera", "カメラ（前後）"}, {"speaker_mic", "スピーカーとマイク"},
		{"buttons", "ボタン類"}, {"charging", "充電"},
		{"wifi", "Wi-Fi"}, {"mobile", "モバイル通信"},
		{"biometrics", "指紋・顔認証"}, {"battery", "バッテリー状態"},
	},
	"pc": {
		{"power", "電源と起動"}, {"display", "画面表示"},
		{"keyboard", "キーボード全キー"}, {"trackpad", "トラックパッド"},
		{"camera", "カメラ"}, {"speaker_mic", "スピーカーとマイク"},
		{"charging_battery", "充電とバッテリー"}, {"wifi", "Wi-Fi"},
		{"lan", "有線 LAN"}, {"ports", "USB 等の端子"},
		{"storage_health", "ストレージの健康状態"},
	},
	"tablet": {
		{"power", "電源が入る"}, {"screen_touch", "画面表示とタッチ"},
		{"camera", "カメラ（前後）"}, {"speaker_mic", "スピーカーとマイク"},
		{"buttons", "ボタン類"}, {"charging", "充電"},
		{"wifi", "Wi-Fi"}, {"battery", "バッテリー状態"},
	},
}

// Results maps an inspection result key to its display name.
var Results = map[string]string{
	"pass": "合格", "fail": "不合格", "na": "対象外",
}

// InspectionState summarizes one device's checklist. Complete means
// every item has a result and none of them is fail ('na' counts as
// done: the item does not apply to this unit).
type InspectionState struct {
	ByKey    map[string]string // item key -> result, only recorded items
	Done     int
	Total    int
	Complete bool
	Fails    []string // labels of failed items, in checklist order
}

// ComputeInspectionState builds the state from the recorded results
// (item key -> result) for a device of the given kind.
func ComputeInspectionState(kind string, results map[string]string) InspectionState {
	items := Checklists[kind]
	state := InspectionState{ByKey: map[string]string{}, Total: len(items)}
	for _, item := range items {
		result, ok := results[item.Key]
		if !ok {
			continue
		}
		state.ByKey[item.Key] = result
		state.Done++
		if result == "fail" {
			state.Fails = append(state.Fails, item.Label)
		}
	}
	state.Complete = state.Done == state.Total && len(state.Fails) == 0
	return state
}

// ExpenseCategories maps an expense category key to its display name.
var ExpenseCategories = map[string]string{
	"parts": "部品代", "shipping": "送料", "fee": "販売手数料", "other": "その他",
}

// PhotoKinds maps a photo kind key to its display name.
var PhotoKinds = map[string]string{
	"front": "正面", "back": "背面", "damage": "傷", "other": "その他",
}

// Option is one key/label pair for select elements. The ordered
// lists below preserve the Python dictionaries' definition order,
// which is the order the pages show.
type Option struct {
	Key   string
	Label string
}

var KindOptions = []Option{
	{"smartphone", "スマートフォン"}, {"pc", "パソコン"}, {"tablet", "タブレット"},
}

var StatusOptions = []Option{
	{"stocked", "仕入済み"}, {"inspecting", "検査中"},
	{"maintaining", "整備中"}, {"ready", "販売可能"},
	{"reserved", "取り置き"}, {"sold", "販売済み"},
	{"returned", "返品・保証対応"}, {"disposed", "処分"},
}

var ResultOptions = []Option{
	{"pass", "合格"}, {"fail", "不合格"}, {"na", "対象外"},
}

var ExpenseCategoryOptions = []Option{
	{"parts", "部品代"}, {"shipping", "送料"}, {"fee", "販売手数料"}, {"other", "その他"},
}

var PhotoKindOptions = []Option{
	{"front", "正面"}, {"back", "背面"}, {"damage", "傷"}, {"other", "その他"},
}

const (
	MaxPhotosPerDevice = 5
	MaxPhotoBytes      = 5 * 1024 * 1024
)

// SniffPhotoExt identifies an image by its magic bytes: "jpg",
// "png", "webp", or "" when it is none of them.
func SniffPhotoExt(data []byte) string {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "jpg"
	}
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "png"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "webp"
	}
	return ""
}

var heicBrands = map[string]bool{
	"heic": true, "heix": true, "hevc": true, "heim": true,
	"heis": true, "mif1": true, "mif2": true,
}

// LooksLikeHEIC recognizes a HEIC/HEIF file (an ISO-BMFF 'ftyp' box
// with a HEIC-family brand) so it can be refused with its own
// message; decoding HEIC is not possible here either.
func LooksLikeHEIC(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	return heicBrands[string(data[8:12])]
}
