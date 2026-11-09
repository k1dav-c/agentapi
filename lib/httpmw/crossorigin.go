package httpmw

import (
	"net/http"
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
// net/http's CrossOriginProtection decides: a request whose Sec-Fetch-Site
// is same-origin or none (the user typed the URL) is let through; so is
// one without Sec-Fetch-Site whose Origin is the request's own host, or
// that has neither header (not a browser: curl, an SDK, a hub). Otherwise
// its Origin must be one of trusted, or match one of trusted that holds a
// "*" as --allowed-origins does (https://*.example.com), in any case. "*"
// alone trusts none: it lets pages read the answers, which a server
// without a token gives anyone already.
func CrossOriginProtection(trusted []string, refused http.Handler) func(http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	var patterns originPatterns
	for _, origin := range trusted {
		origin = strings.ToLower(origin)
		if origin == "*" {
			continue
		}
		if prefix, suffix, ok := strings.Cut(origin, "*"); ok {
			patterns = append(patterns, [2]string{prefix, suffix})
			continue
		}
		// An origin it refuses (no scheme or host, or a path) is never
		// the Origin of a request.
		_ = protection.AddTrustedOrigin(origin)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if protection.Check(r) == nil || patterns.match(r.Header.Get("Origin")) {
				next.ServeHTTP(w, r)
				return
			}
			refused.ServeHTTP(w, r)
		})
	}
}

// originPatterns are the trusted origins with a "*", as prefix and suffix
// in lower case.
type originPatterns [][2]string

func (p originPatterns) match(origin string) bool {
	if origin == "" {
		return false
	}
	origin = strings.ToLower(origin)
	for _, pattern := range p {
		if len(origin) >= len(pattern[0])+len(pattern[1]) && strings.HasPrefix(origin, pattern[0]) && strings.HasSuffix(origin, pattern[1]) {
			return true
		}
	}
	return false
}
