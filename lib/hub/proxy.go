package hub

import (
	"net/http"
	"net/http/httputil"
	"net/url"
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
			if token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		Transport:     h.client.Transport,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			// The instance's cookies are the instance's.
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the instance is unreachable: " + err.Error()})
		},
	}
	rp.ServeHTTP(w, r)
}
