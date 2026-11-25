// Package web is the HTTP layer: a net/http server with signed
// cookie sessions, CSRF protection on every POST, flash messages,
// and the login/admin guards. Handlers call the service layer; the
// templates mirror the Python version's pages.
package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/service"
)

//go:embed templates
var templatesFS embed.FS

// Server holds the web layer's dependencies.
type Server struct {
	db          *sql.DB
	secretKey   string
	instanceDir string
	templates   map[string]*template.Template
	mux         *http.ServeMux
}

// NewServer builds the server and its routes.
func NewServer(database *sql.DB, secretKey, instanceDir string) (*Server, error) {
	srv := &Server{db: database, secretKey: secretKey, instanceDir: instanceDir}
	if err := srv.loadTemplates(); err != nil {
		return nil, err
	}
	srv.routes()
	return srv, nil
}

func (srv *Server) loadTemplates() error {
	base, err := templatesFS.ReadFile("templates/base.html")
	if err != nil {
		return err
	}
	srv.templates = map[string]*template.Template{}
	for _, page := range []string{"list", "form", "detail", "inspection", "login", "setup", "users"} {
		src, err := templatesFS.ReadFile("templates/" + page + ".html")
		if err != nil {
			return err
		}
		tmpl := template.New(page).Funcs(template.FuncMap{
			"jst":            domain.ToJST,
			"yen":            domain.FormatComma,
			"kindLabel":      func(key string) string { return domain.Kinds[key] },
			"statusLabel":    domain.StatusLabel,
			"catLabel":       func(key string) string { return domain.ExpenseCategories[key] },
			"photoKindLabel": func(key string) string { return domain.PhotoKinds[key] },
			"mask":           domain.MaskIMEI,
			"join":           strings.Join,
			"deref":          func(p *int64) int64 { return *p },
		})
		// Two Parse calls: the page's blocks replace the base's
		// defaults (a single combined Parse would reject the
		// duplicate definitions).
		if _, err := tmpl.Parse(string(base)); err != nil {
			return fmt.Errorf("template base: %w", err)
		}
		if _, err := tmpl.Parse(string(src)); err != nil {
			return fmt.Errorf("template %s: %w", page, err)
		}
		srv.templates[page] = tmpl
	}
	return nil
}

type ctxKey int

const (
	ctxKeySession ctxKey = iota
	ctxKeyUser
)

func sessionOf(r *http.Request) *session {
	return r.Context().Value(ctxKeySession).(*session)
}

func userOf(r *http.Request) *service.User {
	u, _ := r.Context().Value(ctxKeyUser).(*service.User)
	return u
}

// ServeHTTP loads the session and the current user, checks the CSRF
// token on POSTs, dispatches, and saves the session.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sess := srv.loadSession(r)
	sess.ensureCSRF()
	ctx := context.WithValue(r.Context(), ctxKeySession, sess)

	// The current user, as the Python version's load_user: the
	// session's user id, checked against the live row. A deleted
	// user, or a password changed since login (reset, D4), ends
	// the session.
	var user *service.User
	if sess.data.UserID != nil {
		u, err := service.UserByID(r.Context(), srv.db, *sess.data.UserID)
		switch {
		case err != nil:
			sess.clear()
			sess.ensureCSRF()
		case subtle.ConstantTimeCompare(
			[]byte(sess.data.PwGen),
			[]byte(srv.passwordFingerprint(u.PasswordHash))) != 1:
			sess.clear()
			sess.ensureCSRF()
		default:
			user = u
		}
	}
	ctx = context.WithValue(ctx, ctxKeyUser, user)
	r = r.WithContext(ctx)

	rec := &sessionResponseWriter{ResponseWriter: w, srv: srv, sess: sess}
	if r.Method == http.MethodPost {
		// These answer through rec, whose WriteHeader saves the
		// session first (a flash set by the 413 path must reach
		// the client).
		if !srv.bufferBody(rec, r, sess) {
			return
		}
		if !srv.checkCSRF(rec, r, sess) {
			return
		}
	}
	srv.mux.ServeHTTP(rec, r)
	if !rec.wroteHeader {
		rec.WriteHeader(http.StatusOK)
	}
}

// sessionResponseWriter saves the session just before the response
// headers go out, so handlers can mutate the session freely.
type sessionResponseWriter struct {
	http.ResponseWriter
	srv         *Server
	sess        *session
	wroteHeader bool
}

func (w *sessionResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.srv.saveSession(w.ResponseWriter, w.sess)
	w.ResponseWriter.WriteHeader(status)
}

func (w *sessionResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// maxRequestBody caps every POST, as the Python version's
// MAX_CONTENT_LENGTH does: one photo (5 MB) plus room for the form
// fields around it.
const maxRequestBody = domain.MaxPhotoBytes + 64*1024

// bufferBody reads a POST body into memory up to the cap. Over the
// cap, it answers the way the Python version's 413 handler does:
// a flash and a redirect back to the page the form was on (only
// when the referer is this host; the design compares hosts only,
// so an HTTPS referer from tailscale serve is accepted), else to
// the device list.
func (srv *Server) bufferBody(w http.ResponseWriter, r *http.Request, sess *session) bool {
	if r.Body == nil {
		return true
	}
	if r.ContentLength > maxRequestBody {
		srv.tooLarge(w, r, sess)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return false
	}
	if int64(len(body)) > maxRequestBody {
		srv.tooLarge(w, r, sess)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return true
}

func (srv *Server) tooLarge(w http.ResponseWriter, r *http.Request, sess *session) {
	sess.flash("送信が大きすぎます。写真は1枚5MBまでです。")
	target := "/"
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host == r.Host {
			target = ref
		}
	}
	redirect(w, r, target)
}

// checkCSRF validates the csrf_token form field against the session
// token on every POST. A mismatch is a 400.
func (srv *Server) checkCSRF(w http.ResponseWriter, r *http.Request, sess *session) bool {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return false
		}
	} else if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return false
	}
	token := r.PostForm.Get("csrf_token")
	if token == "" || subtle.ConstantTimeCompare(
		[]byte(token), []byte(sess.data.CSRF)) != 1 {
		http.Error(w, "Bad Request: CSRF token が一致しません。", http.StatusBadRequest)
		return false
	}
	return true
}

// pageData is what the base template needs on every page.
type pageData struct {
	User    *service.User
	Flashes []string
	CSRF    string
}

func (srv *Server) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	sess := sessionOf(r)
	if pd, ok := data.(interface{ page() *pageData }); ok {
		base := pd.page()
		base.User = userOf(r)
		base.Flashes = sess.popFlashes()
		base.CSRF = sess.data.CSRF
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := srv.templates[page].ExecuteTemplate(w, "base", data); err != nil {
		// The headers may already be sent; just log to stderr via
		// a plain error after the fact is not possible, so write
		// nothing more.
		_ = err
	}
}

func redirect(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, target, http.StatusFound)
}

// requireLogin mirrors the Python version's login_required: with no
// users at all, everything goes to setup; without a login, to the
// login page with next set to the requested path.
func (srv *Server) requireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := service.CountUsers(r.Context(), srv.db)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if count == 0 {
			redirect(w, r, "/setup")
			return
		}
		if userOf(r) == nil {
			redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path))
			return
		}
		next(w, r)
	}
}

// requireAdmin mirrors admin_required: 403 unless an admin is
// logged in. It runs inside requireLogin on the routes that use it.
func (srv *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userOf(r)
		if u == nil || !u.IsAdmin {
			http.Error(w, "403 Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// formValue reads a POST form field ("" when absent).
func formValue(r *http.Request, name string) string {
	return r.PostForm.Get(name)
}

// hasFormField reports whether a POST form field was submitted with
// a non-empty value (a checkbox).
func hasFormField(r *http.Request, name string) bool {
	_, ok := r.PostForm[name]
	return ok && r.PostForm.Get(name) != ""
}
