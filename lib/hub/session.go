package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The browser's session: a cookie derived from the hub token, which a
// changed token invalidates. SameSite=Lax keeps other sites from sending
// it with a POST.
const (
	sessionCookie  = "agentapi_hub_session"
	sessionContext = "agentapi-hub-session-v1"
	sessionMaxAge  = 30 * 24 * time.Hour
)

// sessionValue is the cookie's value: hex(HMAC-SHA256(key=token,
// msg=sessionContext)), derived as lib/httpapi derives the chat's.
func (h *Hub) sessionValue() string {
	mac := hmac.New(sha256.New, []byte(h.config.Token))
	mac.Write([]byte(sessionContext))
	return hex.EncodeToString(mac.Sum(nil))
}

// signedIn reports whether r carries the session cookie or the hub token;
// everyone is, on a hub without a token.
func (h *Hub) signedIn(r *http.Request) bool {
	if h.config.NoToken {
		return true
	}
	if c, err := r.Cookie(sessionCookie); err == nil && hmac.Equal([]byte(c.Value), []byte(h.sessionValue())) {
		return true
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return hmac.Equal([]byte(bearer), []byte(h.config.Token))
	}
	return false
}

func (h *Hub) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.signedIn(r) {
			writeError(w, http.StatusUnauthorized, "sign in to the hub")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionBody is GET, POST and DELETE /api/session: whether the browser is
// signed in.
type sessionBody struct {
	Authenticated bool `json:"authenticated"`
}

// signInBody is POST /api/session.
type signInBody struct {
	Token string `json:"token"`
}

func (h *Hub) getSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sessionBody{Authenticated: h.signedIn(r)})
}

func (h *Hub) createSession(w http.ResponseWriter, r *http.Request) {
	var body signInBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "send {\"token\": ...}")
		return
	}
	if h.config.NoToken {
		writeJSON(w, http.StatusOK, sessionBody{Authenticated: true})
		return
	}
	if !hmac.Equal([]byte(body.Token), []byte(h.config.Token)) {
		writeError(w, http.StatusUnauthorized, "wrong token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: h.sessionValue(), Path: "/", MaxAge: int(sessionMaxAge.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, http.StatusOK, sessionBody{Authenticated: true})
}

func (h *Hub) deleteSession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, sessionBody{Authenticated: false})
}
