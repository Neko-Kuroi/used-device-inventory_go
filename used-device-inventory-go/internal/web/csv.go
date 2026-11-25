package web

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"

	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/service"
)

func (srv *Server) csvRoutes() {
	srv.mux.HandleFunc("GET /export.csv", srv.requireLogin(srv.exportCSV))
}

var csvHeader = []string{
	"ID", "種別", "メーカー", "機種", "シリアル番号", "IMEI末尾4桁",
	"状態ランク", "状態", "ネットワーク利用制限", "検査完了",
	"データ消去日", "仕入れ日", "仕入れ先", "仕入れ価格",
	"販売予定価格", "販売日", "販売先", "実売価格",
	"経費合計", "粗利", "備考", "登録日時", "更新日時",
}

// exportCSV mirrors the Python version's CSV export byte for byte:
// same columns, same values (formula-injection neutralization
// applied to the raw values, as csv_safe does), CRLF line endings,
// and a UTF-8 BOM so Excel opens the Japanese text correctly.
func (srv *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	rows, err := service.ListDevices(r.Context(), srv.db, service.ListFilter{
		Query:  query.Get("q"),
		Kind:   query.Get("kind"),
		Status: query.Get("status"),
	})
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // utf-8-sig BOM
	writer := csv.NewWriter(&buf)
	writer.UseCRLF = true
	if err := writer.Write(csvHeader); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	for _, row := range rows {
		d := row.Device
		imeiTail := ""
		if d.IMEI != nil && *d.IMEI != "" {
			runes := []rune(*d.IMEI)
			if len(runes) > 4 {
				runes = runes[len(runes)-4:]
			}
			imeiTail = string(runes)
		}
		inspection := ""
		if d.InspectionDone != 0 {
			inspection = "完了"
		}
		raw := []any{
			d.ID, domain.Kinds[d.Kind], d.Maker, d.Model, d.SerialNumber,
			imeiTail, d.ConditionRank, domain.Statuses[d.Status],
			d.NetworkRestriction, inspection, d.DataErasureAt,
			d.PurchaseDate, d.PurchaseSource, intOrEmpty(d.PurchasePrice),
			intOrEmpty(d.PlannedPrice), d.SaleDate, d.SaleChannel,
			intOrEmpty(d.SalePrice), row.ExpenseTotal,
			profitOrEmpty(row.Profit), d.Notes,
			domain.ToJST(d.CreatedAt), domain.ToJST(d.UpdatedAt),
		}
		cells := make([]string, len(raw))
		for i, v := range raw {
			cells[i] = csvCell(domain.CSVSafe(v))
		}
		if err := writer.Write(cells); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=devices.csv")
	w.Write(buf.Bytes())
}

// intOrEmpty keeps a nullable integer as an integer (so csv_safe
// sees the same type the Python version passes) or "".
func intOrEmpty(v *int64) any {
	if v == nil {
		return ""
	}
	return *v
}

func profitOrEmpty(v *int64) any { return intOrEmpty(v) }

// csvCell formats a raw CSV value the way Python's csv module
// stringifies it: strings as they are, integers in decimal.
func csvCell(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case int64:
		return strconv.FormatInt(value, 10)
	case int:
		return strconv.Itoa(value)
	}
	return ""
}
