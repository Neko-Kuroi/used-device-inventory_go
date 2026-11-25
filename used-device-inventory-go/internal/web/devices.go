package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/service"
)

func (srv *Server) deviceRoutes() {
	mux := srv.mux
	mux.HandleFunc("GET /{$}", srv.requireLogin(srv.deviceList))
	mux.HandleFunc("GET /devices/new", srv.requireLogin(srv.deviceNew))
	mux.HandleFunc("POST /devices/new", srv.requireLogin(srv.deviceNew))
	mux.HandleFunc("GET /devices/{id}", srv.requireLogin(srv.deviceDetail))
	mux.HandleFunc("GET /devices/{id}/edit", srv.requireLogin(srv.deviceEdit))
	mux.HandleFunc("POST /devices/{id}/edit", srv.requireLogin(srv.deviceEdit))
	mux.HandleFunc("POST /devices/{id}/status", srv.requireLogin(srv.deviceStatus))
	mux.HandleFunc("GET /devices/{id}/inspection", srv.requireLogin(srv.deviceInspection))
	mux.HandleFunc("POST /devices/{id}/inspection", srv.requireLogin(srv.deviceInspection))
	mux.HandleFunc("POST /devices/{id}/expenses", srv.requireLogin(srv.expenseAdd))
	mux.HandleFunc("POST /devices/{id}/expenses/{expenseID}/delete", srv.requireLogin(srv.expenseDelete))
}

// deviceID parses the {id} path value the way Flask's <int:>
// converter does: not an integer means no such route (404).
func deviceID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 0 {
		return 0, false
	}
	return id, true
}

func postForm(r *http.Request) service.Form {
	form := service.Form{}
	for key, values := range r.PostForm {
		if len(values) > 0 {
			form[key] = values[0]
		}
	}
	return form
}

// ---------- list ----------

type listPage struct {
	pageData
	Devices  []service.ListRow
	Q        string
	Kind     string
	Status   string
	CSVURL   string
	Kinds    []domain.Option
	Statuses []domain.Option
}

func (p *listPage) page() *pageData { return &p.pageData }

func (srv *Server) deviceList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := service.ListFilter{
		Query:  query.Get("q"),
		Kind:   query.Get("kind"),
		Status: query.Get("status"),
	}
	rows, err := service.ListDevices(r.Context(), srv.db, filter)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	csvParams := url.Values{"q": {filter.Query}, "kind": {filter.Kind},
		"status": {filter.Status}}
	srv.render(w, r, "list", &listPage{
		Devices: rows, Q: filter.Query, Kind: filter.Kind,
		Status: filter.Status, CSVURL: "/export.csv?" + csvParams.Encode(),
		Kinds: domain.KindOptions, Statuses: domain.StatusOptions,
	})
}

// ---------- detail ----------

type nextStatus struct {
	Key         string
	Label       string
	NeedsReason bool
	Blockers    []string
}

type detailPage struct {
	pageData
	Device        *domain.Device
	IMEIView      string
	NextStatuses  []nextStatus
	ErasureByName string
	Expenses      []service.Expense
	ExpenseTotal  int64
	Profit        *int64
	Planned       *int64
	Inspection    domain.InspectionState
	History       []service.HistoryEntry
	Categories    []domain.Option
	Photos        []service.Photo
	PhotoKinds    []domain.Option
}

func (p *detailPage) page() *pageData { return &p.pageData }

func (srv *Server) loadDetail(w http.ResponseWriter, r *http.Request) (*detailPage, bool) {
	ctx := r.Context()
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	device, err := service.GetDevice(ctx, srv.db, id)
	if errors.Is(err, service.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	user := userOf(r)
	imeiView := domain.MaskIMEI(device.IMEI)
	if user != nil && user.IsAdmin && device.IMEI != nil {
		imeiView = *device.IMEI
	}
	var next []nextStatus
	for _, key := range domain.NextStatuses(device.Status) {
		entry := nextStatus{Key: key, Label: domain.StatusLabel(key),
			NeedsReason: domain.NeedsReason(device.Status, key)}
		if key == "ready" {
			entry.Blockers = domain.ReadyBlockers(device)
		}
		next = append(next, entry)
	}
	erasureByName := ""
	if device.DataErasureBy != nil {
		if u, err := service.UserByID(ctx, srv.db, *device.DataErasureBy); err == nil {
			erasureByName = u.Username
		}
	}
	expenses, err := service.ListExpenses(ctx, srv.db, id)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	total, err := service.ExpenseTotal(ctx, srv.db, id)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	state, err := service.InspectionStateFor(ctx, srv.db, device)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	history, err := service.History(ctx, srv.db, id)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	photos, err := service.ListPhotos(ctx, srv.db, id)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	return &detailPage{
		Device: device, IMEIView: imeiView, NextStatuses: next,
		ErasureByName: erasureByName, Expenses: expenses,
		ExpenseTotal: total, Profit: domain.GrossProfit(device, total),
		Planned: domain.PlannedProfit(device, total), Inspection: state,
		History: history, Categories: domain.ExpenseCategoryOptions,
		Photos: photos, PhotoKinds: domain.PhotoKindOptions,
	}, true
}

func (srv *Server) deviceDetail(w http.ResponseWriter, r *http.Request) {
	page, ok := srv.loadDetail(w, r)
	if !ok {
		return
	}
	srv.render(w, r, "detail", page)
}

// ---------- create / edit ----------

// formView holds the device form's values as display strings.
type formView struct {
	Device   *domain.Device // nil for a new device
	IsAdmin  bool
	Kinds    []domain.Option
	Ranks    []string
	Statuses map[string]string
	Limits   []string // network restriction choices

	Kind, Maker, Model, SerialNumber, IMEI, Storage, RAM, Color  string
	BatteryHealth, SIMStatus, ActivationLock, NetworkRestriction string
	CPU, GPU, OSName, BatteryCycles, ConditionRank               string
	CosmeticDamage, Accessories                                  string
	InspectionDone                                               bool
	DataErasureAt, DataErasureMethod                             string
	PurchaseDate, PurchaseSource, PurchasePrice, PlannedPrice    string
	SaleDate, SaleChannel, SalePrice, WarrantyDays, Notes        string
	StatusLabel                                                  string
}

func intStr(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func (srv *Server) buildFormView(r *http.Request, device *domain.Device) *formView {
	user := userOf(r)
	view := &formView{
		Device: device, Kinds: domain.KindOptions, Ranks: domain.Ranks,
		Statuses: domain.Statuses, Limits: domain.NetworkRestrictions,
	}
	if user != nil {
		view.IsAdmin = user.IsAdmin
	}
	if device == nil {
		view.StatusLabel = "仕入済み（登録時はここで固定です）"
		return view
	}
	view.StatusLabel = domain.StatusLabel(device.Status)
	view.Kind = device.Kind
	view.Maker = device.Maker
	view.Model = device.Model
	view.SerialNumber = device.SerialNumber
	if device.IMEI != nil && view.IsAdmin {
		view.IMEI = *device.IMEI
	}
	view.Storage = device.Storage
	view.RAM = device.RAM
	view.Color = device.Color
	view.BatteryHealth = intStr(device.BatteryHealth)
	view.SIMStatus = device.SIMStatus
	view.ActivationLock = device.ActivationLock
	view.NetworkRestriction = device.NetworkRestriction
	view.CPU = device.CPU
	view.GPU = device.GPU
	view.OSName = device.OSName
	view.BatteryCycles = intStr(device.BatteryCycles)
	view.ConditionRank = device.ConditionRank
	view.CosmeticDamage = device.CosmeticDamage
	view.Accessories = device.Accessories
	view.InspectionDone = device.InspectionDone != 0
	view.DataErasureAt = device.DataErasureAt
	view.DataErasureMethod = device.DataErasureMethod
	view.PurchaseDate = device.PurchaseDate
	view.PurchaseSource = device.PurchaseSource
	view.PurchasePrice = intStr(device.PurchasePrice)
	view.PlannedPrice = intStr(device.PlannedPrice)
	view.SaleDate = device.SaleDate
	view.SaleChannel = device.SaleChannel
	view.SalePrice = intStr(device.SalePrice)
	view.WarrantyDays = intStr(device.WarrantyDays)
	view.Notes = device.Notes
	return view
}

type formPage struct {
	pageData
	View *formView
}

func (p *formPage) page() *pageData { return &p.pageData }

func (srv *Server) renderForm(w http.ResponseWriter, r *http.Request, device *domain.Device) {
	srv.render(w, r, "form", &formPage{View: srv.buildFormView(r, device)})
}

func (srv *Server) deviceNew(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		user := userOf(r)
		id, err := service.CreateDevice(r.Context(), srv.db, user.ID, postForm(r))
		switch {
		case err == nil:
			sessionOf(r).flash("登録しました。")
			redirect(w, r, fmt.Sprintf("/devices/%d", id))
			return
		case errors.Is(err, service.ErrDuplicateSerial):
			sessionOf(r).flash(err.Error())
		default:
			var ve *service.ValidationError
			if errors.As(err, &ve) {
				sessionOf(r).flash(ve.Msg)
			} else {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
		}
		// As in the Python version, a failed create re-renders an
		// empty form.
		srv.renderForm(w, r, nil)
		return
	}
	srv.renderForm(w, r, nil)
}

func (srv *Server) deviceEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	device, err := service.GetDevice(ctx, srv.db, id)
	if errors.Is(err, service.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPost {
		user := userOf(r)
		err := service.UpdateDevice(ctx, srv.db, user.ID, user.IsAdmin, id, postForm(r))
		switch {
		case err == nil:
			sessionOf(r).flash("保存しました。")
			redirect(w, r, fmt.Sprintf("/devices/%d", id))
			return
		default:
			var le *service.LockedError
			var ve *service.ValidationError
			switch {
			case errors.As(err, &le):
				// Re-render with the submitted values, so what
				// was typed is not lost.
				sessionOf(r).flash(le.Error())
				srv.renderForm(w, r, le.Values)
				return
			case errors.As(err, &ve):
				sessionOf(r).flash(ve.Msg)
			case errors.Is(err, service.ErrDuplicateSerial):
				sessionOf(r).flash(err.Error())
			default:
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			srv.renderForm(w, r, device)
			return
		}
	}
	srv.renderForm(w, r, device)
}

// ---------- status ----------

func (srv *Server) deviceStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	user := userOf(r)
	err := service.ChangeStatus(r.Context(), srv.db, user.ID, id,
		formValue(r, "to_status"), formValue(r, "reason"))
	sess := sessionOf(r)
	switch {
	case err == nil:
		sess.flash("状態を変更しました。")
	case errors.Is(err, service.ErrNotFound):
		http.NotFound(w, r)
		return
	default:
		var ite *service.InvalidTransitionError
		var be *service.BlockedError
		if errors.As(err, &ite) || errors.As(err, &be) ||
			errors.Is(err, service.ErrReasonRequired) {
			sess.flash(err.Error())
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	redirect(w, r, fmt.Sprintf("/devices/%d", id))
}

// ---------- inspection ----------

type inspectionItemView struct {
	Key       string
	Label     string
	Result    string
	Note      string
	CheckedAt string
}

type inspectionPage struct {
	pageData
	Device *domain.Device
	Items  []inspectionItemView
	State  domain.InspectionState
	Kinds  map[string]string
}

func (p *inspectionPage) page() *pageData { return &p.pageData }

func (srv *Server) deviceInspection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	device, err := service.GetDevice(ctx, srv.db, id)
	if errors.Is(err, service.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodPost {
		inputs := map[string]service.InspectionInput{}
		for _, item := range domain.Checklists[device.Kind] {
			inputs[item.Key] = service.InspectionInput{
				Result: formValue(r, "result_"+item.Key),
				Note:   formValue(r, "note_"+item.Key),
			}
		}
		outcome, err := service.SaveInspection(ctx, srv.db, userOf(r).ID, id, inputs)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if outcome.Demoted {
			sessionOf(r).flash("検査結果を保存しました。条件が崩れたため、状態を「検査中」に戻しました。")
		} else {
			sessionOf(r).flash("検査結果を保存しました。")
		}
		redirect(w, r, fmt.Sprintf("/devices/%d", id))
		return
	}
	rows, err := service.InspectionRows(ctx, srv.db, id)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	state, err := service.InspectionStateFor(ctx, srv.db, device)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	page := &inspectionPage{Device: device, State: state, Kinds: domain.Kinds}
	for _, item := range domain.Checklists[device.Kind] {
		view := inspectionItemView{Key: item.Key, Label: item.Label}
		if row, ok := rows[item.Key]; ok {
			view.Result = row.Result
			view.Note = row.Note
			view.CheckedAt = row.CheckedAt
		}
		page.Items = append(page.Items, view)
	}
	srv.render(w, r, "inspection", page)
}

// ---------- expenses ----------

func (srv *Server) expenseAdd(w http.ResponseWriter, r *http.Request) {
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := service.AddExpense(r.Context(), srv.db, userOf(r).ID, id,
		formValue(r, "category"), formValue(r, "expense_date"),
		formValue(r, "note"), formValue(r, "amount"))
	sess := sessionOf(r)
	switch {
	case err == nil:
		sess.flash("経費を追加しました。")
	case errors.Is(err, service.ErrNotFound):
		http.NotFound(w, r)
		return
	default:
		var ve *service.ValidationError
		if errors.As(err, &ve) {
			sess.flash(ve.Msg)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	redirect(w, r, fmt.Sprintf("/devices/%d", id))
}

func (srv *Server) expenseDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	expenseID, err := strconv.ParseInt(r.PathValue("expenseID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = service.DeleteExpense(r.Context(), srv.db, userOf(r).ID, id, expenseID)
	switch {
	case err == nil:
		sessionOf(r).flash("経費を削除しました。")
	case errors.Is(err, service.ErrNotFound):
		http.NotFound(w, r)
		return
	default:
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	redirect(w, r, fmt.Sprintf("/devices/%d", id))
}
