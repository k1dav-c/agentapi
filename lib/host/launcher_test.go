package host

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test binary stands in for agentapi server when envFake is set: it
// serves /status and /message on --port behind AGENTAPI_API_TOKEN, writes
// --pid-file and --state-db, and records its arguments, environment and
// messages in envRecord. The names don't start with AGENTAPI_, which the
// launcher keeps from the jobs.
const (
	envFake   = "HOSTTEST_FAKE_AGENTAPI"
	envRecord = "HOSTTEST_RECORD"
)

func TestMain(m *testing.M) {
	if os.Getenv(envFake) == "1" && len(os.Args) > 2 && os.Args[1] == "host" {
		os.Exit(fakeHostCommand(os.Args[2:]))
	}
	if os.Getenv(envFake) == "1" {
		fakeServer()
		return
	}
	os.Exit(m.Run())
}

// fakeHostCommand is agentapi host git-credential and gh-token, as git and
// the gh shim run them: lib/host's, with the --socket the launcher gives.
func fakeHostCommand(args []string) int {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	socket := fs.String("socket", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	key, err := ParseKey(os.Getenv(EnvJob))
	if err != nil {
		return 1
	}
	switch args[0] {
	case "git-credential":
		if err := GitCredential(context.Background(), fs.Arg(0), os.Stdin, os.Stdout, *socket, key); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	case "gh-token":
		token, err := RequestToken(context.Background(), *socket, key)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(token.Value)
	}
	return 0
}

// gitCredentialFill is what git's credential helpers give for url, with
// env.
func gitCredentialFill(dir string, env []string, url string) (string, error) {
	cmd := exec.Command("git", "credential", "fill")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader("url=" + url + "\n\n")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fakeRecord is what the fake server writes when it starts.
type fakeRecord struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
}

func fakeServer() {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	port := fs.Int("port", 0, "")
	pidFile := fs.String("pid-file", "", "")
	stateDB := fs.String("state-db", "", "")
	fs.String("type", "", "")
	fs.String("name", "", "")
	fs.String("instance-id", "", "")
	fs.String("initial-prompt", "", "")
	fs.String("config", "", "")
	if len(os.Args) < 2 || os.Args[1] != "server" {
		os.Exit(2)
	}
	_ = fs.Parse(os.Args[2:])
	record := os.Getenv(envRecord)
	dir, _ := os.Getwd()
	data, _ := json.Marshal(fakeRecord{Args: os.Args[1:], Env: os.Environ(), Dir: dir})
	_ = os.WriteFile(filepath.Join(record, "start-"+strconv.Itoa(os.Getpid())+".json"), data, 0o600)
	if os.Getenv("HOSTTEST_EXIT") == "1" {
		fmt.Fprintln(os.Stderr, "fake: refusing to serve")
		os.Exit(3)
	}
	_ = os.WriteFile(*pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
	_ = os.WriteFile(*stateDB, nil, 0o600)
	token := os.Getenv("AGENTAPI_API_TOKEN")
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /status", auth(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"lifecycle":"ready"}`)
	}))
	// What git in the job gets: run below the job's AgentAPI, as the
	// agent's git is.
	mux.HandleFunc("GET /git-credential", auth(func(w http.ResponseWriter, r *http.Request) {
		out, err := gitCredentialFill(dir, os.Environ(), r.URL.Query().Get("url"))
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
		}
		_, _ = io.WriteString(w, out)
	}))
	mux.HandleFunc("POST /message", auth(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f, _ := os.OpenFile(filepath.Join(record, "messages"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		_, _ = f.Write(append(body, '\n'))
		_ = f.Close()
		_, _ = io.WriteString(w, `{"ok":true,"queued":false}`)
	}))
	_ = http.ListenAndServe("127.0.0.1:"+strconv.Itoa(*port), mux)
}

// testEnv is a machine with a GitHub stand-in: a bare repository for each
// of acme/widget and acme/other that https://github.com/... and
// git@github.com:... resolve to, and a clone of acme/widget in the
// workdir.
type testEnv struct {
	t        *testing.T
	root     string
	record   string
	config   Config
	launcher *Launcher
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	if !Supported {
		t.Skip("agentapi host runs on Linux and macOS")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	// A short directory: a unix socket's path is at most about 100 bytes.
	root, err := os.MkdirTemp("/tmp", "ah")
	require.NoError(t, err)
	// macOS's /tmp is a link to /private/tmp, which a process's working
	// directory names.
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	origin := filepath.Join(root, "origin")
	gitconfig := filepath.Join(root, "gitconfig")
	require.NoError(t, os.WriteFile(gitconfig, []byte(fmt.Sprintf(
		"[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n"+
			"[url \"file://%[1]s/\"]\n\tinsteadOf = https://github.com/\n\tinsteadOf = git@github.com:\n[protocol \"file\"]\n\tallow = always\n",
		origin)), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, repo := range []string{"acme/widget", "acme/other"} {
		bare := filepath.Join(origin, repo+".git")
		require.NoError(t, os.MkdirAll(bare, 0o755))
		runGit(t, bare, "init", "--quiet", "--bare")
		seed := filepath.Join(root, "seed", repo)
		require.NoError(t, os.MkdirAll(seed, 0o755))
		runGit(t, seed, "init", "--quiet")
		require.NoError(t, os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+repo+"\n"), 0o644))
		runGit(t, seed, "add", ".")
		runGit(t, seed, "commit", "--quiet", "-m", "first")
		runGit(t, seed, "push", "--quiet", bare, "main")
		runGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	}
	workdir := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "acme"), 0o755))
	runGit(t, filepath.Join(workdir, "acme"), "clone", "--quiet", "https://github.com/acme/widget.git", "widget")
	runGit(t, filepath.Join(workdir, "acme", "widget"), "remote", "set-url", "origin", "git@github.com:acme/widget.git")

	record := filepath.Join(root, "record")
	require.NoError(t, os.MkdirAll(record, 0o700))
	t.Setenv(envFake, "1")
	t.Setenv(envRecord, record)
	// A variable an AgentAPI the launcher runs in sets: the jobs mustn't
	// inherit it.
	t.Setenv("AGENTAPI_PID_FILE", filepath.Join(root, "someone-elses.pid"))

	config, err := Config{
		Dir:      filepath.Join(root, "agentapi"),
		Workdir:  workdir,
		Ports:    "47300-47399",
		Agents:   []string{"native-demo", "claude"},
		AgentAPI: os.Args[0],
	}.withDefaults(root)
	require.NoError(t, err)
	env := &testEnv{t: t, root: root, record: record, config: config}
	env.launcher = env.newLauncher()
	// Every fake server the test started, whichever launcher started it.
	t.Cleanup(func() {
		starts, _ := filepath.Glob(filepath.Join(record, "start-*.json"))
		for _, start := range starts {
			pid, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(start), "start-"), ".json"))
			// Its arguments: "server", and a path under root.
			if err == nil && alive(pid, "server") && strings.Contains(cmdlineOf(pid), root) {
				kill(pid)
			}
		}
	})
	return env
}

func (e *testEnv) newLauncher() *Launcher {
	e.t.Helper()
	l, err := New(e.config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(e.t, err)
	l.rescan(context.Background())
	return l
}

// started is what the fake server of pid recorded.
func (e *testEnv) started(pid int) fakeRecord {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.record, "start-"+strconv.Itoa(pid)+".json"))
	require.NoError(e.t, err)
	var r fakeRecord
	require.NoError(e.t, json.Unmarshal(data, &r))
	return r
}

// cmdlineOf is pid's command line, as ps tells it.
func cmdlineOf(pid int) string {
	out, _ := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return string(out)
}

func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestLauncherStartMessageStop(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	l := e.launcher

	job, created, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 7, Prompt: "處理 issue #7", GitUser: &GitUser{Name: "bot", Email: "bot@example.com"}})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, JobRunning, job.State)
	assert.Equal(t, "native-demo", job.Agent)
	assert.Equal(t, 1, job.Run)
	assert.Equal(t, "main", job.DefaultBranch)
	wantWorktree := filepath.Join(e.config.Workdir, "acme", "widget.worktrees", "7")
	assert.Equal(t, wantWorktree, job.Worktree)
	assert.Equal(t, "agent/issue-7", runGit(t, wantWorktree, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.True(t, job.Port >= 47300 && job.Port <= 47399, "port %d in the range", job.Port)

	r := e.started(job.PID)
	assert.Equal(t, wantWorktree, r.Dir)
	assert.Contains(t, r.Args, "--initial-prompt")
	assert.Contains(t, r.Args, "處理 issue #7")
	assert.Contains(t, r.Args, "--instance-id")
	_, leaked := envValue(r.Env, "AGENTAPI_PID_FILE")
	assert.False(t, leaked, "the job inherited AGENTAPI_PID_FILE")
	token, _ := envValue(r.Env, "AGENTAPI_API_TOKEN")
	assert.Equal(t, l.instanceToken, token)
	jobEnv, _ := envValue(r.Env, EnvJob)
	assert.Equal(t, "acme~widget~7", jobEnv)
	author, _ := envValue(r.Env, "GIT_AUTHOR_NAME")
	assert.Equal(t, "bot", author)
	_, helper := envValue(r.Env, "GIT_CONFIG_COUNT")
	assert.True(t, helper, "git config for the job")
	_, rewritten := envValue(r.Env, "GIT_CONFIG_KEY_2")
	assert.False(t, rewritten, "without credentials github.com isn't moved to HTTPS")

	// The same job again is the one that runs.
	again, created, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 7})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, job.PID, again.PID)

	// Another agent waits for the job to stop.
	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 7, Agent: "claude"})
	var e409 *Error
	require.ErrorAs(t, err, &e409)
	assert.Equal(t, "job_active", e409.Code)

	status, body, err := l.Message(ctx, mustKey(t, "acme/widget#7"), MessageRequest{Content: "PR review：改名", DispatchMode: "steer"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"ok":true,"queued":false}`, string(body))
	messages, err := os.ReadFile(filepath.Join(e.record, "messages"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"content":"PR review：改名","type":"user","dispatch_mode":"steer"}`, strings.TrimSpace(string(messages)))

	require.NoError(t, l.Stop(ctx, mustKey(t, "acme/widget#7"), StopOptions{}))
	assert.False(t, alive(job.PID, job.InstanceID))
	assert.NoDirExists(t, wantWorktree)
	assert.NoDirExists(t, e.config.jobFiles(mustKey(t, "acme/widget#7")).dir)
	assert.Empty(t, runGit(t, filepath.Join(e.config.Workdir, "acme", "widget"), "branch", "--list", "agent/issue-7"))
	assert.Empty(t, l.Jobs())

	err = l.Stop(ctx, mustKey(t, "acme/widget#7"), StopOptions{})
	require.ErrorAs(t, err, &e409)
	assert.Equal(t, http.StatusNotFound, e409.Status)
}

func mustKey(t *testing.T, s string) Key {
	t.Helper()
	k, err := ParseKey(s)
	require.NoError(t, err)
	return k
}

func TestLauncherSwitchAgentKeepsWorktree(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	l := e.launcher
	key := mustKey(t, "acme/widget#8")

	job, _, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 8})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(job.Worktree, "work.txt"), []byte("in progress"), 0o644))
	require.NoError(t, l.Stop(ctx, key, StopOptions{KeepWorktree: true, KeepState: true}))
	assert.FileExists(t, filepath.Join(job.Worktree, "work.txt"))

	// The next agent works in the same worktree, with a state database of
	// its own.
	l.config.AgentCommands["claude"] = []string{"native-demo"}
	next, created, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 8, Agent: "claude"})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, job.Worktree, next.Worktree)
	assert.Equal(t, 2, next.Run)
	assert.FileExists(t, filepath.Join(next.Worktree, "work.txt"))
	assert.Contains(t, e.started(next.PID).Args, filepath.Join(e.config.jobFiles(key).dir, "state-2.db"))
}

// fakeClock is a launcher's l.now that a test moves.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func useClock(l *Launcher) *fakeClock {
	c := &fakeClock{now: time.Now()}
	l.now = c.Now
	return c
}

// exitAndRestart has the job's AgentAPI exit, and the checks start it
// again once its delay passed.
func exitAndRestart(t *testing.T, l *Launcher, clock *fakeClock, delay time.Duration) {
	t.Helper()
	ctx := context.Background()
	before := l.Jobs()[0]
	kill(before.PID)
	waitFor(t, func() bool { return !alive(before.PID, before.InstanceID) })
	l.check(ctx)
	waiting := l.Jobs()[0]
	assert.Equal(t, JobRestarting, waiting.State)
	assert.Equal(t, before.PID, waiting.PID, "not started again before its delay")
	clock.Advance(delay - time.Second)
	l.check(ctx)
	assert.Equal(t, before.PID, l.Jobs()[0].PID, "not started again before its delay")
	clock.Advance(time.Second)
	l.check(ctx)
	require.NotEqual(t, before.PID, l.Jobs()[0].PID, "started again after %s", delay)
	waitFor(t, func() bool {
		l.check(ctx)
		return l.Jobs()[0].State == JobRunning
	})
}

func TestLauncherRestartsAndTakesOver(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	l := e.launcher
	clock := useClock(l)
	job, _, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 9})
	require.NoError(t, err)

	// Its AgentAPI exits: a check starts it again.
	exitAndRestart(t, l, clock, restartDelays[0])
	restarted := l.Jobs()[0]
	assert.NotEqual(t, job.PID, restarted.PID)

	// Another launcher (after a restart or an update) takes it over
	// without starting it again.
	l2 := e.newLauncher()
	l2.takeOver()
	require.Len(t, l2.Jobs(), 1)
	assert.Equal(t, restarted.PID, l2.Jobs()[0].PID)
	waitFor(t, func() bool {
		l2.check(ctx)
		return l2.Jobs()[0].State == JobRunning
	})
	assert.Equal(t, restarted.PID, l2.Jobs()[0].PID)

	// An AgentAPI that keeps exiting leaves the job failed: the restart
	// before the launcher's counts on.
	clock2 := useClock(l2)
	clock2.now = clock.now
	for _, delay := range restartDelays[1:] {
		exitAndRestart(t, l2, clock2, delay)
	}
	pid := l2.Jobs()[0].PID
	kill(pid)
	waitFor(t, func() bool { return !alive(pid, job.InstanceID) })
	l2.check(ctx)
	assert.Equal(t, JobFailed, l2.Jobs()[0].State)
	assert.Contains(t, l2.Jobs()[0].Error, "exited 6 times")

	// Starting it again tries anew.
	again, created, err := l2.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 9})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, JobRunning, again.State)
}

func TestLauncherClonesAndRefuses(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	l := e.launcher

	job, _, err := l.Start(ctx, StartRequest{Repo: "acme/other", Issue: 1})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(e.config.Workdir, "acme", "other.worktrees", "1"), job.Worktree)
	matches, _ := filepath.Glob(filepath.Join(e.config.Workdir, "acme", ".other.clone-*"))
	assert.Empty(t, matches, "no clone directory left behind")
	assert.Contains(t, l.Info(ctx).Repos, "acme/other")

	var refused *Error
	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/missing", Issue: 1})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "clone_failed", refused.Code)

	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 2, Agent: "pi"})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "agent_unavailable", refused.Code)

	l.config.MaxJobs = 1
	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 3})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "capacity", refused.Code)

	no := false
	l.config.MaxJobs = 4
	l.config.Clone = &no
	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/nothing", Issue: 1})
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "repo_missing", refused.Code)
}

func TestLauncherCredentials(t *testing.T) {
	e := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := e.launcher
	require.NoError(t, os.WriteFile(e.config.GitHubTokenFile, []byte("ghs_test\n"), 0o600))
	go func() { _ = l.ServeSocket(ctx) }()
	waitFor(t, func() bool { _, err := os.Stat(e.config.SocketPath()); return err == nil })
	key := mustKey(t, "acme/widget#5")

	// While a job is being started, the launcher's own git (its clone and
	// fetch) gets its token.
	_, release, err := l.reserve(key, credentialsFile, "native-demo")
	require.NoError(t, err)
	out, err := gitCredentialFill(e.root, l.gitEnv(key, nil, credentialsFile), "https://github.com/acme/widget.git")
	require.NoError(t, err, out)
	assert.Contains(t, out, "password=ghs_test")
	release()
	out, err = gitCredentialFill(e.root, l.gitEnv(key, nil, credentialsFile), "https://github.com/acme/widget.git")
	assert.Error(t, err, "no job, no token: %s", out)

	job, _, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 5})
	require.NoError(t, err)
	r := e.started(job.PID)
	helper, _ := envValue(r.Env, "GIT_CONFIG_VALUE_1")
	assert.Contains(t, helper, "host git-credential --socket")
	useHTTPPath, _ := envValue(r.Env, "GIT_CONFIG_KEY_2")
	assert.Equal(t, "credential.https://github.com.useHttpPath", useHTTPPath)
	path, _ := envValue(r.Env, "PATH")
	if _, err := exec.LookPath("gh"); err == nil {
		assert.True(t, strings.HasPrefix(path, e.config.jobFiles(key).bin()), "gh's shim first on PATH")
	}

	// git in the job gets its repository's token, and no other's.
	ask := func(url string) (int, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(job.Port)+"/git-credential?url="+url, nil)
		req.Header.Set("Authorization", "Bearer "+l.instanceToken)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	status, body := ask("https://github.com/acme/widget.git")
	assert.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "username=x-access-token\npassword=ghs_test")
	status, body = ask("https://github.com/acme/other.git")
	assert.NotContains(t, body, "ghs_test", "another repository")
	_ = status

	// A process outside the job (this test) is refused, where the
	// platform tells who asks.
	_, err = RequestToken(ctx, e.config.SocketPath(), key)
	if runtime.GOOS == "linux" {
		assert.ErrorContains(t, err, "doesn't run in job")
	}
	_, err = RequestToken(ctx, e.config.SocketPath(), mustKey(t, "acme/widget#6"))
	assert.ErrorContains(t, err, "no job")

	var buf bytes.Buffer
	require.NoError(t, GitCredential(ctx, "get", strings.NewReader("protocol=https\nhost=gitlab.com\n\n"), &buf, e.config.SocketPath(), key))
	assert.Empty(t, buf.String())
	require.NoError(t, GitCredential(ctx, "store", strings.NewReader("protocol=https\nhost=github.com\n\n"), &buf, e.config.SocketPath(), key))
	assert.Empty(t, buf.String())
}

// A failed job's AgentAPI that still runs is stopped before the job
// starts again, and another agent starts a run of its own.
func TestLauncherRestartsAFailedJob(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	l := e.launcher
	job, _, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 21})
	require.NoError(t, err)
	l.mu.Lock()
	l.store.get(job.Key).State = JobFailed
	l.mu.Unlock()

	again, created, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 21, Agent: "claude"})
	require.NoError(t, err)
	assert.False(t, created)
	assert.False(t, alive(job.PID, job.InstanceID), "the old AgentAPI is stopped")
	assert.NotEqual(t, job.PID, again.PID)
	assert.Equal(t, "claude", again.Agent)
	assert.Equal(t, 2, again.Run)
	assert.Equal(t, JobRunning, again.State)

	// A failed job counts toward max-jobs once it runs again.
	l.config.MaxJobs = 1
	l.mu.Lock()
	l.store.get(job.Key).State = JobFailed
	l.mu.Unlock()
	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 22})
	require.NoError(t, err)
	_, _, err = l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 21, Agent: "claude"})
	var refused *Error
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "capacity", refused.Code)
}

// An AgentAPI that exits at once fails the start at once, with its log.
func TestLauncherStartFailsFast(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("HOSTTEST_EXIT", "1")
	began := time.Now()
	_, _, err := e.launcher.Start(context.Background(), StartRequest{Repo: "acme/widget", Issue: 23})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to serve")
	assert.Less(t, time.Since(began), 10*time.Second)
	assert.Equal(t, JobFailed, e.launcher.Jobs()[0].State)
}

func TestRunsMatchesTheWholeArgument(t *testing.T) {
	if !Supported {
		t.Skip("agentapi host runs on Linux and macOS")
	}
	cmd := exec.Command("sleep", "30")
	cmd.Args = []string{"sleep", "30"}
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	// Right after the fork, the child still has this program's command
	// line.
	waitFor(t, func() bool { return runs(cmd.Process.Pid, "30") })
	assert.False(t, runs(cmd.Process.Pid, "3"), "a prefix of an argument")
	if runtime.GOOS == "linux" {
		// Elsewhere the caller isn't checked: descendsFrom says yes.
		assert.True(t, descendsFrom(cmd.Process.Pid, os.Getpid()))
		assert.False(t, descendsFrom(os.Getpid(), cmd.Process.Pid))
	}
}

func TestKeyLocks(t *testing.T) {
	var k keyLocks
	unlock := k.lock("a")
	_, ok := k.tryLock("a")
	assert.False(t, ok)
	other, ok := k.tryLock("b")
	require.True(t, ok)
	other()
	unlock()
	again, ok := k.tryLock("a")
	require.True(t, ok)
	again()
	assert.Empty(t, k.locks, "unused locks go")
}

func TestLauncherHTTP(t *testing.T) {
	e := newTestEnv(t)
	srv := httptest.NewServer(e.launcher.Handler())
	defer srv.Close()
	do := func(method, path, token, body string) (int, string) {
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	token := e.launcher.HostToken()
	status, _ := do("GET", "/host", "", "")
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = do("GET", "/host", "wrong", "")
	assert.Equal(t, http.StatusUnauthorized, status)

	status, body := do("GET", "/host", token, "")
	require.Equal(t, http.StatusOK, status)
	var info HostInfo
	require.NoError(t, json.Unmarshal([]byte(body), &info))
	assert.Equal(t, []string{"acme/widget"}, info.Repos)
	assert.Equal(t, []string{"native-demo", "claude"}, info.Agents)

	status, body = do("POST", "/jobs", token, `{"repo":"acme/widget","issue":4}`)
	require.Equal(t, http.StatusCreated, status, body)
	status, _ = do("POST", "/jobs", token, `{"repo":"acme/widget","issue":4}`)
	assert.Equal(t, http.StatusOK, status)
	status, body = do("POST", "/jobs/acme~widget~4/message", token, `{"content":"hi"}`)
	assert.Equal(t, http.StatusOK, status, body)
	status, body = do("POST", "/jobs", token, `{"repo":"nope","issue":4}`)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body, `"code":"bad_request"`)
	status, _ = do("DELETE", "/jobs/acme~widget~4", token, "")
	assert.Equal(t, http.StatusNoContent, status)
	status, _ = do("DELETE", "/jobs/acme~widget~4", token, "")
	assert.Equal(t, http.StatusNotFound, status)
}

// A start goes on when the request that asked for it ends (the hub's call
// timed out, or the hub went away): the hub adopts the job from the
// launcher's next report.
func TestLauncherStartOutlivesTheRequest(t *testing.T) {
	e := newTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	job, created, err := e.launcher.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 24})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, JobRunning, job.State)
	assert.True(t, alive(job.PID, job.InstanceID))
}

// A job being prepared is reported as starting, without an AgentAPI.
func TestLauncherReportsJobsBeingPrepared(t *testing.T) {
	e := newTestEnv(t)
	l := e.launcher
	_, release, err := l.reserve(mustKey(t, "acme/widget#25"), "", "claude")
	require.NoError(t, err)
	assert.Equal(t, []JobInfo{{Key: "acme/widget#25", Agent: "claude", State: JobStarting}}, l.Jobs())
	release()
	assert.Empty(t, l.Jobs())
}

// A job's AgentAPI that exits starts again after a longer wait each time,
// kept across a restart of the launcher; the exits of long ago don't
// count.
func TestLauncherRestartBackoff(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	l := e.launcher
	clock := useClock(l)
	_, _, err := l.Start(ctx, StartRequest{Repo: "acme/widget", Issue: 26})
	require.NoError(t, err)
	for _, delay := range restartDelays[:3] {
		exitAndRestart(t, l, clock, delay)
	}

	// Long after, it exits again: the first delay again.
	clock.Advance(restartWindow + time.Minute)
	exitAndRestart(t, l, clock, restartDelays[0])

	// It exits while the launcher is down: the new one waits the delay of
	// the restarts kept.
	pid := l.Jobs()[0].PID
	kill(pid)
	waitFor(t, func() bool { return !alive(pid, "server") })
	l.check(ctx)
	l2 := e.newLauncher()
	clock2 := useClock(l2)
	clock2.now = clock.now
	l2.takeOver()
	job := l2.Jobs()[0]
	assert.Equal(t, JobRestarting, job.State)
	assert.Equal(t, pid, job.PID, "not started again before its delay")
	clock2.Advance(restartDelays[1])
	l2.check(ctx)
	assert.NotEqual(t, pid, l2.Jobs()[0].PID)
	// Serving, it recorded itself, for the cleanup to stop it.
	waitFor(t, func() bool {
		l2.check(ctx)
		return l2.Jobs()[0].State == JobRunning
	})
	l2.mu.Lock()
	assert.Len(t, l2.store.get("acme/widget#26").Restarts, 2)
	l2.mu.Unlock()
}
