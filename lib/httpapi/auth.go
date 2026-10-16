package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
)

const (
	// sessionCookieName is the cookie the chat UI signs in with. Browsers
	// can't add an Authorization header to EventSource or <img> requests,
	// so the UI trades the API token for this cookie once.
	sessionCookieName = "agentapi_session"
	// sessionCookieContext is the HMAC message the cookie value is derived
	// with. Bump the version to invalidate every cookie already issued.
	sessionCookieContext = "agentapi-session-v1"
	// authSessionPath is exempt from tokenAuth.middleware; its handlers check
	// credentials themselves.
	authSessionPath = "/auth/session"
)

// sessionCookieValue derives the session cookie value from the API token:
// hex(HMAC-SHA256(key=token, msg=sessionCookieContext)). The raw token is
// never stored in the browser, and changing the token invalidates every
// cookie issued for the old one.
func sessionCookieValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(sessionCookieContext))
	return hex.EncodeToString(mac.Sum(nil))
}

// secretEqual compares two secrets in constant time. Both sides are hashed
// first so the comparison doesn't leak the expected length either.
func secretEqual(given, expected string) bool {
	g := sha256.Sum256([]byte(given))
	e := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(g[:], e[:]) == 1
}

// tokenAuth checks requests against the configured API token. A request is
// authenticated by either an `Authorization: Bearer <token>` header or the
// session cookie issued by POST /auth/session.
//
// CSRF: the session cookie is a credential the browser attaches by itself,
// so cross-site request forgery has to be ruled out. The cookie is
// SameSite=Strict, so browsers only send it on requests initiated by a page
// of the same site: a form post, <img> or fetch from another site goes out
// without it and gets a 401. Same-site pages on other origins (e.g. another
// port on localhost) can only read responses, or send non-simple requests
// such as JSON POSTs, if they pass the CORS allowlist (--allowed-origins);
// the remaining simple cross-origin requests from same-site pages are the
// same exposure the API had before authentication existed. POST
// /auth/session itself needs the Bearer token, which a forged request
// doesn't have, so it can't be used to plant a session either.
type tokenAuth struct {
	token  string
	cookie string
}

func newTokenAuth(token string) *tokenAuth {
	return &tokenAuth{token: token, cookie: sessionCookieValue(token)}
}

// bearerValid reports whether the Authorization header carries the token.
func (a *tokenAuth) bearerValid(header string) bool {
	// The scheme is case-insensitive (RFC 9110).
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	return secretEqual(header[len(prefix):], a.token)
}

// cookieValid reports whether the request carries a valid session cookie.
func (a *tokenAuth) cookieValid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return secretEqual(c.Value, a.cookie)
}

func (a *tokenAuth) authenticated(r *http.Request) bool {
	return a.bearerValid(r.Header.Get("Authorization")) || a.cookieValid(r)
}

// middleware enforces token authentication on all requests except static
// file routes (/chat/* and /), so browsers can load the UI, and
// /auth/session, whose handlers check credentials themselves. CORS
// preflight requests are answered by the CORS middleware before this one
// runs.
func (a *tokenAuth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || strings.HasPrefix(path, "/chat") || path == authSessionPath {
			next.ServeHTTP(w, r)
			return
		}
		if !a.authenticated(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agentapi"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AuthSessionBody describes whether the API needs a token and whether the
// caller has one.
type AuthSessionBody struct {
	AuthRequired  bool `json:"auth_required" doc:"Whether the server was started with --api-token."`
	Authenticated bool `json:"authenticated" doc:"Whether this request carried a valid Bearer token or session cookie. Always true when auth_required is false."`
}

type AuthSessionResponse struct {
	SetCookie string `header:"Set-Cookie" doc:"The session cookie (POST) or its removal (DELETE)."`
	Body      AuthSessionBody
}

// authSessionInput gives the handlers the underlying request, which they
// need to read the session cookie and to tell whether it came over TLS.
type authSessionInput struct {
	req *http.Request
}

func (i *authSessionInput) Resolve(ctx huma.Context) []error {
	i.req, _ = humachi.Unwrap(ctx)
	return nil
}

// requestIsHTTPS reports whether the client reached the server over TLS,
// directly or through a proxy that says so in X-Forwarded-Proto.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func sessionCookie(r *http.Request, value string, maxAge int) string {
	c := http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	}
	return c.String()
}

func (s *Server) authSessionState(r *http.Request) AuthSessionBody {
	if s.auth == nil {
		return AuthSessionBody{AuthRequired: false, Authenticated: true}
	}
	return AuthSessionBody{AuthRequired: true, Authenticated: s.auth.authenticated(r)}
}

func (s *Server) getAuthSession(_ context.Context, input *authSessionInput) (*AuthSessionResponse, error) {
	return &AuthSessionResponse{Body: s.authSessionState(input.req)}, nil
}

func (s *Server) createAuthSession(_ context.Context, input *authSessionInput) (*AuthSessionResponse, error) {
	resp := &AuthSessionResponse{}
	if s.auth == nil {
		resp.Body = s.authSessionState(input.req)
		return resp, nil
	}
	if !s.auth.bearerValid(input.req.Header.Get("Authorization")) {
		return nil, huma.Error401Unauthorized("a valid Authorization: Bearer <token> header is required")
	}
	// A session cookie, no Max-Age: it lasts until the browser closes.
	resp.SetCookie = sessionCookie(input.req, s.auth.cookie, 0)
	resp.Body = AuthSessionBody{AuthRequired: true, Authenticated: true}
	return resp, nil
}

func (s *Server) deleteAuthSession(_ context.Context, input *authSessionInput) (*AuthSessionResponse, error) {
	resp := &AuthSessionResponse{}
	if s.auth != nil {
		resp.SetCookie = sessionCookie(input.req, "", -1)
	}
	resp.Body = s.authSessionState(input.req)
	// The cookie is being removed, so only a Bearer header still counts.
	if s.auth != nil {
		resp.Body.Authenticated = s.auth.bearerValid(input.req.Header.Get("Authorization"))
	}
	return resp, nil
}

func (s *Server) registerAuthRoutes() {
	huma.Get(s.api, authSessionPath, s.getAuthSession, func(o *huma.Operation) {
		o.Tags = []string{"Auth"}
		o.Summary = "Get authentication state"
		o.Description = "Reports whether the server requires an API token and whether this request is authenticated. Reachable without credentials."
	})
	huma.Post(s.api, authSessionPath, s.createAuthSession, func(o *huma.Operation) {
		o.Tags = []string{"Auth"}
		o.Summary = "Sign in a browser"
		o.Description = "Requires `Authorization: Bearer <token>` and sets an HttpOnly, SameSite=Strict session cookie that authenticates later requests, including EventSource and image requests that can't send headers. The cookie holds a value derived from the token, not the token itself, so changing the token signs every browser out. Without --api-token it sets nothing."
		o.Errors = []int{401}
	})
	huma.Delete(s.api, authSessionPath, s.deleteAuthSession, func(o *huma.Operation) {
		o.Tags = []string{"Auth"}
		o.Summary = "Sign out a browser"
		o.Description = "Removes the session cookie. Reachable without credentials."
	})
}
