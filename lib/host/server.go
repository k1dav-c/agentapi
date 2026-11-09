package host

import (
	"context"
	"crypto/hmac"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/jsonapi"
)

// peerKey is the context key of the socket's peer process.
type peerKey struct{}

// StartResponse is POST /jobs's answer.
type StartResponse struct {
	Job     JobInfo `json:"job"`
	Created bool    `json:"created"`
}

// JobsResponse is GET /jobs.
type JobsResponse struct {
	Jobs []JobInfo `json:"jobs"`
}

// Handler is the launcher's API for the hub, behind its token.
func (l *Launcher) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(l.requireToken)
	r.Get("/host", func(w http.ResponseWriter, r *http.Request) {
		jsonapi.Write(w, http.StatusOK, l.Info(r.Context()))
	})
	r.Get("/jobs", func(w http.ResponseWriter, _ *http.Request) {
		jsonapi.Write(w, http.StatusOK, JobsResponse{Jobs: l.Jobs()})
	})
	r.Post("/jobs", func(w http.ResponseWriter, r *http.Request) {
		var req StartRequest
		if !jsonapi.Read(w, r, &req) {
			return
		}
		job, created, err := l.Start(r.Context(), req)
		if err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		jsonapi.Write(w, status, StartResponse{Job: job, Created: created})
	})
	r.Post("/jobs/{key}/message", func(w http.ResponseWriter, r *http.Request) {
		key, ok := pathKey(w, r)
		if !ok {
			return
		}
		var req MessageRequest
		if !jsonapi.Read(w, r, &req) {
			return
		}
		status, body, err := l.Message(r.Context(), key, req)
		if err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	r.Delete("/jobs/{key}", func(w http.ResponseWriter, r *http.Request) {
		key, ok := pathKey(w, r)
		if !ok {
			return
		}
		q := r.URL.Query()
		opts := StopOptions{KeepWorktree: q.Get("keep_worktree") == "true", KeepState: q.Get("keep_state") == "true"}
		if err := l.Stop(r.Context(), key, opts); err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return r
}

func (l *Launcher) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !hmac.Equal([]byte(bearer), []byte(l.hostToken)) {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusUnauthorized, "unauthorized", "send the launcher's token: Authorization: Bearer <token>"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ServeSocket answers the jobs' git and gh on the launcher's unix socket,
// readable by its user only, until ctx ends.
func (l *Launcher) ServeSocket(ctx context.Context) error {
	path := l.config.SocketPath()
	// The socket is made at the umask's mode before its chmod: the
	// directory keeps others out meanwhile.
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+tokenPath, func(w http.ResponseWriter, r *http.Request) {
		key, err := ParseKey(r.URL.Query().Get("job"))
		if err != nil {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "%v", err))
			return
		}
		caller, _ := r.Context().Value(peerKey{}).(int)
		token, err := l.token(r.Context(), key, caller)
		if err != nil {
			jsonapi.WriteError(w, err)
			return
		}
		jsonapi.Write(w, http.StatusOK, token)
	})
	srv := &http.Server{
		Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		// Which process asks: a token goes only to its job's processes.
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, peerKey{}, peerPID(c))
		},
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	_ = os.Remove(path)
	return nil
}

func pathKey(w http.ResponseWriter, r *http.Request) (Key, bool) {
	key, err := ParseKey(chi.URLParam(r, "key"))
	if err != nil {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "%v", err))
		return Key{}, false
	}
	return key, true
}
