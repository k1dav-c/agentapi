package httpapi

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// CrossOriginProtection refuses the requests that change something (any
// method but GET, HEAD and OPTIONS) that a browser sends for a page of
// another origin. CORS doesn't stop them: a page can send a form, or a
// fetch in "no-cors" mode, without asking first, and the server acts on it
// though the page can't read the answer. Without --api-token, any site the
// user opened could otherwise send the agent a message or type into the
// shell terminal.
//
// The rules are those of Go 1.25's http.CrossOriginProtection. A request
// whose Sec-Fetch-Site is same-origin or none (the user typed the URL) is
// let through; so is one without Sec-Fetch-Site whose Origin is the
// request's own host, or that has neither header (not a browser: curl, an
// SDK, a hub). Otherwise its Origin must match one of trusted, which may
// hold one "*" each as --allowed-origins does (https://*.example.com). "*"
// alone trusts none: it lets pages read the answers, which a server
// without a token gives anyone already.
func CrossOriginProtection(trusted []string, refused http.Handler) func(http.Handler) http.Handler {
	var origins trustedOrigins
	for _, origin := range trusted {
		origin = strings.ToLower(origin)
		if origin == "*" {
			continue
		}
		if prefix, suffix, ok := strings.Cut(origin, "*"); ok {
			origins.patterns = append(origins.patterns, [2]string{prefix, suffix})
			continue
		}
		origins.exact = append(origins.exact, origin)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if crossOriginAllowed(r, origins) {
				next.ServeHTTP(w, r)
				return
			}
			refused.ServeHTTP(w, r)
		})
	}
}

type trustedOrigins struct {
	exact    []string
	patterns [][2]string
}

func (t trustedOrigins) match(origin string) bool {
	origin = strings.ToLower(origin)
	if origin == "" {
		return false
	}
	if slices.Contains(t.exact, origin) {
		return true
	}
	for _, p := range t.patterns {
		if len(origin) >= len(p[0])+len(p[1]) && strings.HasPrefix(origin, p[0]) && strings.HasSuffix(origin, p[1]) {
			return true
		}
	}
	return false
}

func crossOriginAllowed(r *http.Request, trusted trustedOrigins) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// An older browser, or no browser at all.
		if origin == "" {
			return true
		}
		if u, err := url.Parse(origin); err == nil && u.Host == r.Host {
			return true
		}
	}
	return trusted.match(origin)
}
