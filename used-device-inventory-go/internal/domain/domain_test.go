package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func loadJSON(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}

type constantsDump struct {
	Kinds              map[string]string      `json:"kinds"`
	Ranks              []string               `json:"ranks"`
	Statuses           map[string]string      `json:"statuses"`
	NetworkRestriction []string               `json:"network_restriction"`
	FlowOrder          []string               `json:"flow_order"`
	MainOrder          []string               `json:"main_order"`
	Transitions        map[string][]string    `json:"transitions"`
	ReasonPairs        [][2]string            `json:"reason_pairs"`
	DeviceFields       []string               `json:"device_fields"`
	IntFields          []string               `json:"int_fields"`
	Checklists         map[string][][2]string `json:"checklists"`
	Results            map[string]string      `json:"results"`
	ExpenseCategories  map[string]string      `json:"expense_categories"`
	PhotoKinds         map[string]string      `json:"photo_kinds"`
	MaxPhotos          int                    `json:"max_photos"`
	MaxPhotoBytes      int                    `json:"max_photo_bytes"`
}

// Every constant must match the Python version's, byte for byte.
func TestConstantsMatchPython(t *testing.T) {
	var c constantsDump
	loadJSON(t, "constants.json", &c)
	if !reflect.DeepEqual(Kinds, c.Kinds) {
		t.Errorf("Kinds = %v, want %v", Kinds, c.Kinds)
	}
	if !reflect.DeepEqual(Ranks, c.Ranks) {
		t.Errorf("Ranks = %v, want %v", Ranks, c.Ranks)
	}
	if !reflect.DeepEqual(Statuses, c.Statuses) {
		t.Errorf("Statuses = %v, want %v", Statuses, c.Statuses)
	}
	if !reflect.DeepEqual(NetworkRestrictions, c.NetworkRestriction) {
		t.Errorf("NetworkRestrictions = %v", NetworkRestrictions)
	}
	if !reflect.DeepEqual(FlowOrder, c.FlowOrder) {
		t.Errorf("FlowOrder = %v", FlowOrder)
	}
	if !reflect.DeepEqual(MainOrder, c.MainOrder) {
		t.Errorf("MainOrder = %v", MainOrder)
	}
	for status, targets := range c.Transitions {
		got := Transitions[status]
		if len(got) != len(targets) {
			t.Errorf("Transitions[%s] = %v, want %v", status, got, targets)
			continue
		}
		for _, target := range targets {
			if !got[target] {
				t.Errorf("Transitions[%s] missing %s", status, target)
			}
		}
	}
	if len(Transitions) != len(c.Transitions) {
		t.Errorf("Transitions has %d keys, want %d", len(Transitions), len(c.Transitions))
	}
	for _, pair := range c.ReasonPairs {
		if !ReasonPairs[pair] {
			t.Errorf("ReasonPairs missing %v", pair)
		}
	}
	if len(ReasonPairs) != len(c.ReasonPairs) {
		t.Errorf("ReasonPairs size = %d, want %d", len(ReasonPairs), len(c.ReasonPairs))
	}
	if !reflect.DeepEqual(DeviceFields, c.DeviceFields) {
		t.Errorf("DeviceFields = %v", DeviceFields)
	}
	for _, f := range c.IntFields {
		if !IntFields[f] {
			t.Errorf("IntFields missing %s", f)
		}
	}
	if len(IntFields) != len(c.IntFields) {
		t.Errorf("IntFields size = %d, want %d", len(IntFields), len(c.IntFields))
	}
	for kind, items := range c.Checklists {
		got := Checklists[kind]
		if len(got) != len(items) {
			t.Errorf("Checklists[%s] has %d items, want %d", kind, len(got), len(items))
			continue
		}
		for i, item := range items {
			if got[i].Key != item[0] || got[i].Label != item[1] {
				t.Errorf("Checklists[%s][%d] = %v, want %v", kind, i, got[i], item)
			}
		}
	}
	if !reflect.DeepEqual(Results, c.Results) {
		t.Errorf("Results = %v", Results)
	}
	if !reflect.DeepEqual(ExpenseCategories, c.ExpenseCategories) {
		t.Errorf("ExpenseCategories = %v", ExpenseCategories)
	}
	if !reflect.DeepEqual(PhotoKinds, c.PhotoKinds) {
		t.Errorf("PhotoKinds = %v", PhotoKinds)
	}
	if MaxPhotosPerDevice != c.MaxPhotos || MaxPhotoBytes != c.MaxPhotoBytes {
		t.Errorf("photo limits = %d/%d, want %d/%d",
			MaxPhotosPerDevice, MaxPhotoBytes, c.MaxPhotos, c.MaxPhotoBytes)
	}
}

type reasonVector struct {
	Current string `json:"current"`
	Target  string `json:"target"`
	Want    bool   `json:"want"`
}

func TestNeedsReasonAgainstPython(t *testing.T) {
	var vectors []reasonVector
	loadJSON(t, "needs_reason.json", &vectors)
	if len(vectors) != 64 {
		t.Fatalf("vectors = %d, want 64 (8x8)", len(vectors))
	}
	for _, v := range vectors {
		if got := NeedsReason(v.Current, v.Target); got != v.Want {
			t.Errorf("NeedsReason(%s, %s) = %v, want %v",
				v.Current, v.Target, got, v.Want)
		}
	}
}

type blockerVector struct {
	InspectionDone     int      `json:"inspection_done"`
	DataErasureAt      string   `json:"data_erasure_at"`
	NetworkRestriction string   `json:"network_restriction"`
	Want               []string `json:"want"`
}

func TestReadyBlockersAgainstPython(t *testing.T) {
	var vectors []blockerVector
	loadJSON(t, "blockers.json", &vectors)
	for _, v := range vectors {
		d := &Device{InspectionDone: v.InspectionDone,
			DataErasureAt:      v.DataErasureAt,
			NetworkRestriction: v.NetworkRestriction}
		got := ReadyBlockers(d)
		if len(got) == 0 && len(v.Want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, v.Want) {
			t.Errorf("ReadyBlockers(%+v) = %v, want %v", v, got, v.Want)
		}
	}
}

type csvVector struct {
	Input any `json:"input"`
	Want  any `json:"want"`
}

func TestCSVSafeAgainstPython(t *testing.T) {
	var vectors []csvVector
	loadJSON(t, "csv_safe.json", &vectors)
	for _, v := range vectors {
		got := CSVSafe(v.Input)
		// JSON numbers come back as float64; Python ints stringify
		// the same for the small values used here.
		if !reflect.DeepEqual(got, v.Want) {
			t.Errorf("CSVSafe(%#v) = %#v, want %#v", v.Input, got, v.Want)
		}
	}
}

type jstVector struct {
	Input *string `json:"input"`
	Want  string  `json:"want"`
}

func TestToJSTAgainstPython(t *testing.T) {
	var vectors []jstVector
	loadJSON(t, "to_jst.json", &vectors)
	for _, v := range vectors {
		in := ""
		if v.Input != nil {
			in = *v.Input
		}
		if got := ToJST(in); got != v.Want {
			t.Errorf("ToJST(%q) = %q, want %q", in, got, v.Want)
		}
	}
}

type profitVector struct {
	SalePrice     *int64 `json:"sale_price"`
	PlannedPrice  *int64 `json:"planned_price"`
	PurchasePrice *int64 `json:"purchase_price"`
	Total         int64  `json:"total"`
	Gross         *int64 `json:"gross"`
	PlannedProfit *int64 `json:"planned_profit"`
}

func TestProfitsAgainstPython(t *testing.T) {
	var vectors []profitVector
	loadJSON(t, "profits.json", &vectors)
	for _, v := range vectors {
		d := &Device{SalePrice: v.SalePrice, PlannedPrice: v.PlannedPrice,
			PurchasePrice: v.PurchasePrice}
		if got := GrossProfit(d, v.Total); !ptrEq(got, v.Gross) {
			t.Errorf("GrossProfit(%+v) = %v, want %v", v, got, v.Gross)
		}
		if got := PlannedProfit(d, v.Total); !ptrEq(got, v.PlannedProfit) {
			t.Errorf("PlannedProfit(%+v) = %v, want %v", v, got, v.PlannedProfit)
		}
	}
}

func ptrEq(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func TestMaskIMEI(t *testing.T) {
	full := "123456789012345"
	if got := MaskIMEI(&full); got != "****2345" {
		t.Errorf("MaskIMEI(full) = %q", got)
	}
	if got := MaskIMEI(nil); got != "" {
		t.Errorf("MaskIMEI(nil) = %q", got)
	}
	empty := ""
	if got := MaskIMEI(&empty); got != "" {
		t.Errorf("MaskIMEI(empty) = %q", got)
	}
}

func TestNormalizeIMEI(t *testing.T) {
	if got, ok := NormalizeIMEI("12345 6789-0123 45"); !ok || got != "123456789012345" {
		t.Errorf("NormalizeIMEI(spaced) = %q, %v", got, ok)
	}
	for _, bad := range []string{
		"１２３４５６７８９０１２３４５", "١٢٣٤٥٦٧٨٩٠١٢٣٤٥",
		"²²²²²²²²²²²²²²²", "12345678901234５", "1234", "",
	} {
		if _, ok := NormalizeIMEI(bad); ok {
			t.Errorf("NormalizeIMEI(%q) accepted", bad)
		}
	}
}

func TestFormatComma(t *testing.T) {
	cases := map[int64]string{
		0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567",
		-12345: "-12,345",
	}
	for in, want := range cases {
		if got := FormatComma(in); got != want {
			t.Errorf("FormatComma(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeInspectionState(t *testing.T) {
	// Nothing recorded.
	s := ComputeInspectionState("smartphone", map[string]string{})
	if s.Done != 0 || s.Total != 10 || s.Complete {
		t.Errorf("empty state = %+v", s)
	}
	// All recorded, one na: complete.
	all := map[string]string{}
	for _, item := range Checklists["tablet"] {
		all[item.Key] = "pass"
	}
	all["wifi"] = "na"
	s = ComputeInspectionState("tablet", all)
	if !s.Complete || s.Done != 8 {
		t.Errorf("tablet all-pass state = %+v", s)
	}
	// A fail blocks completion and is reported by label.
	all["battery"] = "fail"
	s = ComputeInspectionState("tablet", all)
	if s.Complete || len(s.Fails) != 1 || s.Fails[0] != "バッテリー状態" {
		t.Errorf("tablet fail state = %+v", s)
	}
	// Results for another kind's items are ignored.
	s = ComputeInspectionState("smartphone", map[string]string{"lan": "pass"})
	if s.Done != 0 {
		t.Errorf("cross-kind state = %+v", s)
	}
}
