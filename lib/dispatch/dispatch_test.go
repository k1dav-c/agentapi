package dispatch

import (
	"cmp"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/github"
	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/jsonapi"
)

// fakeLauncher is a machine's launcher: it records what the hub asks.
type fakeLauncher struct {
	t      *testing.T
	srv    *httptest.Server
	name   string
	token  string
	mu     sync.Mutex
	starts []host.StartRequest
	stops  []string
	sends  []host.MessageRequest
	jobs   map[string]host.JobInfo
	// gate, when set, holds POST /jobs until it closes; onStart, when set,
	// answers it, after the start is recorded.
	gate    chan struct{}
	onStart func(w http.ResponseWriter, job host.JobInfo)
	maxJobs int
}

func newFakeLauncher(t *testing.T, name string) *fakeLauncher {
	f := &fakeLauncher{t: t, name: name, token: "host-token-" + name, jobs: map[string]host.JobInfo{}}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+f.token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Post("/jobs", func(w http.ResponseWriter, r *http.Request) {
		var req host.StartRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		f.mu.Lock()
		f.starts = append(f.starts, req)
		gate, onStart := f.gate, f.onStart
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
		key := host.Key{Issue: req.Issue}
		key.Owner, key.Name, _ = host.ParseRepo(req.Repo)
		job := host.JobInfo{Key: key.String(), Agent: req.Agent, InstanceID: f.name + "~" + key.Slug(), DefaultBranch: "main", State: host.JobRunning}
		if onStart != nil {
			onStart(w, job)
			return
		}
		f.mu.Lock()
		f.jobs[key.String()] = job
		f.mu.Unlock()
		jsonapi.Write(w, http.StatusCreated, host.StartResponse{Job: job, Created: true})
	})
	r.Post("/jobs/{key}/message", func(w http.ResponseWriter, r *http.Request) {
		var req host.MessageRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		f.mu.Lock()
		f.sends = append(f.sends, req)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	r.Delete("/jobs/{key}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.stops = append(f.stops, chi.URLParam(r, "key")+"?"+r.URL.RawQuery)
		k, _ := host.ParseKey(chi.URLParam(r, "key"))
		delete(f.jobs, k.String())
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewServer(r)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLauncher) event(agents ...string) host.HostEventData {
	f.mu.Lock()
	defer f.mu.Unlock()
	var jobs []host.JobInfo
	for _, j := range f.jobs {
		jobs = append(jobs, j)
	}
	return host.HostEventData{
		HostInfo: host.HostInfo{
			Hostname: f.name, URL: f.srv.URL, Repos: []string{"acme/widget"}, Agents: agents,
			DefaultAgent: agents[0], MaxJobs: cmp.Or(f.maxJobs, 2), Clone: true, Jobs: jobs,
		},
		Token: f.token, HeartbeatSeconds: 60,
	}
}

func join(t *testing.T, s *Service, f *fakeLauncher, agents ...string) {
	t.Helper()
	data, _ := json.Marshal(f.event(agents...))
	require.NoError(t, s.HostEvent(host.EventHostHeartbeat, data))
}

// fakeGitHub is GitHub's API for an app installed on acme/widget.
type fakeGitHub struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	mu       sync.Mutex
	calls    []string
	comments map[int64]string
	tokens   int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	g := &fakeGitHub{t: t, key: key, comments: map[int64]string{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) pem() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(g.key)}))
}

// checkJWT verifies the app's JWT: signed with its key, for app 42.
func (g *fakeGitHub) checkJWT(r *http.Request) bool {
	jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(jwt, ".")
	if !ok || len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&g.key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	return strings.Contains(string(claims), `"iss":"42"`)
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.calls = append(g.calls, r.Method+" "+r.URL.Path)
	g.mu.Unlock()
	token := r.Header.Get("Authorization") == "token ghs_widget"
	path := r.URL.Path
	switch {
	case r.Method == "POST" && path == "/app-manifests/the-code/conversions":
		jsonapi.Write(w, 201, map[string]any{"id": 42, "slug": "agentapi-hub-x", "client_id": "Iv1", "client_secret": "cs", "pem": g.pem(), "webhook_secret": "ws", "owner": map[string]string{"login": "acme", "type": "Organization"}})
	case r.Method == "POST" && path == "/app-manifests/another-code/conversions":
		// The same key and ID, for the fake's JWT check: another app all the same.
		jsonapi.Write(w, 201, map[string]any{"id": 42, "slug": "agentapi-hub-y", "client_id": "Iv2", "client_secret": "cs", "pem": g.pem(), "webhook_secret": "ws", "owner": map[string]string{"login": "dave", "type": "User"}})
	case r.Method == "GET" && path == "/users/agentapi-hub-x[bot]":
		jsonapi.Write(w, 200, map[string]any{"id": 99})
	case r.Method == "GET" && path == "/app/installations/7" && g.checkJWT(r):
		jsonapi.Write(w, 200, map[string]any{"id": 7, "account": map[string]string{"login": "acme", "type": "Organization"}, "repository_selection": "selected"})
	case r.Method == "GET" && path == "/repos/acme/widget/installation" && g.checkJWT(r):
		jsonapi.Write(w, 200, map[string]any{"id": 7})
	case r.Method == "GET" && strings.HasSuffix(path, "/installation"):
		jsonapi.Write(w, 404, map[string]string{"message": "Not Found"})
	case r.Method == "POST" && path == "/app/installations/7/access_tokens" && g.checkJWT(r):
		var body struct {
			Repositories []string `json:"repositories"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Repositories) != 1 || body.Repositories[0] != "widget" {
			jsonapi.Write(w, 422, map[string]string{"message": "one repository"})
			return
		}
		g.mu.Lock()
		g.tokens++
		g.mu.Unlock()
		jsonapi.Write(w, 201, map[string]any{"token": "ghs_widget", "expires_at": time.Now().Add(time.Hour)})
	case !token:
		jsonapi.Write(w, 401, map[string]string{"message": "Bad credentials"})
	case r.Method == "GET" && path == "/repos/acme/widget":
		jsonapi.Write(w, 200, map[string]any{"full_name": "acme/widget", "default_branch": "trunk"})
	case r.Method == "GET" && path == "/repos/acme/widget/issues/12":
		jsonapi.Write(w, 200, map[string]any{
			"number": 12, "title": "Fix it", "html_url": "https://github.com/acme/widget/issues/12",
			"labels": []map[string]string{{"name": "agent:codex"}, {"name": "agent:claude"}, {"name": "bug"}},
		})
	case r.Method == "POST" && strings.HasPrefix(path, "/repos/acme/widget/issues/") && strings.HasSuffix(path, "/comments"):
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		id := int64(len(g.comments) + 1)
		g.comments[id] = path + "\n" + body.Body
		g.mu.Unlock()
		jsonapi.Write(w, 201, map[string]any{"id": id})
	case r.Method == "PATCH" && strings.HasPrefix(path, "/repos/acme/widget/issues/comments/"):
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id, _ := strconv.ParseInt(strings.TrimPrefix(path, "/repos/acme/widget/issues/comments/"), 10, 64)
		g.mu.Lock()
		g.comments[id] = "edited\n" + body.Body
		g.mu.Unlock()
		jsonapi.Write(w, 200, map[string]any{"id": id})
	case r.Method == "DELETE":
		w.WriteHeader(204)
	default:
		jsonapi.Write(w, 404, map[string]string{"message": "Not Found: " + r.Method + " " + path})
	}
}

func (g *fakeGitHub) called(call string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.calls {
		if c == call {
			return true
		}
	}
	return false
}

func newService(t *testing.T, g *fakeGitHub) (*Service, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	client := github.NewClient()
	if g != nil {
		client.API, client.Web = g.srv.URL, "https://github.example"
	}
	dir := t.TempDir()
	s, err := New(Config{
		StatePath: filepath.Join(dir, "dispatch.json"), AppPath: filepath.Join(dir, "github-app.json"),
		Secret: "hub-secret", DispatchToken: "dispatch-token", GitHub: client,
		Now: func() time.Time { return now }, Location: time.UTC,
	})
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s, &now
}

func TestDispatchWithoutGitHub(t *testing.T) {
	ctx := context.Background()
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude", "codex")

	resp, err := s.Dispatch(ctx, Request{Action: ActionStart, DeliveryID: "run-1", Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.True(t, resp.Created)
	// The hub answers once the job starts; the launcher starts it after.
	assert.Equal(t, JobStarting, resp.Job.State)
	assert.Equal(t, "pve-1", resp.Job.Host)
	s.waitStarts()
	resp.Job = *s.job("acme/widget#12")
	assert.Equal(t, JobWorking, resp.Job.State)
	assert.Equal(t, "pve-1", resp.Job.Host)
	assert.Equal(t, "claude", resp.Job.Agent)
	assert.Equal(t, "pve-1~acme~widget~12", resp.Job.InstanceID)
	require.Len(t, l.starts, 1)
	start := l.starts[0]
	assert.Equal(t, "claude", start.Agent)
	assert.Empty(t, start.Credentials, "without the app the machine's own credentials")
	assert.Nil(t, start.GitUser)
	assert.Contains(t, start.Prompt, "acme/widget#12（https://github.com/acme/widget/issues/12）")
	assert.Contains(t, start.Prompt, "這台主機自己的 GitHub 憑證")

	// The same delivery again, and the same start again.
	resp, err = s.Dispatch(ctx, Request{Action: ActionStart, DeliveryID: "run-1", Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.True(t, resp.Duplicate)
	resp, err = s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Agent: "claude"})
	require.NoError(t, err)
	assert.False(t, resp.Created)
	assert.Len(t, l.starts, 1)

	resp, err = s.Dispatch(ctx, Request{Action: ActionLinkPR, Repo: "acme/widget", Issue: 12, PRURL: "https://github.com/acme/widget/pull/34"})
	require.NoError(t, err)
	assert.Equal(t, JobReview, resp.Job.State)

	_, err = s.Dispatch(ctx, Request{
		Action: ActionSend, Repo: "acme/widget", Issue: 12, Author: "dave", ReviewState: "changes_requested",
		Message: "請改名", Comments: []ReviewComment{{Path: "main.go", Line: 3, Body: "這裡"}},
	})
	require.NoError(t, err)
	require.Len(t, l.sends, 1)
	assert.Contains(t, l.sends[0].Content, "PR https://github.com/acme/widget/pull/34 有新的 review（dave，changes_requested）")
	fence := regexp.MustCompile(`BEGIN-([0-9a-f]{12})\n請改名\n- main\.go:3 這裡\nEND-([0-9a-f]{12})`).FindStringSubmatch(l.sends[0].Content)
	require.NotNil(t, fence, l.sends[0].Content)
	assert.Equal(t, fence[1], fence[2], "the user's text between a random fence")
	assert.Equal(t, JobWorking, s.job("acme/widget#12").State)

	// The tasks' events add up the run's cost.
	s.TaskEvent("pve-1~acme~widget~12", "task.completed", json.RawMessage(`{"duration_seconds":90,"cost":{"usd":0.5,"tokens":{"input":10,"output":2000,"cache_read":80000},"calls":4}}`))
	s.TaskEvent("pve-1~acme~widget~12", "task.completed", json.RawMessage(`{"duration_seconds":30,"cost":{"usd":0.25,"tokens":{"input":5,"output":1000},"calls":2}}`))
	s.TaskEvent("someone-else", "task.completed", json.RawMessage(`{"cost":{"usd":100}}`))
	job := s.job("acme/widget#12")
	assert.Equal(t, JobReview, job.State, "a task ended with a pull request open")
	assert.InDelta(t, 0.75, job.Runs[0].Cost.USD, 1e-9)
	assert.Equal(t, 2, job.Runs[0].Cost.Tasks)
	assert.Equal(t, int64(3000), job.Runs[0].Cost.Tokens.Output)

	*now = now.Add(time.Minute)
	resp, err = s.Dispatch(ctx, Request{Action: ActionStop, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.Equal(t, JobDone, resp.Job.State)
	assert.Equal(t, []string{"acme~widget~12?"}, l.stops)
	assert.Equal(t, "stopped", resp.Job.Runs[0].EndReason)

	_, err = s.Dispatch(ctx, Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Message: "x"})
	assert.True(t, jsonapi.IsCode(err, "job_ended"))

	// The dispatch keeps it all.
	s.Flush()
	again, err := New(s.config)
	require.NoError(t, err)
	defer again.Close()
	assert.Equal(t, JobDone, again.job("acme/widget#12").State)
	assert.Len(t, again.Hosts(), 1)
}

// pickHost is pickHostLocked with s.mu held.
func (s *Service) pickHost(key host.Key, agent, want string) (Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pickHostLocked(key, agent, want, nil)
}

func TestPickHost(t *testing.T) {
	s, now := newService(t, nil)
	a := newFakeLauncher(t, "a")
	b := newFakeLauncher(t, "b")
	join(t, s, a, "claude")
	data, _ := json.Marshal(host.HostEventData{HostInfo: host.HostInfo{Hostname: "b", URL: b.srv.URL, Agents: []string{"codex"}, DefaultAgent: "codex", MaxJobs: 1, Clone: true}, Token: b.token})
	require.NoError(t, s.HostEvent(host.EventHostHeartbeat, data))
	key := host.Key{Owner: "acme", Name: "widget", Issue: 1}

	h, err := s.pickHost(key, "", "")
	require.NoError(t, err)
	assert.Equal(t, "a", h.Hostname, "the one with the repository")
	h, err = s.pickHost(key, "codex", "")
	require.NoError(t, err)
	assert.Equal(t, "b", h.Hostname, "the one that runs the agent, cloning")
	_, err = s.pickHost(key, "pi", "")
	assert.True(t, jsonapi.IsCode(err, "no_host"))
	_, err = s.pickHost(key, "codex", "a")
	assert.True(t, jsonapi.IsCode(err, "agent_unavailable"))

	// A job the hub started counts before the machine's next heartbeat
	// says so.
	_, err = s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 9, Agent: "codex"})
	require.NoError(t, err)
	s.waitStarts()
	_, err = s.pickHost(key, "codex", "")
	assert.True(t, jsonapi.IsCode(err, "no_host"), "b is full")
	assert.Equal(t, 1, s.Hosts()[1].Active)

	*now = now.Add(4 * time.Minute)
	_, err = s.pickHost(key, "", "a")
	assert.True(t, jsonapi.IsCode(err, "host_offline"), "missed three heartbeats")
}

func TestDispatchWithGitHubApp(t *testing.T) {
	ctx := context.Background()
	g := newFakeGitHub(t)
	s, _ := newService(t, g)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude", "codex")

	// Connect: the manifest's code, then the installation.
	r := chi.NewRouter()
	s.MountSession(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(srv.URL + "/github/callback?code=the-code&state=forged")
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, err = noRedirect.Get(srv.URL + "/github/callback?code=the-code&state=" + s.newState())
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Location"), "https://github.example/apps/agentapi-hub-x/installations/new?state="))
	resp, err = http.Get(srv.URL + "/github/setup?installation_id=7&setup_action=install")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	app, err := LoadApp(s.config.AppPath)
	require.NoError(t, err)
	assert.Equal(t, int64(42), app.ID)
	assert.Equal(t, int64(99), app.BotUserID)
	assert.Equal(t, srv.URL, app.HubURL)
	assert.Equal(t, "Organization", app.OwnerType)
	assert.Equal(t, []github.Installation{{ID: 7, Account: "acme", AccountType: "Organization", RepositorySelection: "selected"}}, app.Installations)

	// A repository the app isn't on is refused.
	_, err = s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/other", Issue: 1})
	assert.True(t, jsonapi.IsCode(err, "github_not_installed"))

	started := startJob(t, s, Request{Repo: "acme/widget", Issue: 12, Agent: "claude"})
	start := l.starts[0]
	assert.Equal(t, host.CredentialsHub, start.Credentials)
	assert.Equal(t, &host.GitUser{Name: "agentapi-hub-x[bot]", Email: "99+agentapi-hub-x[bot]@users.noreply.github.com"}, start.GitUser)
	assert.Equal(t, "trunk", start.DefaultBranch)
	assert.Contains(t, start.Prompt, "base 是 trunk")
	assert.Equal(t, "Fix it", started.Job.Title)
	assert.True(t, started.Job.Credentials)
	assert.True(t, g.called("DELETE /repos/acme/widget/issues/12/labels/agent:codex"), "the other agent's label goes")
	assert.False(t, g.called("DELETE /repos/acme/widget/issues/12/labels/agent:claude"))
	assert.Contains(t, g.comments[1], "Claude Code 在 `pve-1` 上開始處理這個 issue")

	// The launcher gets the job's token, signed with the hub's secret and
	// with its own token. A request sent again has the same signature.
	launcher := chi.NewRouter()
	s.MountLauncher(launcher)
	lsrv := httptest.NewServer(launcher)
	defer lsrv.Close()
	type ask struct {
		hostname, job, secret, bearer string
		// ago is how many seconds before now it was signed: the same ago
		// makes the same signature.
		ago int
	}
	send := func(a ask) (int, string) {
		body, _ := json.Marshal(host.HubTokenRequest{Hostname: a.hostname, Job: a.job})
		req, _ := http.NewRequest("POST", lsrv.URL+host.HubTokenPath, strings.NewReader(string(body)))
		ts := strconv.FormatInt(s.config.Now().Unix()-int64(a.ago), 10)
		req.Header.Set(instanceapi.HeaderTimestamp, ts)
		req.Header.Set(instanceapi.HeaderSignature, instanceapi.Sign(a.secret, ts, body))
		req.Header.Set(instanceapi.HeaderDelivery, fmt.Sprintf("delivery-%d", a.ago))
		req.Header.Set("Authorization", "Bearer "+a.bearer)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	good := ask{hostname: "pve-1", job: "acme~widget~12", secret: "hub-secret", bearer: l.token, ago: 1}
	status, body := send(good)
	assert.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"token":"ghs_widget"`)
	status, _ = send(good)
	assert.Equal(t, http.StatusConflict, status, "a copy, even with another delivery ID")
	for name, bad := range map[string]ask{
		"another secret":               {hostname: "pve-1", job: "acme~widget~12", secret: "wrong", bearer: l.token, ago: 2},
		"without the launcher's token": {hostname: "pve-1", job: "acme~widget~12", secret: "hub-secret", bearer: "wrong", ago: 3},
		"another machine":              {hostname: "pve-2", job: "acme~widget~12", secret: "hub-secret", bearer: l.token, ago: 4},
		"no such job":                  {hostname: "pve-1", job: "acme~widget~99", secret: "hub-secret", bearer: l.token, ago: 5},
	} {
		status, _ := send(bad)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, status, name)
	}
	assert.Equal(t, 1, g.tokens, "the token is kept, not asked again")

	// Another agent takes over on the same machine and worktree.
	_, err = s.Dispatch(ctx, Request{Action: ActionLinkPR, Repo: "acme/widget", Issue: 12, PRURL: "https://github.com/acme/widget/pull/34"})
	require.NoError(t, err)
	replaced, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Agent: "codex", Replace: ptr(false)})
	assert.True(t, jsonapi.IsCode(err, "job_active"))
	assert.Empty(t, replaced.Job.Key)
	replaced = startJob(t, s, Request{Repo: "acme/widget", Issue: 12, Agent: "codex"})
	assert.Equal(t, []string{"acme~widget~12?keep_worktree=true&keep_state=true"}, l.stops)
	require.Len(t, l.starts, 2)
	assert.Equal(t, "codex", l.starts[1].Agent)
	assert.Contains(t, l.starts[1].Prompt, "之前由 Claude Code 處理")
	assert.Contains(t, l.starts[1].Prompt, "PR 是 https://github.com/acme/widget/pull/34")
	require.Len(t, replaced.Job.Runs, 2)
	assert.Equal(t, "replaced", replaced.Job.Runs[0].EndReason)
	assert.Equal(t, "codex", replaced.Job.Agent)
	assert.Equal(t, "https://github.com/acme/widget/pull/34", replaced.Job.PRURL, "the pull request stays")

	s.TaskEvent("pve-1~acme~widget~12", "task.completed", json.RawMessage(`{"duration_seconds":600,"cost":{"usd":1.5,"tokens":{"output":5000}}}`))
	stopped, err := s.Dispatch(ctx, Request{Action: ActionStop, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.Equal(t, JobDone, stopped.Job.State)
	assert.True(t, g.called("DELETE /repos/acme/widget/git/refs/heads/agent/issue-12"))
	var cost string
	for _, c := range g.comments {
		if strings.Contains(c, "花費彙總") {
			cost = c
		}
	}
	assert.True(t, strings.HasPrefix(cost, "/repos/acme/widget/issues/34/comments"), "on the pull request")
	assert.Contains(t, cost, "| 1 | Claude Code | pve-1 |")
	assert.Contains(t, cost, "| 2 | Codex | pve-1 |")
	assert.Contains(t, cost, "**$1.50**")
	assert.NotZero(t, s.job("acme/widget#12").CostCommentID)

	// A new round on the same issue adds to the same job and edits the
	// same comment.
	startJob(t, s, Request{Repo: "acme/widget", Issue: 12, Agent: "claude"})
	_, err = s.Dispatch(ctx, Request{Action: ActionStop, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	job := s.job("acme/widget#12")
	assert.Len(t, job.Runs, 3)
	assert.Empty(t, job.PRURL)
	edited := 0
	for _, c := range g.comments {
		if strings.HasPrefix(c, "edited\n") {
			edited++
		}
	}
	assert.Equal(t, 0, edited, "a new round clears the old comment")
}

func ptr[T any](v T) *T { return &v }

func TestHostReconcile(t *testing.T) {
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	startJob(t, s, Request{Repo: "acme/widget", Issue: 5})
	// A heartbeat the launcher made before it started the job comes after
	// it: the job, just started, stays.
	l.mu.Lock()
	l.jobs = map[string]host.JobInfo{}
	l.mu.Unlock()
	join(t, s, l, "claude")
	assert.Equal(t, JobWorking, s.job("acme/widget#5").State)
	// Later, the launcher still doesn't have it (stopped by hand).
	*now = now.Add(3 * time.Minute)
	join(t, s, l, "claude")
	job := s.job("acme/widget#5")
	assert.Equal(t, JobFailed, job.State)
	assert.Contains(t, job.Error, "no longer")
	assert.Equal(t, 0, s.Hosts()[0].Active, "a failed job holds no place")

	// Started again, it gets a run of its own: its tasks' cost counts.
	again := startJob(t, s, Request{Repo: "acme/widget", Issue: 5})
	require.Len(t, again.Job.Runs, 2)
	assert.Equal(t, "failed", again.Job.Runs[0].EndReason)
	s.TaskEvent(again.Job.InstanceID, "task.completed", json.RawMessage(`{"cost":{"usd":0.4}}`))
	assert.InDelta(t, 0.4, s.job("acme/widget#5").Runs[1].Cost.USD, 1e-9)
}

// A machine's name is pinned to the launcher's token it first came with:
// another machine that has the hub's secret can't take it over, by any
// event, online or not.
func TestHostTakeOverRefused(t *testing.T) {
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	forged := l.event("claude")
	forged.Token, forged.URL = "attacker", "http://attacker:3291"
	data, _ := json.Marshal(forged)
	pinned := func() {
		t.Helper()
		s.mu.Lock()
		defer s.mu.Unlock()
		assert.Equal(t, l.token, s.hosts["pve-1"].Token)
		assert.Equal(t, l.srv.URL, s.hosts["pve-1"].URL)
	}

	// A forged stop doesn't stop the machine, nor free its name for the
	// heartbeat after it.
	assert.Error(t, s.HostEvent(host.EventHostStopped, data))
	assert.True(t, s.Hosts()[0].Online)
	assert.Error(t, s.HostEvent(host.EventHostHeartbeat, data))
	assert.Error(t, s.HostEvent(host.EventHostStarted, data))
	pinned()

	// Nor once the machine is offline, stopped or silent.
	real, _ := json.Marshal(l.event("claude"))
	require.NoError(t, s.HostEvent(host.EventHostStopped, real))
	assert.Error(t, s.HostEvent(host.EventHostHeartbeat, data))
	*now = now.Add(10 * time.Minute)
	assert.Error(t, s.HostEvent(host.EventHostStarted, data))
	pinned()

	// An event without a token doesn't count.
	forged.Token = ""
	data, _ = json.Marshal(forged)
	assert.Error(t, s.HostEvent(host.EventHostHeartbeat, data))
	pinned()

	// The real launcher comes back.
	require.NoError(t, s.HostEvent(host.EventHostStarted, real))
	assert.True(t, s.Hosts()[0].Online)
}

// Forgetting a machine frees its name for a launcher with another token:
// not while it is online or has jobs that work.
func TestForgetHost(t *testing.T) {
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	_, err := s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 4})
	require.NoError(t, err)
	s.waitStarts()

	r := chi.NewRouter()
	s.MountSession(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	forget := func(name string) (int, string) {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/hosts/"+name, nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		code, _ := jsonapi.ReadError(data)
		return resp.StatusCode, code
	}
	status, code := forget("nowhere")
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "host_not_found", code)
	status, code = forget("pve-1")
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "host_online", code)

	// The machine went away with its launcher's token; a new launcher
	// under its name is refused.
	*now = now.Add(10 * time.Minute)
	rekeyed := newFakeLauncher(t, "pve-1")
	rekeyed.token = "a-new-token"
	data, _ := json.Marshal(rekeyed.event("claude"))
	assert.Error(t, s.HostEvent(host.EventHostStarted, data))
	status, code = forget("pve-1")
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "host_busy", code, "its job works")

	_, err = s.Dispatch(context.Background(), Request{Action: ActionStop, Repo: "acme/widget", Issue: 4})
	require.NoError(t, err)
	assert.Equal(t, JobStopped, s.job("acme/widget#4").State, "stopped while the machine was offline")
	status, _ = forget("pve-1")
	assert.Equal(t, http.StatusNoContent, status)
	assert.Empty(t, s.Hosts())

	// The new launcher joins; the job stopped while it was away is done.
	require.NoError(t, s.HostEvent(host.EventHostStarted, data))
	require.Len(t, s.Hosts(), 1)
	assert.True(t, s.Hosts()[0].Online)
	s.mu.Lock()
	assert.Equal(t, "a-new-token", s.hosts["pve-1"].Token)
	s.mu.Unlock()
	assert.Equal(t, JobDone, s.job("acme/widget#4").State)
}

// A switch that fails after the old agent stopped leaves the job failed,
// not working with no agent.
func TestSwitchAgentFailure(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude", "codex")
	startJob(t, s, Request{Repo: "acme/widget", Issue: 7, Agent: "claude"})
	l.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusInternalServerError, "start_failed", "codex didn't start"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// The hub answers before the launcher refuses.
	_, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 7, Agent: "codex"})
	require.NoError(t, err)
	s.waitStarts()
	job := s.job("acme/widget#7")
	assert.Equal(t, JobFailed, job.State)
	assert.Contains(t, job.Error, "codex didn't start")
	assert.Nil(t, job.current(), "no run goes on")
}

// A new round's comment counts its own runs, and the rounds before in one
// line.
func TestCostCommentRounds(t *testing.T) {
	job := &Job{Round: 2, Runs: []Run{
		{Round: 1, Agent: "claude", Host: "a", Cost: Cost{USD: 3, Tasks: 4}},
		{Round: 2, Agent: "codex", Host: "a", Cost: Cost{USD: 0.5, Tasks: 1}},
	}}
	comment := costComment(job, time.UTC)
	assert.Contains(t, comment, "| 1 | Codex | a |")
	assert.NotContains(t, comment, "Claude Code")
	assert.Contains(t, comment, "**$0.50**")
	assert.Contains(t, comment, "之前幾輪的花費另計：4 個任務，$3.00")
}

// Forget while a token is asked for neither panics nor keeps the old
// app's token.
func TestForgetWhileAsking(t *testing.T) {
	g := newFakeGitHub(t)
	client := github.NewClient()
	client.API = g.srv.URL
	app := &github.App{ID: 42, PEM: g.pem()}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = client.RepoToken(context.Background(), app, "acme/widget") }()
		go func() { defer wg.Done(); client.Forget() }()
	}
	wg.Wait()
}

// A job keeps the messages its agents were sent: from the hub's page, a
// review or an /agent comment, the last maxMessages, each cut short.
func TestJobMessages(t *testing.T) {
	ctx := context.Background()
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude", "codex")
	_, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	s.waitStarts()

	_, err = s.Dispatch(FromHub(ctx), Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Author: "hub", Message: "  Also the docs  "})
	require.NoError(t, err)
	*now = now.Add(time.Minute)
	_, err = s.Dispatch(ctx, Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Author: "dave", ReviewState: "changes_requested", Message: "Rename it", Comments: []ReviewComment{{Path: "a.go", Body: "x"}, {Path: "b.go", Body: "y"}}})
	require.NoError(t, err)
	_, err = s.Dispatch(ctx, Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Author: "erin", Message: "please rebase"})
	require.NoError(t, err)

	job := s.job("acme/widget#12")
	require.Len(t, job.Messages, 3)
	assert.Equal(t, Message{At: now.Add(-time.Minute), Source: SourceHub, Agent: "claude", Text: "Also the docs"}, job.Messages[0], "the hub's page has no author")
	assert.Equal(t, Message{At: *now, Source: SourceReview, Author: "dave", Agent: "claude", Text: "Rename it", ReviewState: "changes_requested", Comments: 2}, job.Messages[1])
	assert.Equal(t, SourceComment, job.Messages[2].Source)
	assert.Equal(t, "erin", job.Messages[2].Author)

	for i := range maxMessages {
		_, err = s.Dispatch(ctx, Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Message: fmt.Sprintf("m%d %s", i, strings.Repeat("é", maxMessageText))})
		require.NoError(t, err)
	}
	job = s.job("acme/widget#12")
	require.Len(t, job.Messages, maxMessages)
	assert.True(t, strings.HasPrefix(job.Messages[0].Text, "m0 "), "the oldest went")
	assert.Equal(t, maxMessageText, len([]rune(job.Messages[0].Text)))
	assert.True(t, strings.HasSuffix(job.Messages[0].Text, "…"))

	// dispatch.json keeps them; a file of a hub before them reads.
	s.Flush()
	again, err := New(s.config)
	require.NoError(t, err)
	defer again.Close()
	assert.Len(t, again.job("acme/widget#12").Messages, maxMessages)
	var old Job
	require.NoError(t, json.Unmarshal([]byte(`{"key":"acme/widget#1","state":"done","runs":[]}`), &old))
	assert.Empty(t, old.Messages)
}

// The dispatch's subscribers hear of each change of the machines and the
// jobs, and of a machine that goes offline without a word.
func TestBoardSubscribe(t *testing.T) {
	s, now := newService(t, nil)
	changes, cancel := s.Subscribe()
	defer cancel()
	heard := func() bool {
		select {
		case <-changes:
			return true
		default:
			return false
		}
	}
	assert.False(t, heard())

	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	assert.True(t, heard(), "a machine joined")
	board := s.Board()
	require.Len(t, board.Hosts, 1)
	assert.True(t, board.Hosts[0].Online)
	assert.Empty(t, board.Jobs)

	startJob(t, s, Request{Repo: "acme/widget", Issue: 12})
	assert.True(t, heard(), "a job started")
	assert.False(t, heard(), "changes close together come as one")
	require.Len(t, s.Board().Jobs, 1)

	s.CheckOnline()
	assert.False(t, heard(), "nothing changed")
	*now = now.Add(4 * time.Minute)
	s.CheckOnline()
	assert.True(t, heard(), "three heartbeats missed")
	assert.False(t, s.Board().Hosts[0].Online)
	s.CheckOnline()
	assert.False(t, heard(), "told once")

	cancel()
	join(t, s, l, "claude")
	assert.False(t, heard(), "cancelled")
}

// The repositories come the latest pushed or changed first.
func TestReposLatestFirst(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse(time.DateOnly, s); return v }
	repos := []github.Repo{
		{FullName: "a/old", PushedAt: at("2026-01-01")},
		{FullName: "a/new", PushedAt: at("2026-11-01")},
		{FullName: "a/touched", PushedAt: at("2026-02-01"), UpdatedAt: at("2026-11-05")},
		{FullName: "a/never"},
	}
	sortRepos(repos)
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
	}
	assert.Equal(t, []string{"a/touched", "a/new", "a/old", "a/never"}, names)
}

// Over HTTP, a signed-in browser's send is the hub's; one with the
// dispatch token is the workflow's, even on a hub without sign-in.
func TestMessageSourceOverHTTP(t *testing.T) {
	s, _ := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	startJob(t, s, Request{Repo: "acme/widget", Issue: 12})
	r := chi.NewRouter()
	s.MountDispatch(r, func(*http.Request) bool { return true })
	srv := httptest.NewServer(r)
	defer srv.Close()
	send := func(bearer string) {
		body := strings.NewReader(`{"action":"send","repo":"acme/widget","issue":12,"author":"hub","message":"hi"}`)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/dispatch", body)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	send("")
	send("dispatch-token")
	messages := s.job("acme/widget#12").Messages
	require.Len(t, messages, 2)
	assert.Equal(t, SourceHub, messages[0].Source)
	assert.Empty(t, messages[0].Author)
	assert.Equal(t, SourceComment, messages[1].Source)
	assert.Equal(t, "hub", messages[1].Author)
}

func TestGitHubStatus(t *testing.T) {
	web := "https://github.example/"
	assert.Equal(t, GitHubStatus{Installations: []InstallationStatus{}}, githubStatus(web, nil))

	org := &github.App{Slug: "agentapi-hub-x", Owner: "acme", OwnerType: "Organization", Installations: []github.Installation{
		{ID: 7, Account: "acme", AccountType: "Organization", RepositorySelection: "selected"},
		{ID: 8, Account: "dave", AccountType: "User", RepositorySelection: "all"},
	}}
	assert.Equal(t, GitHubStatus{
		Connected: true, Slug: "agentapi-hub-x", Owner: "acme", OwnerType: "Organization",
		HTMLURL:     "https://github.example/apps/agentapi-hub-x",
		SettingsURL: "https://github.example/organizations/acme/settings/apps/agentapi-hub-x",
		InstallURL:  "https://github.example/apps/agentapi-hub-x/installations/new",
		Installations: []InstallationStatus{
			{ID: 7, Account: "acme", AccountType: "Organization", RepositorySelection: "selected", AvatarURL: "https://github.example/acme.png?size=96", SettingsURL: "https://github.example/organizations/acme/settings/installations/7"},
			{ID: 8, Account: "dave", AccountType: "User", RepositorySelection: "all", AvatarURL: "https://github.example/dave.png?size=96", SettingsURL: "https://github.example/settings/installations/8"},
		},
	}, githubStatus(web, org))

	// An app from before the hub kept the owner's kind: its owner's own
	// installation says it, once one says its kind.
	old := &github.App{Slug: "agentapi-hub-x", Owner: "dave", Installations: []github.Installation{{ID: 8, Account: "dave", RepositorySelection: "all"}}}
	status := githubStatus(web, old)
	assert.Empty(t, status.OwnerType)
	assert.Empty(t, status.SettingsURL)
	assert.Empty(t, status.Installations[0].SettingsURL)
	old.Installations[0].AccountType = "User"
	status = githubStatus(web, old)
	assert.Equal(t, "User", status.OwnerType)
	assert.Equal(t, "https://github.example/settings/apps/agentapi-hub-x", status.SettingsURL)
	assert.Empty(t, old.OwnerType, "the app itself is left as it is")
}

// A connect while the hub has an app keeps that one until GitHub hands the
// new one back: a connect left on GitHub changes nothing.
func TestReconnectReplacesTheAppOnlyOnceSaved(t *testing.T) {
	ctx := context.Background()
	g := newFakeGitHub(t)
	s, _ := newService(t, g)
	r := chi.NewRouter()
	s.MountSession(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) *http.Response {
		resp, err := noRedirect.Get(srv.URL + path)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	assert.Equal(t, http.StatusFound, get("/github/callback?code=the-code&state="+s.newState()).StatusCode)
	assert.Equal(t, http.StatusOK, get("/github/setup?installation_id=7&setup_action=install").StatusCode)
	_, err := s.gh.RepoToken(ctx, s.currentApp(), "acme/widget")
	require.NoError(t, err)
	require.Equal(t, 1, g.tokens)

	// The connect page names the app it would replace.
	resp := get("/github/connect")
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "這個 hub 現在用的是 agentapi-hub-x")
	assert.Equal(t, "agentapi-hub-x", s.currentApp().Slug, "a connect alone changes nothing")
	saved, err := LoadApp(s.config.AppPath)
	require.NoError(t, err)
	assert.Equal(t, "agentapi-hub-x", saved.Slug)

	// GitHub hands the new app back: it replaces the old one, which had
	// its installations, and the old one's tokens go.
	assert.Equal(t, http.StatusFound, get("/github/callback?code=another-code&state="+s.newState()).StatusCode)
	saved, err = LoadApp(s.config.AppPath)
	require.NoError(t, err)
	assert.Equal(t, "agentapi-hub-y", saved.Slug)
	assert.Equal(t, "User", saved.OwnerType)
	assert.Empty(t, saved.Installations)
	assert.Equal(t, "agentapi-hub-y", s.currentApp().Slug)
	_, err = s.gh.RepoToken(ctx, s.currentApp(), "acme/widget")
	require.NoError(t, err)
	assert.Equal(t, 2, g.tokens, "a token of the new app, not the old one's kept")
}

// waitStarts waits for the starts on their way.
func (s *Service) waitStarts() { s.starts.Wait() }

// startJob dispatches req, a start, and waits until the launcher answered.
func startJob(t *testing.T, s *Service, req Request) Response {
	t.Helper()
	req.Action = ActionStart
	resp, err := s.Dispatch(context.Background(), req)
	require.NoError(t, err)
	s.waitStarts()
	if resp.Job.Key != "" {
		resp.Job = *s.job(resp.Job.Key)
	}
	return resp
}

// hangUp is an onStart that closes the connection without an answer.
func hangUp(w http.ResponseWriter, _ host.JobInfo) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func (f *fakeLauncher) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeLauncher) stopped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stops)
}

// dispatchSoon is s.Dispatch(ctx, req), which must answer within seconds.
func dispatchSoon(t *testing.T, s *Service, ctx context.Context, req Request) (Response, error) {
	t.Helper()
	type result struct {
		resp Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := s.Dispatch(ctx, req)
		done <- result{resp, err}
	}()
	select {
	case r := <-done:
		return r.resp, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatch didn't answer while the launcher starts the job")
		return Response{}, nil
	}
}

// The hub answers a start once the job is starting on a machine, while
// the launcher clones and starts it; the start goes on when the request
// that asked for it ends.
func TestStartAnswersAtOnce(t *testing.T) {
	s, _ := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	l.gate = make(chan struct{})
	join(t, s, l, "claude")
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := dispatchSoon(t, s, ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.True(t, resp.Created)
	assert.Equal(t, JobStarting, resp.Job.State)
	assert.Equal(t, "pve-1", resp.Job.Host)
	assert.Equal(t, "claude", resp.Job.Agent)
	assert.Equal(t, 1, s.Hosts()[0].Active, "the starting job holds its place")
	cancel()

	// Starting, it is the same start again, and gets no messages yet.
	again, err := s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.False(t, again.Created)
	_, err = s.Dispatch(context.Background(), Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Message: "hi"})
	assert.True(t, jsonapi.IsCode(err, "job_starting"))
	_, err = s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Agent: "codex"})
	assert.True(t, jsonapi.IsCode(err, "job_starting"))

	close(l.gate)
	s.waitStarts()
	job := s.job("acme/widget#12")
	assert.Equal(t, JobWorking, job.State)
	assert.Equal(t, "pve-1~acme~widget~12", job.InstanceID)
	require.Len(t, job.Runs, 1)
	assert.Equal(t, 1, l.startCount())
}

// A start whose answer the hub lost (the call failed after the launcher
// took it, or the hub restarted) is adopted from the machine's next
// report; one the launcher never had fails once the report is late.
func TestStartWithoutAnswer(t *testing.T) {
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	l.onStart = func(w http.ResponseWriter, job host.JobInfo) {
		l.mu.Lock()
		l.jobs[job.Key] = job
		l.mu.Unlock()
		hangUp(w, job)
	}
	_, err := s.Dispatch(context.Background(), Request{Action: ActionStart, DeliveryID: "run-1", Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	s.waitStarts()
	assert.Equal(t, JobStarting, s.job("acme/widget#12").State, "no answer isn't a failure")

	// The hub restarts, then hears from the machine.
	s.Close()
	s, err = New(s.config)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	join(t, s, l, "claude")
	job := s.job("acme/widget#12")
	assert.Equal(t, JobWorking, job.State)
	assert.Equal(t, "pve-1~acme~widget~12", job.InstanceID)
	require.Len(t, job.Runs, 1)
	assert.Equal(t, "pve-1", job.Runs[0].Host)
	assert.Empty(t, l.stopped(), "adopted, not stopped")

	// A start the launcher never had.
	l.onStart = hangUp
	_, err = s.Dispatch(context.Background(), Request{Action: ActionStart, DeliveryID: "run-2", Repo: "acme/widget", Issue: 13})
	require.NoError(t, err)
	s.waitStarts()
	join(t, s, l, "claude")
	assert.Equal(t, JobStarting, s.job("acme/widget#13").State, "the report may be older than the start")
	*now = now.Add(3 * time.Minute)
	join(t, s, l, "claude")
	job = s.job("acme/widget#13")
	assert.Equal(t, JobFailed, job.State)
	assert.Contains(t, job.Error, "didn't start")

	// Its delivery is forgotten: the workflow run again starts it again.
	l.onStart = nil
	resp, err := s.Dispatch(context.Background(), Request{Action: ActionStart, DeliveryID: "run-2", Repo: "acme/widget", Issue: 13})
	require.NoError(t, err)
	assert.False(t, resp.Duplicate)
	s.waitStarts()
	assert.Equal(t, JobWorking, s.job("acme/widget#13").State)
}

// A launcher's refusal fails the job, and forgets the start's delivery.
func TestStartRefused(t *testing.T) {
	g := newFakeGitHub(t)
	s, _ := newService(t, g)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	s.app = &github.App{ID: 42, Slug: "agentapi-hub-x", PEM: g.pem(), Installations: []github.Installation{{ID: 7, Account: "acme"}}}
	l.onStart = func(w http.ResponseWriter, _ host.JobInfo) {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusInternalServerError, "start_failed", "the clone failed"))
	}
	_, err := s.Dispatch(context.Background(), Request{Action: ActionStart, DeliveryID: "run-1", Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	s.waitStarts()
	job := s.job("acme/widget#12")
	assert.Equal(t, JobFailed, job.State)
	assert.Contains(t, job.Error, "the clone failed")
	g.mu.Lock()
	var said string
	for _, c := range g.comments {
		said += c
	}
	g.mu.Unlock()
	assert.Contains(t, said, "沒能在 `pve-1` 上開始處理這個 issue")

	l.onStart = nil
	resp, err := s.Dispatch(context.Background(), Request{Action: ActionStart, DeliveryID: "run-1", Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.False(t, resp.Duplicate)
	s.waitStarts()
	assert.Equal(t, JobWorking, s.job("acme/widget#12").State)
}

// A job stopped while it starts is stopped on the launcher once the start
// answers, and a new start waits until then.
func TestStopWhileStarting(t *testing.T) {
	s, _ := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	l.gate = make(chan struct{})
	join(t, s, l, "claude")
	_, err := dispatchSoon(t, s, context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	stopped, err := dispatchSoon(t, s, context.Background(), Request{Action: ActionStop, Repo: "acme/widget", Issue: 12})
	require.NoError(t, err)
	assert.Equal(t, JobDone, stopped.Job.State)
	_, err = s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 12})
	assert.True(t, jsonapi.IsCode(err, "job_busy"))

	close(l.gate)
	s.waitStarts()
	assert.Equal(t, []string{"acme~widget~12?"}, l.stopped())
	assert.Equal(t, JobDone, s.job("acme/widget#12").State)
}

// A job a machine runs that no start of the hub asked for is stopped
// there, its worktree kept; one it is still starting, or that failed, is
// left.
func TestOrphanJobsStopped(t *testing.T) {
	s, _ := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	l.jobs["acme/widget#77"] = host.JobInfo{Key: "acme/widget#77", Agent: "claude", InstanceID: "pve-1~acme~widget~77", State: host.JobRunning}
	l.jobs["acme/widget#78"] = host.JobInfo{Key: "acme/widget#78", Agent: "claude", InstanceID: "pve-1~acme~widget~78", State: host.JobFailed}
	l.jobs["acme/widget#79"] = host.JobInfo{Key: "acme/widget#79", Agent: "claude", State: host.JobStarting}
	join(t, s, l, "claude")
	require.Eventually(t, func() bool { return len(l.stopped()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"acme~widget~77?keep_worktree=true&keep_state=true"}, l.stopped())
	assert.Nil(t, s.job("acme/widget#77"))
}

// Concurrent starts don't pick the same machine past its room: the pick
// holds the place.
func TestConcurrentStartsSpread(t *testing.T) {
	s, _ := newService(t, nil)
	gate := make(chan struct{})
	var launchers []*fakeLauncher
	for _, name := range []string{"a", "b"} {
		l := newFakeLauncher(t, name)
		l.gate, l.maxJobs = gate, 1
		join(t, s, l, "claude")
		launchers = append(launchers, l)
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 20 + i})
		}()
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("the starts didn't answer")
	}
	close(gate)
	s.waitStarts()
	refused := 0
	for _, err := range errs {
		if err != nil {
			assert.True(t, jsonapi.IsCode(err, "no_host"), err)
			refused++
		}
	}
	assert.Equal(t, 1, refused, "two machines with room for one each")
	assert.Equal(t, 1, launchers[0].startCount())
	assert.Equal(t, 1, launchers[1].startCount())
}

// A machine the hub picked that has no room after all has another take
// the job; one asked for doesn't.
func TestCapacityRefusalTriesAnother(t *testing.T) {
	s, _ := newService(t, nil)
	full := func(w http.ResponseWriter, _ host.JobInfo) {
		jsonapi.WriteError(w, jsonapi.Errorf(http.StatusConflict, "capacity", "this machine runs 2 jobs, its most"))
	}
	a := newFakeLauncher(t, "a")
	a.onStart = full
	b := newFakeLauncher(t, "b")
	join(t, s, a, "claude")
	join(t, s, b, "codex")

	job := startJob(t, s, Request{Repo: "acme/widget", Issue: 30}).Job
	assert.Equal(t, JobWorking, job.State)
	assert.Equal(t, "b", job.Host)
	assert.Equal(t, "codex", job.Agent, "b's default agent")
	assert.Equal(t, 1, a.startCount())
	assert.Equal(t, "codex", b.starts[0].Agent)

	job = startJob(t, s, Request{Repo: "acme/widget", Issue: 31, Host: "a"}).Job
	assert.Equal(t, JobFailed, job.State)
	assert.Equal(t, "a", job.Host)
	assert.Contains(t, job.Error, "its most")
}
