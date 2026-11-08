package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/selfupdate"
)

// The update's types are part of the summary's contract (lib/instanceapi);
// these names keep the OpenAPI schema's.
type (
	UpdateState  = instanceapi.UpdateState
	UpdateStatus = instanceapi.UpdateStatus
)

const (
	UpdateStateIdle        = instanceapi.UpdateStateIdle
	UpdateStateChecking    = instanceapi.UpdateStateChecking
	UpdateStateDownloading = instanceapi.UpdateStateDownloading
	UpdateStateWaiting     = instanceapi.UpdateStateWaiting
	UpdateStateRestarting  = instanceapi.UpdateStateRestarting
	UpdateStateFailed      = instanceapi.UpdateStateFailed
)

const (
	// updateCheckTimeout bounds the lookup of the latest release and the
	// run of the program on disk.
	updateCheckTimeout = time.Minute
	// updateInstallTimeout bounds the download and the replacement,
	// another AgentAPI's included, whose lock it waits for.
	updateInstallTimeout = 10 * time.Minute
	// updateBlockedInterval is how often the server checks again whether
	// it can replace its program, which the user may have fixed.
	updateBlockedInterval = time.Minute
)

// errUpdatesOff answers an update request to a server without
// --allow-update.
var errUpdatesOff = errors.New("updates through the API are off: start AgentAPI with --allow-update, or add allow-update: true to ~/.agentapi/config.yaml")

// UpdateConfig is how the server updates itself through its API.
type UpdateConfig struct {
	// Allowed turns updates through the API on (--allow-update).
	Allowed bool
	// Program is the program the server restarts into, as it was started;
	// an update replaces the file it names, symlinks followed. Empty when
	// it wasn't found.
	Program string
	// Restart restarts the server in place into Program: once that runs,
	// the server stops as for a shutdown and runs it again. It returns why
	// it can't, and the server goes on serving. Nil where the server
	// doesn't restart in place (Windows).
	Restart func(ctx context.Context) error
	// Releases is where the latest release comes from: the zero value is
	// k1dav-c/agentapi on GitHub, and tests point it at a fake.
	Releases selfupdate.Source
	// Version is the version that runs; empty is this build's.
	Version string
}

// UpdateWhen is when an update restarts AgentAPI.
type UpdateWhen string

const (
	UpdateWhenIdle UpdateWhen = "idle"
	UpdateWhenNow  UpdateWhen = "now"
)

// UpdateRequestBody is the body of POST /update.
type UpdateRequestBody struct {
	When UpdateWhen `json:"when,omitempty" enum:"idle,now" doc:"When AgentAPI restarts into the new version. idle (the default): once the agent is idle, that is no turn runs and no request (approval, question, plan) is open; queued messages stay queued and go out after the restart. now: at once, interrupting the agent."`
}

// UpdateRequest asks to update AgentAPI; the body is optional.
type UpdateRequest struct {
	Body *UpdateRequestBody
}

// UpdateResponseBody is where an update is.
type UpdateResponseBody struct {
	State    UpdateState `json:"state" doc:"Where the update is, as the summary's update.state."`
	Target   string      `json:"target,omitempty" doc:"The version it updates to, once known."`
	UpToDate bool        `json:"up_to_date,omitempty" doc:"AgentAPI runs the latest release (or a newer version) already, and the program on disk is no newer: there is nothing to update."`
}

// UpdateResponse is the answer of POST and DELETE /update.
type UpdateResponse struct {
	Status int
	Body   UpdateResponseBody
}

// updater updates the server to the latest release through its API: it
// looks the release up, installs it in place of the program unless the
// program on disk is that new already, waits for the agent to be idle
// (unless asked to restart now), and restarts the server in place. Every
// step changes the summary's update.
type updater struct {
	config  UpdateConfig
	logger  *slog.Logger
	emitter *EventEmitter
	// running is the version that runs; nil if it doesn't parse, which
	// any release is newer than.
	running *semver.Version

	// holding is set while the update waits for the agent or restarts the
	// server: queued messages stay queued, for the new program.
	holding atomic.Bool

	// mu guards status and run. It is taken after the server's mu and
	// before the emitter's lock, which publishing the status takes.
	mu     sync.Mutex
	status UpdateStatus
	// run is the update under way, nil when there is none. It stays set
	// once it restarts the server.
	run *updateRun
}

// updateRun is one update, from the request that starts it until it
// restarts the server, fails or is cancelled.
type updateRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	// now, once closed, has it restart without waiting for the agent.
	now     chan struct{}
	nowOnce sync.Once
	// checked gets what the check found, for the request that started it.
	checked chan updateCheck
}

func (r *updateRun) restartNow() { r.nowOnce.Do(func() { close(r.now) }) }

func (r *updateRun) isNow() bool {
	select {
	case <-r.now:
		return true
	default:
		return false
	}
}

// updateCheck is what the check of an update found.
type updateCheck struct {
	upToDate bool
	err      error
	// cancelled: DELETE /update or the server's stop ended the update.
	cancelled bool
}

// newUpdater returns the server's updater, which reports its status in the
// summary from now on.
func newUpdater(config UpdateConfig, logger *slog.Logger, emitter *EventEmitter, keepsConversation bool) *updater {
	if config.Version == "" {
		config.Version = version.Version
	}
	u := &updater{config: config, logger: logger, emitter: emitter}
	u.running, _ = selfupdate.ParseVersion(config.Version)
	u.status = UpdateStatus{Allowed: config.Allowed, State: UpdateStateIdle, KeepsConversation: keepsConversation}
	if config.Allowed {
		program, blocked := u.program()
		u.status.Blocked = blocked
		if blocked != "" {
			logger.Warn("Updates through the API are on, but AgentAPI can't update itself", "reason", blocked)
		} else {
			logger.Info("Updates through the API are on", "program", program.Real)
		}
	}
	emitter.SetUpdate(u.status)
	return u
}

// program finds the program an update replaces, and says why the server
// can't update it, if it can't.
func (u *updater) program() (selfupdate.Program, string) {
	switch {
	case u.config.Restart == nil:
		return selfupdate.Program{}, "AgentAPI can't restart in place on this system, so it can't update itself"
	case u.config.Program == "":
		return selfupdate.Program{}, "AgentAPI didn't find its program when it started, so it can't restart into a new one"
	}
	program, err := selfupdate.Locate(u.config.Program)
	if err != nil {
		return selfupdate.Program{}, fmt.Sprintf("can't find the program: %v", err)
	}
	return program, program.Blocked()
}

// watchBlocked checks again, now and then, whether the server can replace
// its program, which the user may have fixed meanwhile.
func (u *updater) watchBlocked(s *Server) {
	if !u.config.Allowed {
		return
	}
	s.clock.TickerFunc(s.shutdownCtx, updateBlockedInterval, func() error {
		_, blocked := u.program()
		u.mu.Lock()
		defer u.mu.Unlock()
		u.setBlockedLocked(blocked)
		return nil
	}, "updateBlocked")
}

// setBlockedLocked records why the server can't replace its program.
// Caller holds u.mu.
func (u *updater) setBlockedLocked(blocked string) {
	if u.status.Blocked != blocked {
		u.status.Blocked = blocked
		u.publishLocked()
	}
}

// publishLocked puts the status in the summary. Caller holds u.mu.
func (u *updater) publishLocked() {
	u.holding.Store(u.status.State == UpdateStateWaiting || u.status.State == UpdateStateRestarting)
	u.emitter.SetUpdate(u.status)
}

// holdsQueue reports whether queued messages wait for the new program.
func (u *updater) holdsQueue() bool {
	return u != nil && u.holding.Load()
}

// responseLocked is the answer that reports the status. Caller holds u.mu.
func (u *updater) responseLocked(status int) *UpdateResponse {
	return &UpdateResponse{Status: status, Body: UpdateResponseBody{State: u.status.State, Target: u.status.Target}}
}

// advance moves run on to state, with target once it is known. It reports
// false, changing nothing, once run is no longer the update under way: a
// DELETE /update cancelled it.
func (u *updater) advance(run *updateRun, state UpdateState, target string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.run != run {
		return false
	}
	u.status.State = state
	if target != "" {
		u.status.Target = target
	}
	u.publishLocked()
	return true
}

// end ends run: idle when there was nothing to do, failed with err.
func (u *updater) end(run *updateRun, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.run != run {
		return
	}
	u.run = nil
	run.cancel()
	if err != nil {
		u.logger.Error("The update failed", "error", err)
		u.status.State, u.status.Error = UpdateStateFailed, err.Error()
	} else {
		u.status.State, u.status.Target, u.status.Error = UpdateStateIdle, "", ""
	}
	u.publishLocked()
}

// check looks up the latest release and the program on disk, and returns
// the version to restart into and the release to install first (nil when
// the program on disk is that new already). It returns a nil version when
// the server runs the newest of them already.
func (u *updater) check(ctx context.Context, program selfupdate.Program) (*semver.Version, *selfupdate.Release, error) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	u.logger.Info("Looking up the latest release")
	release, err := u.config.Releases.Latest(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("look up the latest release: %w", err)
	}
	latest, err := release.Version()
	if err != nil {
		return nil, nil, fmt.Errorf("the latest release: %w", err)
	}
	// Another AgentAPI that shares the program may have installed it.
	disk, err := selfupdate.ProgramVersion(ctx, program.Real)
	if err != nil {
		u.logger.Warn("Can't tell the version of the program on disk; installing the latest release", "error", err)
	}
	target := latest
	if disk != nil && !disk.LessThan(latest) {
		target, release = disk, nil
	}
	u.logger.Info("Found the latest release", "latest", latest.String(), "onDisk", versionString(disk), "running", u.config.Version)
	if u.running != nil && !target.GreaterThan(u.running) {
		return nil, nil, nil
	}
	return target, release, nil
}

func versionString(v *semver.Version) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// postUpdate handles POST /update.
func (s *Server) postUpdate(ctx context.Context, input *UpdateRequest) (*UpdateResponse, error) {
	u := s.updater
	if !u.config.Allowed {
		return nil, huma.Error403Forbidden(errUpdatesOff.Error())
	}
	when := UpdateWhenIdle
	if input.Body != nil && input.Body.When != "" {
		when = input.Body.When
	}
	if s.shutdownCtx.Err() != nil {
		return nil, huma.Error409Conflict(errStopping.Error())
	}
	program, blocked := u.program()
	u.mu.Lock()
	u.setBlockedLocked(blocked)
	if run := u.run; run != nil {
		// One update at a time; "now" hurries the one under way.
		if when == UpdateWhenNow && !run.isNow() {
			u.logger.Info("The update restarts AgentAPI without waiting for the agent, as asked")
			run.restartNow()
		}
		defer u.mu.Unlock()
		return u.responseLocked(http.StatusAccepted), nil
	}
	if blocked != "" {
		u.mu.Unlock()
		return nil, huma.Error409Conflict(blocked)
	}
	runCtx, cancel := context.WithCancel(s.shutdownCtx)
	run := &updateRun{ctx: runCtx, cancel: cancel, now: make(chan struct{}), checked: make(chan updateCheck, 1)}
	if when == UpdateWhenNow {
		run.restartNow()
	}
	u.run = run
	u.status.State, u.status.Target, u.status.Error = UpdateStateChecking, "", ""
	u.publishLocked()
	u.mu.Unlock()
	u.logger.Info("Updating AgentAPI", "when", when, "running", u.config.Version, "program", program.Path)
	go s.runUpdate(run, program)

	select {
	case checked := <-run.checked:
		switch {
		case checked.cancelled:
			return nil, huma.Error409Conflict("the update was cancelled")
		case checked.err != nil:
			return nil, huma.Error502BadGateway(checked.err.Error())
		case checked.upToDate:
			return &UpdateResponse{Status: http.StatusOK, Body: UpdateResponseBody{State: UpdateStateIdle, UpToDate: true}}, nil
		}
		u.mu.Lock()
		defer u.mu.Unlock()
		return u.responseLocked(http.StatusAccepted), nil
	case <-ctx.Done():
		// The client went away; the update goes on.
		return nil, ctx.Err()
	}
}

// runUpdate runs the update run of program: the check, the installation,
// the wait for the agent, and the restart.
func (s *Server) runUpdate(run *updateRun, program selfupdate.Program) {
	u := s.updater
	target, release, err := u.check(run.ctx, program)
	if err != nil || target == nil {
		cancelled := run.ctx.Err() != nil
		if err == nil {
			u.logger.Info("AgentAPI runs the latest release already", "version", u.config.Version)
		}
		u.end(run, err)
		run.checked <- updateCheck{upToDate: err == nil, err: err, cancelled: cancelled}
		return
	}
	// The request that started the update answers once the state moved on
	// from checking, or the update ended.
	var once sync.Once
	moved := func() {
		once.Do(func() { run.checked <- updateCheck{cancelled: run.ctx.Err() != nil} })
	}
	defer moved()
	version := target.String()
	if release != nil {
		if !u.advance(run, UpdateStateDownloading, version) {
			return
		}
		moved()
		u.logger.Info("Downloading the release", "version", version, "program", program.Real)
		ctx, cancel := context.WithTimeout(run.ctx, updateInstallTimeout)
		installed, err := u.config.Releases.Install(ctx, release, program, false)
		cancel()
		switch {
		case err != nil:
			u.end(run, fmt.Errorf("install %s: %w", version, err))
			return
		case installed:
			u.logger.Info("Installed the release", "version", version, "program", program.Real)
		default:
			u.logger.Info("Another AgentAPI installed the release already", "version", version, "program", program.Real)
		}
	} else {
		u.logger.Info("The program on disk is newer than the one running: restarting into it", "version", version, "program", program.Real)
	}
	if !s.awaitUpdateRestart(run, version, moved) {
		return
	}
	u.logger.Info("Restarting into the new version", "version", version)
	if err := u.config.Restart(run.ctx); err != nil {
		s.mu.Lock()
		s.updating.Store(false)
		s.mu.Unlock()
		u.end(run, fmt.Errorf("restart into %s: %w", version, err))
	}
}

// awaitUpdateRestart begins the restart of run into version
// (beginUpdateRestart) once the agent is idle, or at once if run restarts
// now; meanwhile the state says it waits. moved is called once the state
// moved on. It reports false when run was cancelled or the server stops.
func (s *Server) awaitUpdateRestart(run *updateRun, version string, moved func()) bool {
	// Subscribed first, so no change goes unseen.
	statusChanges := func(t EventType) bool { return t == EventTypeStatusChange }
	id, changes, _ := s.emitter.SubscribeTo(statusChanges, 0, "")
	defer func() { s.emitter.Unsubscribe(id) }()
	waiting := false
	for {
		began, current := s.beginUpdateRestart(run, version)
		switch {
		case began:
			moved()
			return true
		case !current:
			return false
		case !waiting:
			if !s.updater.advance(run, UpdateStateWaiting, version) {
				return false
			}
			moved()
			s.logger.Info("Waiting for the agent to be idle to restart", "version", version)
			waiting = true
		}
		select {
		case <-run.ctx.Done():
			return false
		case <-run.now:
		case _, ok := <-changes:
			if !ok {
				// The emitter drops a subscription that falls behind.
				id, changes, _ = s.emitter.SubscribeTo(statusChanges, 0, "")
			}
		}
	}
}

// beginUpdateRestart begins the restart of run into version once the
// agent is idle, or at once if run restarts now: the state says
// restarting, and from now on messages wait in the queue, which sends
// nothing, so the agent starts no new work. It reports whether it began,
// and whether run is still the update under way.
func (s *Server) beginUpdateRestart(run *updateRun, version string) (began, current bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The agent's own view, not the emitter's: an agent that exited in the
	// middle of a turn has no work left, though its last status said it
	// worked.
	if !run.isNow() && (s.turnGateLocked(turnUser, s.clock.Now()) != nil || s.native.Busy()) {
		return false, true
	}
	if !s.updater.advance(run, UpdateStateRestarting, version) {
		return false, false
	}
	s.updating.Store(true)
	return true, true
}

// deleteUpdate handles DELETE /update.
func (s *Server) deleteUpdate(_ context.Context, _ *struct{}) (*UpdateResponse, error) {
	u := s.updater
	if !u.config.Allowed {
		return nil, huma.Error403Forbidden(errUpdatesOff.Error())
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.status.State == UpdateStateRestarting {
		return nil, huma.Error409Conflict(fmt.Sprintf("AgentAPI is restarting into %s already", u.status.Target))
	}
	if run := u.run; run != nil {
		run.cancel()
		u.run = nil
		u.logger.Info("Cancelled the update", "target", u.status.Target)
	}
	u.status.State, u.status.Target, u.status.Error = UpdateStateIdle, "", ""
	u.publishLocked()
	return u.responseLocked(http.StatusOK), nil
}

func (s *Server) registerUpdateRoutes() {
	huma.Post(s.api, "/update", s.postUpdate, func(o *huma.Operation) {
		o.Summary = "Update AgentAPI"
		o.Description = "Installs the latest official release of AgentAPI (k1dav-c/agentapi on GitHub) for this " +
			"machine's OS and architecture, checked against the release's checksums.txt, in place of the program " +
			"AgentAPI runs, and restarts AgentAPI in place into it: the same process, a new server_id and version. " +
			"The request can't choose a version or a URL. AgentAPIs that share the program replace it under a lock " +
			"file next to it, and one that finds the program on disk newer than the one it runs only restarts. " +
			"`when` is `idle` (the default: once no turn runs and no request is open) or " +
			"`now`. Returns 200 with `up_to_date` when AgentAPI runs the latest release already, else 202 with " +
			"the state and the version it updates to; while an update is under way it returns its state, and " +
			"`now` has it restart without waiting. 403 unless AgentAPI runs with --allow-update; 409 when it " +
			"can't replace its program (the message and the summary's update.blocked say why and how to fix it) " +
			"or stops; 502 when the latest release can't be looked up. Every change of the state sends " +
			"summary_update. With --state-db the conversation and the agent's session carry over the restart; " +
			"without it the conversation starts anew."
		o.Responses = map[string]*huma.Response{
			"200": {Description: "AgentAPI runs the latest release already."},
		}
		o.Errors = []int{403, 409, 502}
	})
	// 202 has the body of 200.
	if op := s.api.OpenAPI().Paths["/update"].Post; op != nil {
		if ok := op.Responses["200"]; ok != nil {
			op.Responses["202"] = &huma.Response{Description: "The update is under way.", Content: ok.Content}
		}
	}
	huma.Delete(s.api, "/update", s.deleteUpdate, func(o *huma.Operation) {
		o.Summary = "Cancel an update"
		o.Description = "Cancels the update under way while it hasn't begun to restart AgentAPI (it looks up the " +
			"release, downloads it or waits for the agent to be idle), and forgets a failed one. A program it " +
			"installed already stays. 409 once AgentAPI restarts; 403 unless it runs with --allow-update."
		o.Errors = []int{403, 409}
	})
}
