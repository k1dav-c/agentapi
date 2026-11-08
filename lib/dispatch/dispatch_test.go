package dispatch

import (
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
		defer f.mu.Unlock()
		f.starts = append(f.starts, req)
		key := host.Key{Issue: req.Issue}
		key.Owner, key.Name, _ = host.ParseRepo(req.Repo)
		job := host.JobInfo{Key: key.String(), Agent: req.Agent, InstanceID: f.name + "~" + key.Slug(), DefaultBranch: "main", State: host.JobRunning}
		f.jobs[key.String()] = job
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
			DefaultAgent: agents[0], MaxJobs: 2, Clone: true, Jobs: jobs,
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
		jsonapi.Write(w, 201, map[string]any{"id": 42, "slug": "agentapi-hub-x", "client_id": "Iv1", "client_secret": "cs", "pem": g.pem(), "webhook_secret": "ws", "owner": map[string]string{"login": "acme"}})
	case r.Method == "GET" && path == "/users/agentapi-hub-x[bot]":
		jsonapi.Write(w, 200, map[string]any{"id": 99})
	case r.Method == "GET" && path == "/app/installations/7" && g.checkJWT(r):
		jsonapi.Write(w, 200, map[string]any{"id": 7, "account": map[string]string{"login": "acme"}, "repository_selection": "selected"})
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
	assert.True(t, isCode(err, "job_ended"))

	// The dispatch keeps it all.
	s.Flush()
	again, err := New(s.config)
	require.NoError(t, err)
	defer again.Close()
	assert.Equal(t, JobDone, again.job("acme/widget#12").State)
	assert.Len(t, again.Hosts(), 1)
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
	assert.True(t, isCode(err, "no_host"))
	_, err = s.pickHost(key, "codex", "a")
	assert.True(t, isCode(err, "agent_unavailable"))

	// A job the hub started counts before the machine's next heartbeat
	// says so.
	_, err = s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 9, Agent: "codex"})
	require.NoError(t, err)
	_, err = s.pickHost(key, "codex", "")
	assert.True(t, isCode(err, "no_host"), "b is full")
	assert.Equal(t, 1, s.Hosts()[1].Active)

	*now = now.Add(4 * time.Minute)
	_, err = s.pickHost(key, "", "a")
	assert.True(t, isCode(err, "host_offline"), "missed three heartbeats")
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
	assert.Equal(t, []github.Installation{{ID: 7, Account: "acme", RepositorySelection: "selected"}}, app.Installations)

	// A repository the app isn't on is refused.
	_, err = s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/other", Issue: 1})
	assert.True(t, isCode(err, "github_not_installed"))

	started, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Agent: "claude"})
	require.NoError(t, err)
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
	assert.True(t, isCode(err, "job_active"))
	assert.Empty(t, replaced.Job.Key)
	replaced, err = s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Agent: "codex"})
	require.NoError(t, err)
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
	_, err = s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Agent: "claude"})
	require.NoError(t, err)
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
	ctx := context.Background()
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	_, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 5})
	require.NoError(t, err)
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
	again, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 5})
	require.NoError(t, err)
	require.Len(t, again.Job.Runs, 2)
	assert.Equal(t, "failed", again.Job.Runs[0].EndReason)
	s.TaskEvent(again.Job.InstanceID, "task.completed", json.RawMessage(`{"cost":{"usd":0.4}}`))
	assert.InDelta(t, 0.4, s.job("acme/widget#5").Runs[1].Cost.USD, 1e-9)
}

// Another machine that has the hub's secret can't take over a machine's
// name while it is online.
func TestHostTakeOverRefused(t *testing.T) {
	s, now := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude")
	forged := l.event("claude")
	forged.Token, forged.URL = "attacker", "http://attacker:3291"
	data, _ := json.Marshal(forged)
	assert.Error(t, s.HostEvent(host.EventHostHeartbeat, data))
	assert.Equal(t, l.srv.URL, s.Hosts()[0].URL)
	// A machine offline for long can come back with a new launcher.
	*now = now.Add(10 * time.Minute)
	assert.NoError(t, s.HostEvent(host.EventHostHeartbeat, data))
}

// A switch that fails after the old agent stopped leaves the job failed,
// not working with no agent.
func TestSwitchAgentFailure(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, nil)
	l := newFakeLauncher(t, "pve-1")
	join(t, s, l, "claude", "codex")
	_, err := s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 7, Agent: "claude"})
	require.NoError(t, err)
	l.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			jsonapi.WriteError(w, jsonapi.Errorf(http.StatusInternalServerError, "start_failed", "codex didn't start"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	_, err = s.Dispatch(ctx, Request{Action: ActionStart, Repo: "acme/widget", Issue: 7, Agent: "codex"})
	require.Error(t, err)
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
