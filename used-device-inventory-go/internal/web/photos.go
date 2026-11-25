package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"used-device-inventory-go/internal/domain"
	"used-device-inventory-go/internal/service"
)

func (srv *Server) photoRoutes() {
	srv.mux.HandleFunc("POST /devices/{id}/photos", srv.requireLogin(srv.photoAdd))
	srv.mux.HandleFunc("GET /devices/{id}/photos/{photoID}", srv.requireLogin(srv.photoView))
	srv.mux.HandleFunc("POST /devices/{id}/photos/{photoID}/delete", srv.requireLogin(srv.photoDelete))
}

func (srv *Server) photosDir() string {
	return filepath.Join(srv.instanceDir, "photos")
}

func (srv *Server) photoAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := service.GetDevice(ctx, srv.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	sess := sessionOf(r)
	detail := fmt.Sprintf("/devices/%d", id)
	kind := formValue(r, "kind")
	file, header, err := r.FormFile("photo")
	_, kindOK := domain.PhotoKinds[kind]
	if !kindOK || err != nil || header.Filename == "" {
		sess.flash("写真の種別とファイルを選んでください。")
		redirect(w, r, detail)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxPhotoBytes+1))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	err = service.AddPhoto(ctx, srv.db, srv.photosDir(), userOf(r).ID, id,
		kind, header.Filename, data)
	switch {
	case err == nil:
		sess.flash("写真を追加しました。")
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
	redirect(w, r, detail)
}

func (srv *Server) photoView(w http.ResponseWriter, r *http.Request) {
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	photoID, err := strconv.ParseInt(r.PathValue("photoID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	photo, err := service.GetPhoto(r.Context(), srv.db, id, photoID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(srv.photosDir(),
		strconv.FormatInt(id, 10), filepath.Base(photo.Filename)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := "application/octet-stream"
	switch filepath.Ext(photo.Filename) {
	case ".jpg":
		contentType = "image/jpeg"
	case ".png":
		contentType = "image/png"
	case ".webp":
		contentType = "image/webp"
	}
	w.Header().Set("Content-Type", contentType)
	// The magic-byte check only proves the file starts like an
	// image; tell browsers never to sniff it as anything else.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

func (srv *Server) photoDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := deviceID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	photoID, err := strconv.ParseInt(r.PathValue("photoID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = service.DeletePhoto(r.Context(), srv.db, srv.photosDir(),
		userOf(r).ID, id, photoID)
	switch {
	case err == nil:
		sessionOf(r).flash("写真を削除しました。")
	case errors.Is(err, service.ErrNotFound):
		http.NotFound(w, r)
		return
	default:
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	redirect(w, r, fmt.Sprintf("/devices/%d", id))
}
