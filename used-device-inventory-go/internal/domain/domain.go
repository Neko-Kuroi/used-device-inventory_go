// Package domain holds the business rules of used-device-inventory
// as pure functions and data. Everything here mirrors the Python
// version (the specification); the constants and the pure functions
// are checked against vectors generated from the Python code.
package domain

import (
	"strings"
	"time"

	"used-device-inventory-go/internal/util"
)

// Kinds maps a device kind key to its display name.
var Kinds = map[string]string{
	"smartphone": "スマートフォン",
	"pc":         "パソコン",
	"tablet":     "タブレット",
}

// Ranks is the condition rank list, best to worst.
var Ranks = []string{"S", "A", "B", "C", "junk"}

// Statuses maps a status key to its display name.
var Statuses = map[string]string{
	"stocked":     "仕入済み",
	"inspecting":  "検査中",
	"maintaining": "整備中",
	"ready":       "販売可能",
	"reserved":    "取り置き",
	"sold":        "販売済み",
	"returned":    "返品・保証対応",
	"disposed":    "処分",
}

// StatusLabel returns the display name, or the key itself when
// unknown (the Python version's STATUSES.get(target, target)).
func StatusLabel(key string) string {
	if label, ok := Statuses[key]; ok {
		return label
	}
	return key
}

// NetworkRestrictions is the allowed set for the network restriction
// field, "" first.
var NetworkRestrictions = []string{"", "○", "△", "×", "確認できず"}

// FlowOrder is the display order of the main flow (used to sort the
// next-status choices); returned/disposed are not on it.
var FlowOrder = []string{
	"stocked", "inspecting", "maintaining", "ready", "reserved", "sold",
}

// Transitions is the lifecycle graph: from a status, the set of
// statuses it may move to. Disposed is terminal.
var Transitions = map[string]map[string]bool{
	"stocked":     {"inspecting": true, "maintaining": true, "disposed": true},
	"inspecting":  {"maintaining": true, "ready": true, "stocked": true, "disposed": true},
	"maintaining": {"inspecting": true, "ready": true, "disposed": true},
	"ready":       {"reserved": true, "sold": true, "maintaining": true, "inspecting": true},
	"reserved":    {"sold": true, "ready": true},
	"sold":        {"returned": true},
	"returned":    {"maintaining": true, "disposed": true},
	"disposed":    {},
}

// MainOrder is the spine of the flow for the reason rule.
// Maintaining and reserved are optional side steps, not on it.
var MainOrder = []string{"stocked", "inspecting", "ready", "sold"}

// ReasonPairs always need a reason, on top of the spine rule.
var ReasonPairs = map[[2]string]bool{
	{"reserved", "ready"}:         true,
	{"ready", "maintaining"}:      true,
	{"maintaining", "inspecting"}: true,
}

// NeedsReason reports whether a status change needs a reason in
// history: the returned/disposed branches, the explicit pairs, and
// skipping or going backwards on the spine.
func NeedsReason(current, target string) bool {
	if target == "returned" || target == "disposed" {
		return true
	}
	if ReasonPairs[[2]string{current, target}] {
		return true
	}
	ci, ti := indexOf(MainOrder, current), indexOf(MainOrder, target)
	if ci >= 0 && ti >= 0 {
		diff := ti - ci
		return diff < 0 || diff > 1
	}
	return false
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// NextStatuses lists the statuses reachable from current, in the
// order the detail page shows them (flow order; statuses outside
// the flow come last, as the Python sort key 99 does).
func NextStatuses(current string) []string {
	set := Transitions[current]
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sortByFlow(out)
	return out
}

func sortByFlow(keys []string) {
	less := func(a, b string) bool {
		ia, ib := indexOf(FlowOrder, a), indexOf(FlowOrder, b)
		if ia < 0 {
			ia = 99
		}
		if ib < 0 {
			ib = 99
		}
		if ia != ib {
			return ia < ib
		}
		return a < b // Python's sort is stable over a set; ties never
		// occur in practice (targets are distinct flow members or
		// the single off-flow target "disposed"/"returned").
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && less(keys[j], keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

// DeviceFields is the fixed field order of the device form, also
// used to build the change summary recorded in history.
var DeviceFields = []string{
	"kind", "maker", "model", "serial_number", "imei",
	"storage", "ram", "color", "battery_health", "sim_status",
	"activation_lock", "network_restriction",
	"cpu", "gpu", "os_name", "battery_cycles",
	"condition_rank", "status", "cosmetic_damage", "accessories",
	"inspection_done", "data_erasure_at", "data_erasure_method",
	"purchase_date", "purchase_source", "purchase_price",
	"planned_price", "sale_date", "sale_channel", "sale_price",
	"warranty_days", "notes",
}

// IntFields are the form fields parsed as integers.
var IntFields = map[string]bool{
	"battery_health": true, "battery_cycles": true,
	"purchase_price": true, "planned_price": true,
	"sale_price": true, "warranty_days": true,
}

// ReadyBlockers lists why a device cannot move to 販売可能 yet
// (empty = allowed), in the Python version's order and wording.
func ReadyBlockers(d *Device) []string {
	var blockers []string
	if d.InspectionDone == 0 {
		blockers = append(blockers, "検査が完了していません")
	}
	if d.DataErasureAt == "" {
		blockers = append(blockers, "データ消去の記録がありません")
	}
	if d.NetworkRestriction == "×" {
		blockers = append(blockers, "ネットワーク利用制限が × です")
	}
	return blockers
}

// GrossProfit is 実売価格 - 仕入れ価格 - 経費合計; nil when it cannot
// be computed (not sold yet, or no purchase price recorded).
func GrossProfit(d *Device, expenseTotal int64) *int64 {
	if d.SalePrice == nil || d.PurchasePrice == nil {
		return nil
	}
	v := *d.SalePrice - *d.PurchasePrice - expenseTotal
	return &v
}

// PlannedProfit is the same computation with the planned price.
func PlannedProfit(d *Device, expenseTotal int64) *int64 {
	if d.PlannedPrice == nil || d.PurchasePrice == nil {
		return nil
	}
	v := *d.PlannedPrice - *d.PurchasePrice - expenseTotal
	return &v
}

// CSVSafe neutralizes spreadsheet formula injection: a cell starting
// (after leading whitespace) with =, +, - or @ gets a leading
// apostrophe. Non-string values pass through unchanged, exactly as
// the Python version's csv_safe does.
func CSVSafe(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	trimmed := util.PyLStrip(s)
	if trimmed != "" {
		switch trimmed[0] {
		case '=', '+', '-', '@':
			return "'" + s
		}
	}
	return value
}

// ToJST converts a stored UTC timestamp ("YYYY-MM-DD HH:MM:SS") to
// the same form in Japan time (a fixed +9 hours; no timezone
// database). Empty gives ""; a value not in the timestamp form is
// returned unchanged. Only displays are converted, never stored
// values, and never user-entered dates.
// isTimestampForm reports whether value has the exact
// YYYY-MM-DD HH:MM:SS form: 19 bytes, zero-padded, ASCII digits.
// The strictness is deliberate (decided 2026-11-24, the Python
// version follows the same rule): time.Parse alone would also
// accept fractional seconds, and Python's strptime would accept
// unpadded fields, so without this gate the two versions
// disagreed on such inputs.
func isTimestampForm(value string) bool {
	if len(value) != 19 {
		return false
	}
	if value[4] != '-' || value[7] != '-' || value[10] != ' ' ||
		value[13] != ':' || value[16] != ':' {
		return false
	}
	for i := 0; i < 19; i++ {
		switch i {
		case 4, 7, 10, 13, 16:
			continue
		}
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// ToJST converts a UTC timestamp text to Japan time (+9 hours).
// Only the exact timestamp form is converted; anything else is
// returned unchanged. The year zero is not a real year (Python's
// datetime agrees), so it stays unchanged too.
func ToJST(value string) string {
	if value == "" {
		return ""
	}
	if !isTimestampForm(value) {
		return value
	}
	t, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil || t.Year() < 1 {
		return value
	}
	out := t.Add(9 * time.Hour)
	if out.Year() > 9999 {
		// Python's datetime cannot hold a year past 9999; the Python
		// version returns such a value unchanged, so does this one.
		return value
	}
	return out.Format("2006-01-02 15:04:05")
}

// MaskIMEI shows only the last 4 characters of an IMEI; the full
// value is admin-only. A nil or empty IMEI gives "".
func MaskIMEI(imei *string) string {
	if imei == nil || *imei == "" {
		return ""
	}
	runes := []rune(*imei)
	if len(runes) > 4 {
		runes = runes[len(runes)-4:]
	}
	return "****" + string(runes)
}

// NormalizeIMEI removes spaces and hyphens and checks the result is
// exactly 15 ASCII digits, as the Python version's form validation
// does (Unicode digits are rejected even though Python's isdigit
// alone would accept them).
func NormalizeIMEI(raw string) (string, bool) {
	digits := strings.ReplaceAll(strings.ReplaceAll(raw, " ", ""), "-", "")
	if len(digits) != 15 {
		return "", false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", false
		}
	}
	return digits, true
}

// FormatComma formats an integer with thousands separators, like
// Python's f"{n:,}" used in the history wording.
func FormatComma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := intToDigits(n)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func intToDigits(n int64) []rune {
	if n == 0 {
		return []rune{'0'}
	}
	var rev []rune
	for n > 0 {
		rev = append(rev, rune('0'+n%10))
		n /= 10
	}
	out := make([]rune, len(rev))
	for i, c := range rev {
		out[len(rev)-1-i] = c
	}
	return out
}
