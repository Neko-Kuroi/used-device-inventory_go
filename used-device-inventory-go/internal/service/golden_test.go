package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"used-device-inventory-go/internal/db"
	"used-device-inventory-go/internal/domain"
)

type goldenDevice struct {
	ID                 int64   `json:"id"`
	Kind               string  `json:"kind"`
	Status             string  `json:"status"`
	IMEI               *string `json:"imei"`
	NetworkRestriction string  `json:"network_restriction"`
	InspectionDone     int     `json:"inspection_done"`
	DataErasureBy      *int64  `json:"data_erasure_by"`
	ExpenseTotal       int64   `json:"expense_total"`
	GrossProfit        *int64  `json:"gross_profit"`
	PlannedProfit      *int64  `json:"planned_profit"`
	Inspection         struct {
		Done     int      `json:"done"`
		Total    int      `json:"total"`
		Complete bool     `json:"complete"`
		Fails    []string `json:"fails"`
	} `json:"inspection"`
	History []struct {
		Action string `json:"action"`
		Detail string `json:"detail"`
	} `json:"history"`
}

// The golden database was populated entirely through the Python
// version's own routes, and golden.json holds the values the Python
// version's own functions compute from it. The Go service must
// reproduce every one of them.
func TestGoldenPythonPopulatedDB(t *testing.T) {
	src := filepath.Join("testdata", "python_populated.db")
	dst := filepath.Join(t.TempDir(), "golden.db")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()

	raw, err := os.ReadFile(filepath.Join("testdata", "golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden []goldenDevice
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden) != 3 {
		t.Fatalf("golden devices = %d, want 3", len(golden))
	}
	for _, want := range golden {
		d, err := GetDevice(ctx, database, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Kind != want.Kind || d.Status != want.Status ||
			d.NetworkRestriction != want.NetworkRestriction ||
			d.InspectionDone != want.InspectionDone {
			t.Errorf("device %d: got kind=%s status=%s restriction=%q flag=%d",
				want.ID, d.Kind, d.Status, d.NetworkRestriction, d.InspectionDone)
		}
		if !imeiEqual(d.IMEI, want.IMEI) {
			t.Errorf("device %d: imei = %v, want %v", want.ID, d.IMEI, want.IMEI)
		}
		if !ptrEq(d.DataErasureBy, want.DataErasureBy) {
			t.Errorf("device %d: erasure_by = %v, want %v", want.ID, d.DataErasureBy, want.DataErasureBy)
		}
		total, err := ExpenseTotal(ctx, database, want.ID)
		if err != nil || total != want.ExpenseTotal {
			t.Errorf("device %d: expense total = %d, want %d", want.ID, total, want.ExpenseTotal)
		}
		if got := domain.GrossProfit(d, total); !ptrEq(got, want.GrossProfit) {
			t.Errorf("device %d: gross = %v, want %v", want.ID, got, want.GrossProfit)
		}
		if got := domain.PlannedProfit(d, total); !ptrEq(got, want.PlannedProfit) {
			t.Errorf("device %d: planned = %v, want %v", want.ID, got, want.PlannedProfit)
		}
		state, err := InspectionStateFor(ctx, database, d)
		if err != nil {
			t.Fatal(err)
		}
		if state.Done != want.Inspection.Done || state.Total != want.Inspection.Total ||
			state.Complete != want.Inspection.Complete ||
			!reflect.DeepEqual(nonNil(state.Fails), want.Inspection.Fails) {
			t.Errorf("device %d: inspection = %+v, want %+v", want.ID, state, want.Inspection)
		}
		history, err := History(ctx, database, want.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != len(want.History) {
			t.Fatalf("device %d: history = %d rows, want %d", want.ID, len(history), len(want.History))
		}
		// History() is newest first; golden.json is oldest first.
		for i, h := range history {
			g := want.History[len(want.History)-1-i]
			if h.Action != g.Action || h.Detail != g.Detail {
				t.Errorf("device %d history[%d] = %s | %q, want %s | %q",
					want.ID, i, h.Action, h.Detail, g.Action, g.Detail)
			}
		}
	}
}

func ptrEq(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
