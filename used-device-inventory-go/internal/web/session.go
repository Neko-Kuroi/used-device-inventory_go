package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Sessions are client-side, as in the Python version (Flask): the
// cookie holds a JSON payload plus an HMAC-SHA256 signature made
// with the secret key. The server keeps no session state.
//
// The payload carries one field Flask does not: "_pw", a fingerprint
// of the user's password hash at login time. A password reset
// changes the hash, so every session signed in before the reset
// stops matching and is treated as logged out (design decision D4).
// This needs no schema change: the fingerprint is recomputed from
// the user row on every request.
type sessionData struct {
	UserID  *int64   `json:"user_id,omitempty"`
	CSRF    string   `json:"_csrf,omitempty"`
	Flashes []string `json:"_flashes,omitempty"`
	PwGen   string   `json:"_pw,omitempty"`
}

type session struct {
	data     sessionData
	modified bool
	existed  bool // a valid cookie arrived with this request
}

func (s *session) empty() bool {
	return s.data.UserID == nil && s.data.CSRF == "" &&
		len(s.data.Flashes) == 0 && s.data.PwGen == ""
}

func (s *session) flash(message string) {
	s.data.Flashes = append(s.data.Flashes, message)
	s.modified = true
}

// popFlashes returns the pending flashes and clears them (rendering
// a page consumes them, as Flask's get_flashed_messages does).
func (s *session) popFlashes() []string {
	flashes := s.data.Flashes
	if len(flashes) > 0 {
		s.data.Flashes = nil
		s.modified = true
	}
	return flashes
}

func (s *session) clear() {
	s.data = sessionData{}
	s.modified = true
}

func (s *session) ensureCSRF() {
	if s.data.CSRF == "" {
		s.data.CSRF = randomToken()
		s.modified = true
	}
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

const sessionCookieName = "session"

func (srv *Server) sign(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(srv.secretKey))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (srv *Server) loadSession(r *http.Request) *session {
	sess := &session{}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return sess
	}
	payloadB64, sig, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return sess
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return sess
	}
	expected := srv.sign(payload)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return sess
	}
	if err := json.Unmarshal(payload, &sess.data); err != nil {
		return &session{}
	}
	sess.existed = true
	return sess
}

// saveSession writes the session cookie with Flask-like rules: an
// untouched session sets no cookie; an emptied session deletes the
// cookie; a changed one is re-issued.
func (srv *Server) saveSession(w http.ResponseWriter, sess *session) {
	if sess.empty() {
		if sess.existed || sess.modified {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookieName, Value: "", Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				Expires: time.Unix(0, 0), MaxAge: -1,
			})
		}
		return
	}
	if !sess.modified && sess.existed {
		return
	}
	payload, err := json.Marshal(sess.data)
	if err != nil {
		return
	}
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + srv.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// passwordFingerprint derives the "_pw" session field from a stored
// password hash.
func (srv *Server) passwordFingerprint(passwordHash string) string {
	mac := hmac.New(sha256.New, []byte(srv.secretKey))
	mac.Write([]byte("pw:" + passwordHash))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}
