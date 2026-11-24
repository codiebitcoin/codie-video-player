package web

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// auth keeps the viewer's logins: who may in, and for how long. The pair
// comes from the environment; a server started without one makes a random
// password and says it once in the log, so whoever started it can in.
type auth struct {
	user, pass string

	mu       sync.Mutex
	sessions map[string]time.Time // token -> when it goes stale
}

// cookie is the login's name in the browser.
const cookieName = "cvp_session"

// newAuth takes the pair from the environment, or makes one up.
func newAuth(user, pass string) *auth {
	if user == "" || pass == "" {
		user, pass = "codie", randomPassword()
		log.Printf("web: AUTH_USER/AUTH_PASS not set; login with %s / %s (change them in the environment)", user, pass)
	}
	return &auth{
		user:     user,
		pass:     pass,
		sessions: map[string]time.Time{},
	}
}

// handleLogin answers a name and a password with a cookie.
func (a *auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	var q struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	if q.User != a.user || q.Pass != a.pass {
		http.Error(w, `{"error":"用户名或密码不对"}`, http.StatusUnauthorized)
		return
	}
	token := randomToken()
	a.mu.Lock()
	a.sessions[token] = time.Now().Add(sessionTTL)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(`{"ok":true}`))
}

// handleLogout takes the login away.
func (a *auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	w.Write([]byte(`{"ok":true}`))
}

// guard wraps a handler with the login: without a live cookie, the API says
// 401 and the page answers with its own.
func (a *auth) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.live(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") || isAssetPath(r.URL.Path) {
				http.Error(w, `{"error":"请先登录"}`, http.StatusUnauthorized)
				return
			}
			// A page ask: the single-page app takes it from here and shows
			// the login itself.
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAssetPath says whether the path is one of the web app's own files.
func isAssetPath(p string) bool {
	return !strings.ContainsRune(strings.TrimPrefix(p, "/"), '/') &&
		filepath.Ext(p) != ""
}

// live says whether the request carries a cookie still good.
func (a *auth) live(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	at, ok := a.sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(at) {
		delete(a.sessions, c.Value)
		return false
	}
	return true
}

// randomPassword is twelve letters and digits, for the made-up pair.
func randomPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzACDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 12)
	for i := range out {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		out[i] = chars[n.Int64()]
	}
	return string(out)
}
