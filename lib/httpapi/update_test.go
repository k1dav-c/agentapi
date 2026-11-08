package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/selfupdate"
	"github.com/coder/agentapi/lib/selfupdate/selfupdatetest"
	"github.com/coder/agentapi/lib/statedb"
)

// updateOptions set up an updateServer: the versions that run, that the
// program on disk prints, and that the latest release has.
type updateOptions struct {
	off     bool
	running string
	onDisk  string
	latest  string
	// restartErr is what the restart in place returns.
	restartErr error
	stateDB    bool
	// noAgentRestarts: an agent that exits isn't started again.
	noAgentRestarts bool
}

// updateServer is a native server that updates itself from a fake
// release, into a fake program in a temporary directory; its restart in
// place is recorded.
type updateServer struct {
	*nativeServer
	release  *selfupdatetest.Release
	program  string
	restarts atomic.Int32
}

func newUpdateServer(t *testing.T, opts updateOptions) *updateServer {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake programs are shell scripts")
	}
	if opts.running == "" {
		opts.running = "2.1.0"
	}
	if opts.onDisk == "" {
		opts.onDisk = opts.running
	}
	if opts.latest == "" {
		opts.latest = "2.2.0"
	}
	u := &updateServer{
		release: selfupdatetest.NewRelease(t, opts.latest, selfupdatetest.Script(opts.latest)),
		program: selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "bin", "agentapi"), opts.onDisk),
	}
	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	factory := &nativetest.Factory{}
	config := httpapi.ServerConfig{
		AgentType:      instanceapi.AgentTypeClaude,
		Native:         httpapi.NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Update: httpapi.UpdateConfig{
			Allowed:  !opts.off,
			Program:  u.program,
			Releases: u.release.Source(),
			Version:  opts.running,
			Restart: func(context.Context) error {
				u.restarts.Add(1)
				return opts.restartErr
			},
		},
	}
	if opts.noAgentRestarts {
		config.Native.RestartDelays = []time.Duration{}
	}
	if opts.stateDB {
		path := filepath.Join(t.TempDir(), "state.db")
		db, err := statedb.Open(path, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		config.StateDB, config.UploadDir = db, path+".uploads"
	}
	srv, err := httpapi.NewServer(ctx, config)
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Stop(context.Background())
		cancel()
	})
	u.nativeServer = &nativeServer{t: t, url: ts.URL, factory: factory}
	require.Eventually(t, func() bool {
		var status convstate.StatusChangeBody
		u.do(http.MethodGet, "/status", nil, &status)
		return status.Capabilities != nil
	}, 5*time.Second, 5*time.Millisecond)
	return u
}

// update is the summary's update.
func (u *updateServer) update() instanceapi.UpdateStatus {
	u.t.Helper()
	var summary instanceapi.SummaryBody
	require.Equal(u.t, http.StatusOK, u.do(http.MethodGet, "/summary", nil, &summary))
	require.NotNil(u.t, summary.Update, "servers of this version always say")
	return *summary.Update
}

func (u *updateServer) awaitState(state instanceapi.UpdateState) instanceapi.UpdateStatus {
	u.t.Helper()
	var last instanceapi.UpdateStatus
	require.Eventually(u.t, func() bool {
		last = u.update()
		return last.State == state
	}, 10*time.Second, 5*time.Millisecond, "state %s", state)
	return last
}

// post sends POST /update with when, and returns the status code, the
// body and an error's detail.
func (u *updateServer) post(when httpapi.UpdateWhen) (int, httpapi.UpdateResponseBody, string) {
	u.t.Helper()
	body := "{}"
	if when != "" {
		body = `{"when":"` + string(when) + `"}`
	}
	return u.request(http.MethodPost, body)
}

func (u *updateServer) request(method, body string) (int, httpapi.UpdateResponseBody, string) {
	u.t.Helper()
	req, err := http.NewRequest(method, u.url+"/update", strings.NewReader(body))
	require.NoError(u.t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(u.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(u.t, err)
	var out httpapi.UpdateResponseBody
	var problem struct {
		Detail string `json:"detail"`
	}
	if resp.StatusCode < 300 {
		require.NoError(u.t, json.Unmarshal(data, &out))
	} else {
		require.NoError(u.t, json.Unmarshal(data, &problem))
	}
	return resp.StatusCode, out, problem.Detail
}

// busy has the agent work on a turn.
func (u *updateServer) busy() {
	u.t.Helper()
	code, _ := u.send("work", "")
	require.Equal(u.t, http.StatusOK, code)
	require.Eventually(u.t, func() bool {
		var status convstate.StatusChangeBody
		u.do(http.MethodGet, "/status", nil, &status)
		return status.Status == instanceapi.AgentStatusRunning
	}, 5*time.Second, 5*time.Millisecond)
}

// idle ends the agent's turn.
func (u *updateServer) idle() {
	u.factory.Last().Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
}

func (u *updateServer) onDisk() string {
	u.t.Helper()
	version, err := selfupdate.ProgramVersion(context.Background(), u.program)
	require.NoError(u.t, err)
	return version.String()
}

// Without --allow-update the API refuses, and names the setting.
func TestUpdate_Off(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{off: true})
	assert.Equal(t, instanceapi.UpdateStatus{State: instanceapi.UpdateStateIdle, KeepsSession: true}, u.update())
	code, _, detail := u.post("")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, detail, "--allow-update")
	assert.Contains(t, detail, "allow-update: true")
	code, _, _ = u.request(http.MethodDelete, "")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Zero(t, u.release.Lookups())
}

// A server that runs the latest release, the one on disk, is up to date.
func TestUpdate_UpToDate(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{running: "2.2.0", latest: "2.2.0"})
	assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle, KeepsSession: true}, u.update())
	code, body, _ := u.post("")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, httpapi.UpdateResponseBody{State: instanceapi.UpdateStateIdle, UpToDate: true}, body)
	assert.Equal(t, 1, u.release.Lookups())
	assert.Zero(t, u.release.Downloads())
	assert.Zero(t, u.restarts.Load())
	assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle, KeepsSession: true}, u.update())

	// No body is fine too.
	code, body, _ = u.request(http.MethodPost, "")
	require.Equal(t, http.StatusOK, code)
	assert.True(t, body.UpToDate)
}

// By default the update installs the release at once, and restarts once
// the agent is idle; messages sent meanwhile wait for the new program.
func TestUpdate_WaitsForTheAgent(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{stateDB: true})
	assert.True(t, u.update().KeepsConversation)
	assert.True(t, u.update().KeepsSession)
	u.busy()
	code, body, _ := u.post("")
	require.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, "2.2.0", body.Target)
	assert.Contains(t, []instanceapi.UpdateState{instanceapi.UpdateStateDownloading, instanceapi.UpdateStateWaiting}, body.State)

	status := u.awaitState(instanceapi.UpdateStateWaiting)
	assert.Equal(t, "2.2.0", status.Target)
	assert.Equal(t, "2.2.0", u.onDisk(), "installed while it waits")
	assert.Equal(t, 1, u.release.Downloads())
	code, queued := u.send("later", "")
	require.Equal(t, http.StatusOK, code)
	require.True(t, queued.Queued)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, u.restarts.Load(), "the agent works")

	u.idle()
	require.Eventually(t, func() bool { return u.restarts.Load() == 1 }, 10*time.Second, 5*time.Millisecond)
	assert.Equal(t, instanceapi.UpdateStateRestarting, u.update().State)
	// The queue holds its message for the new program.
	time.Sleep(700 * time.Millisecond)
	assert.Len(t, u.factory.Last().Turns(), 1)
	assert.Len(t, u.queue(), 1)
	code, queued = u.send("even later", "")
	require.Equal(t, http.StatusOK, code)
	assert.True(t, queued.Queued)

	code, _, detail := u.request(http.MethodDelete, "")
	assert.Equal(t, http.StatusConflict, code, "too late to cancel")
	assert.Contains(t, detail, "restarting into 2.2.0")
	code, body, _ = u.post(httpapi.UpdateWhenNow)
	assert.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, instanceapi.UpdateStateRestarting, body.State)
	assert.EqualValues(t, 1, u.restarts.Load())
	assert.Equal(t, http.StatusConflict, u.do(http.MethodDelete, "/messages", nil, nil), "nor does the agent restart")
}

// An agent that exited in the middle of its turn has no work left that a
// restart would cut short: the update doesn't wait for it.
func TestUpdate_DoesntWaitForAnAgentThatExited(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{noAgentRestarts: true})
	u.busy()
	code, _, _ := u.post("")
	require.Equal(t, http.StatusAccepted, code)
	u.awaitState(instanceapi.UpdateStateWaiting)
	assert.Zero(t, u.restarts.Load(), "the agent works")

	u.factory.Last().Exit(errors.New("exit status 1"))
	require.Eventually(t, func() bool { return u.restarts.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
}

// "now" restarts without waiting for the agent.
func TestUpdate_Now(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{})
	// Without a state database, Claude Code's session carries over.
	assert.False(t, u.update().KeepsConversation)
	assert.True(t, u.update().KeepsSession)
	u.busy()
	code, body, _ := u.post(httpapi.UpdateWhenNow)
	require.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, "2.2.0", body.Target)
	require.Eventually(t, func() bool { return u.restarts.Load() == 1 }, 10*time.Second, 5*time.Millisecond)
	assert.Equal(t, instanceapi.UpdateStateRestarting, u.update().State)
	assert.Equal(t, "2.2.0", u.onDisk())
}

// DELETE /update cancels an update that waits for the agent; a second
// POST while one is under way reports it, and "now" hurries it.
func TestUpdate_CancelAndRepeat(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{})
	u.busy()
	code, _, _ := u.post("")
	require.Equal(t, http.StatusAccepted, code)
	u.awaitState(instanceapi.UpdateStateWaiting)

	code, body, _ := u.post("")
	assert.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, httpapi.UpdateResponseBody{State: instanceapi.UpdateStateWaiting, Target: "2.2.0"}, body)
	assert.Equal(t, 1, u.release.Lookups(), "the same update")

	code, body, _ = u.request(http.MethodDelete, "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, httpapi.UpdateResponseBody{State: instanceapi.UpdateStateIdle}, body)
	assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle, KeepsSession: true}, u.update())
	u.idle()
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, u.restarts.Load(), "cancelled")
	assert.Equal(t, "2.2.0", u.onDisk(), "what it installed stays")

	// The program on disk is newer now: the next update only restarts.
	u.busy()
	code, body, _ = u.post("")
	require.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, httpapi.UpdateResponseBody{State: instanceapi.UpdateStateWaiting, Target: "2.2.0"}, body)
	code, body, _ = u.post(httpapi.UpdateWhenNow)
	assert.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, "2.2.0", body.Target)
	require.Eventually(t, func() bool { return u.restarts.Load() == 1 }, 10*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, u.release.Downloads())
}

// Another AgentAPI that shares the program installed the release: the
// update only restarts, at once while the agent is idle.
func TestUpdate_ProgramOnDiskIsNewer(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{onDisk: "2.2.0"})
	code, body, _ := u.post("")
	require.Equal(t, http.StatusAccepted, code)
	assert.Equal(t, "2.2.0", body.Target)
	require.Eventually(t, func() bool { return u.restarts.Load() == 1 }, 10*time.Second, 5*time.Millisecond)
	assert.Zero(t, u.release.Downloads())
	assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateRestarting, Target: "2.2.0", KeepsSession: true}, u.update())
}

// A summary client sees each state of the update.
func TestUpdate_SummaryFollowsTheUpdate(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{})
	u.busy()
	events := openEvents(t, u.url+"/events?only=summary")
	var states []instanceapi.UpdateState
	next := func() instanceapi.UpdateStatus {
		t.Helper()
		event := nextEvent(t, events)
		require.Equal(t, "summary_update", event.event)
		var body convstate.SummaryUpdateBody
		require.NoError(t, json.Unmarshal([]byte(event.data), &body))
		require.NotNil(t, body.Update)
		if len(states) == 0 || states[len(states)-1] != body.Update.State {
			states = append(states, body.Update.State)
		}
		return *body.Update
	}
	assert.Equal(t, instanceapi.UpdateStateIdle, next().State)
	code, _, _ := u.post("")
	require.Equal(t, http.StatusAccepted, code)
	for next().State != instanceapi.UpdateStateWaiting {
	}
	u.idle()
	for {
		update := next()
		if update.State == instanceapi.UpdateStateRestarting {
			assert.Equal(t, "2.2.0", update.Target)
			break
		}
	}
	assert.Equal(t, []instanceapi.UpdateState{
		instanceapi.UpdateStateIdle, instanceapi.UpdateStateChecking, instanceapi.UpdateStateDownloading,
		instanceapi.UpdateStateWaiting, instanceapi.UpdateStateRestarting,
	}, states)
}

// Failures leave the state failed with the reason, and the server as it
// was; DELETE forgets them.
func TestUpdate_Failures(t *testing.T) {
	t.Parallel()
	t.Run("checksum", func(t *testing.T) {
		t.Parallel()
		u := newUpdateServer(t, updateOptions{})
		u.release.Tamper()
		code, _, _ := u.post("")
		require.Equal(t, http.StatusAccepted, code)
		status := u.awaitState(instanceapi.UpdateStateFailed)
		assert.Contains(t, status.Error, "checksum mismatch")
		assert.Equal(t, "2.1.0", u.onDisk())
		assert.Zero(t, u.restarts.Load())

		code, body, _ := u.request(http.MethodDelete, "")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, instanceapi.UpdateStateIdle, body.State)
		assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle, KeepsSession: true}, u.update())
	})
	t.Run("lookup", func(t *testing.T) {
		t.Parallel()
		u := newUpdateServer(t, updateOptions{})
		u.release.Fail()
		code, _, detail := u.post("")
		require.Equal(t, http.StatusBadGateway, code)
		assert.Contains(t, detail, "rate limit")
		assert.Equal(t, instanceapi.UpdateStateFailed, u.update().State)
	})
	t.Run("restart", func(t *testing.T) {
		t.Parallel()
		u := newUpdateServer(t, updateOptions{restartErr: errors.New("agentapi --version: exit status 1")})
		u.busy()
		code, _ := u.send("queued", "")
		require.Equal(t, http.StatusOK, code)
		code, _, _ = u.post("")
		require.Equal(t, http.StatusAccepted, code)
		u.awaitState(instanceapi.UpdateStateWaiting)
		u.idle()
		status := u.awaitState(instanceapi.UpdateStateFailed)
		assert.Contains(t, status.Error, "restart into 2.2.0: agentapi --version: exit status 1")
		assert.Equal(t, "2.2.0", status.Target)
		// The server goes on, and so does its queue.
		require.Eventually(t, func() bool { return len(u.factory.Last().Turns()) == 2 }, 5*time.Second, 5*time.Millisecond)
	})
}

// A server that can't write the program's directory says so, with the fix.
func TestUpdate_Blocked(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	t.Parallel()
	u := newUpdateServer(t, updateOptions{})
	dir := filepath.Dir(u.program)
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	code, _, detail := u.post("")
	require.Equal(t, http.StatusConflict, code)
	assert.Contains(t, detail, "isn't writable")
	assert.Contains(t, detail, "ln -sf")
	assert.Contains(t, detail, u.program)
	assert.Equal(t, detail, u.update().Blocked)
	assert.Zero(t, u.release.Lookups())

	// Fixed: the next request goes ahead.
	require.NoError(t, os.Chmod(dir, 0o755))
	code, _, _ = u.post("")
	assert.Equal(t, http.StatusAccepted, code)
	assert.Empty(t, u.update().Blocked)
}

// A page of another site can't have the server update itself.
func TestUpdate_RefusesCrossOrigin(t *testing.T) {
	t.Parallel()
	u := newUpdateServer(t, updateOptions{})
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		req, err := http.NewRequest(method, u.url+"/update", strings.NewReader(`{"when":"now"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, method)
	}
	assert.Zero(t, u.release.Lookups())
	assert.Equal(t, instanceapi.UpdateStateIdle, u.update().State)
}
