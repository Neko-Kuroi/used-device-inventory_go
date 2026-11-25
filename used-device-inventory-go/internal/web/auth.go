package web

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"used-device-inventory-go/internal/auth"
	"used-device-inventory-go/internal/service"
	"used-device-inventory-go/internal/util"
)

func (srv *Server) routes() {
	mux := http.NewServeMux()
	srv.mux = mux
	mux.HandleFunc("GET /setup", srv.setup)
	mux.HandleFunc("POST /setup", srv.setup)
	mux.HandleFunc("GET /login", srv.login)
	mux.HandleFunc("POST /login", srv.login)
	mux.HandleFunc("POST /logout", srv.logout)
	mux.HandleFunc("GET /admin/users", srv.requireLogin(srv.requireAdmin(srv.adminUsers)))
	mux.HandleFunc("POST /admin/users", srv.requireLogin(srv.requireAdmin(srv.adminUsers)))
	srv.deviceRoutes()
	srv.photoRoutes()
	srv.csvRoutes()
}

type plainPage struct {
	pageData
}

func (p *plainPage) page() *pageData { return &p.pageData }

// setup creates the first user (an admin) while no users exist,
// then logs them in.
func (srv *Server) setup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, err := service.CountUsers(ctx, srv.db)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		redirect(w, r, "/login")
		return
	}
	sess := sessionOf(r)
	if r.Method == http.MethodPost {
		username := util.PyStrip(formValue(r, "username"))
		password := formValue(r, "password")
		if username == "" || utf8.RuneCountInString(password) < auth.MinLength {
			sess.flash("ユーザー名と、8文字以上のパスワードを入れてください。")
		} else {
			hash, err := auth.Hash(password)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			id, err := service.CreateUser(ctx, srv.db, username, hash, true)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			sess.data.UserID = &id
			sess.data.PwGen = srv.passwordFingerprint(hash)
			sess.modified = true
			sess.flash("管理者を作成しました。")
			redirect(w, r, "/")
			return
		}
	}
	srv.render(w, r, "setup", &plainPage{})
}

// login checks the credentials and redirects to next (internal
// paths only) or the device list.
func (srv *Server) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	count, err := service.CountUsers(ctx, srv.db)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		redirect(w, r, "/setup")
		return
	}
	sess := sessionOf(r)
	if r.Method == http.MethodPost {
		username := util.PyStrip(formValue(r, "username"))
		password := formValue(r, "password")
		user, err := service.UserByUsername(ctx, srv.db, username)
		if err == nil && auth.Check(user.PasswordHash, password) {
			sess.data.UserID = &user.ID
			sess.data.PwGen = srv.passwordFingerprint(user.PasswordHash)
			sess.modified = true
			redirect(w, r, util.SafeNext(r.URL.Query().Get("next")))
			return
		}
		sess.flash("ユーザー名かパスワードが違います。")
	}
	srv.render(w, r, "login", &plainPage{})
}

// logout clears the session.
func (srv *Server) logout(w http.ResponseWriter, r *http.Request) {
	sessionOf(r).clear()
	redirect(w, r, "/login")
}

type usersPage struct {
	pageData
	Users []*service.User
}

func (p *usersPage) page() *pageData { return &p.pageData }

// adminUsers lists the users and adds new ones. As in the Python
// version, a POST re-renders the page (no redirect).
func (srv *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionOf(r)
	if r.Method == http.MethodPost {
		username := util.PyStrip(formValue(r, "username"))
		password := formValue(r, "password")
		isAdmin := hasFormField(r, "is_admin")
		if username == "" || utf8.RuneCountInString(password) < auth.MinLength {
			sess.flash("ユーザー名と、8文字以上のパスワードを入れてください。")
		} else {
			hash, err := auth.Hash(password)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			_, err = service.CreateUser(ctx, srv.db, username, hash, isAdmin)
			switch {
			case errors.Is(err, service.ErrDuplicateUsername):
				sess.flash("そのユーザー名は既に使われています。")
			case err != nil:
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			default:
				sess.flash("ユーザーを追加しました。")
			}
		}
	}
	users, err := service.ListUsers(ctx, srv.db)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	srv.render(w, r, "users", &usersPage{Users: users})
}
