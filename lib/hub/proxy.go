package hub

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	pathpkg "path"
	"strings"

	"github.com/go-chi/chi/v5"
)

// instanceParam is the {id} of the route, unescaped: an ID such as
// dev-1:3284 comes escaped from a browser.
func instanceParam(r *http.Request) string {
	id := chi.URLParam(r, "id")
	if unescaped, err := url.PathUnescape(id); err == nil {
		return unescaped
	}
	return id
}

// proxy sends /i/{id}/<path> to <instance URL>/<path> with the instance's
// token in place of the browser's credentials: the hub's cookie and any
// Authorization header stay with the hub. Event streams are passed on as
// they come.
func (h *Hub) proxy(w http.ResponseWriter, r *http.Request) {
	id := instanceParam(r)
	instance, ok := h.store.get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such instance"})
		return
	}
	if instance.answeredBy != "" {
		// Its approvals would go to another agent.
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "another AgentAPI answers at this instance's URL (" + instance.answeredBy + ")"})
		return
	}
	target, err := url.Parse(instance.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the instance's URL is invalid"})
		return
	}
	// The path after /i/<id>, as the browser escaped it.
	_, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), "/i/"), "/")
	path := "/" + rest
	if changesWebhook(r.Method, path) {
		// The instance's webhook is how it reaches the hub; changed from
		// here, it could send the hub's events, and the token the hub
		// sends to the URL they name, elsewhere.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "change an instance's webhook on its machine (agentapi hub join or leave), not through the hub"})
		return
	}
	token := h.tokenFor(instance)
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.RawPath = strings.TrimSuffix(target.EscapedPath(), "/") + path
			pr.Out.URL.Path, _ = url.PathUnescape(pr.Out.URL.RawPath)
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			// The instance checks the origin of browsers that call it;
			// through the hub, the hub is the caller.
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Del("Referer")
			for _, name := range []string{"Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
				pr.Out.Header.Del(name)
			}
			if token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		Transport:     h.client.Transport,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			// The instance's cookies are the instance's.
			resp.Header.Del("Set-Cookie")
			// A page an instance served would run as the hub's, with the
			// hub's cookie and every other instance within reach: nothing
			// it answers runs as a page of the hub's origin. The chat in
			// the hub's frame is the hub's own.
			resp.Header.Add("Content-Security-Policy", "sandbox")
			resp.Header.Set("X-Content-Type-Options", "nosniff")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the instance is unreachable: " + err.Error()})
		},
	}
	rp.ServeHTTP(w, r)
}

// changesWebhook reports whether a request for path (escaped) would change
// the instance's webhook: PUT /webhook, POST /webhook/test.
func changesWebhook(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	unescaped, err := url.PathUnescape(path)
	if err != nil {
		return true
	}
	clean := pathpkg.Clean("/" + unescaped)
	return clean == "/webhook" || strings.HasPrefix(clean, "/webhook/")
}
