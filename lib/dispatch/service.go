package dispatch

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/github"
	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/jsonapi"
)

// Config is how the dispatch runs.
type Config struct {
	// StatePath keeps the machines and jobs (dispatch.json); empty keeps
	// none. AppPath keeps the GitHub App (github-app.json).
	StatePath string
	AppPath   string
	// Secret is the hub's: the launchers sign their events and token
	// requests with it.
	Secret string
	// DispatchToken lets a client (the GitHub workflow's runner) call
	// POST /api/dispatch alone.
	DispatchToken string
	GitHub        *github.Client
	// HTTP reaches the launchers.
	HTTP     *http.Client
	Logger   *slog.Logger
	Now      func() time.Time
	Location *time.Location
}

// Error is a dispatch the hub refuses or that failed, with the HTTP status
// and a code clients tell apart.
type Error = jsonapi.Error

var errorf = jsonapi.Errorf

func isCode(err error, code string) bool { return jsonapi.IsCode(err, code) }

// Service is the dispatch.
type Service struct {
	config Config
	logger *slog.Logger
	gh     *github.Client

	mu         sync.Mutex
	hosts      map[string]*Host
	jobs       map[string]*Job
	deliveries map[string]time.Time
	app        *github.App
	// states are the GitHub setup's pending states, until they expire.
	states map[string]time.Time
	// tokenRequests are the launchers' token requests taken, by delivery
	// ID, so a copy isn't answered.
	tokenRequests map[string]time.Time
	// subs are the subscribers to the machines and jobs (Subscribe), and
	// onlineSeen whether each machine was online as they last read it
	// (CheckOnline).
	subs       map[chan struct{}]struct{}
	onlineSeen map[string]bool
	// jobLocks serialize the dispatches of a job, by the key's hash: a
	// fixed set, so they don't grow with the jobs.
	jobLocks [64]sync.Mutex

	// The state file is written in the background (writeLoop): saveLocked
	// leaves the latest in pending.
	pending   []byte
	writeMu   sync.Mutex
	nudge     chan struct{}
	done      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
}

// storedHost is a machine as dispatch.json keeps it: with its launcher's
// token, which the hub never sends.
type storedHost struct {
	Host
	Token string `json:"token"`
}

// stateFile is dispatch.json.
type stateFile struct {
	Version    int                  `json:"version"`
	Hosts      []storedHost         `json:"hosts"`
	Jobs       []*Job               `json:"jobs"`
	Deliveries map[string]time.Time `json:"deliveries"`
}

const stateVersion = 1

// Timings.
const (
	// deliveryTTL is how long a dispatch's delivery ID counts.
	deliveryTTL = 24 * time.Hour
	// keepJobs is how long an ended or failed job stays, for its spend.
	keepJobs = 30 * 24 * time.Hour
	// writeDelay gathers the changes of the state file into one write.
	writeDelay = 200 * time.Millisecond
	// missedHeartbeats a machine may miss before it counts as offline.
	missedHeartbeats = 3
)

// New returns the dispatch of config, with what it kept.
func New(config Config) (*Service, error) {
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Location == nil {
		config.Location = time.Local
	}
	if config.GitHub == nil {
		config.GitHub = github.NewClient()
	}
	if config.HTTP == nil {
		config.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	s := &Service{
		config: config, logger: config.Logger, gh: config.GitHub,
		hosts: map[string]*Host{}, jobs: map[string]*Job{}, deliveries: map[string]time.Time{},
		states: map[string]time.Time{}, tokenRequests: map[string]time.Time{},
		subs: map[chan struct{}]struct{}{}, onlineSeen: map[string]bool{},
		nudge: make(chan struct{}, 1), done: make(chan struct{}), stopped: make(chan struct{}),
	}
	if config.StatePath != "" {
		data, err := os.ReadFile(config.StatePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			var file stateFile
			if err := json.Unmarshal(data, &file); err != nil {
				return nil, fmt.Errorf("read %s: %w", config.StatePath, err)
			}
			if file.Version != stateVersion {
				return nil, fmt.Errorf("read %s: version %d, this hub reads %d", config.StatePath, file.Version, stateVersion)
			}
			for _, h := range file.Hosts {
				host := h.Host
				host.Token = h.Token
				s.hosts[h.Hostname] = &host
			}
			for _, j := range file.Jobs {
				// The hub may have missed the cost of the tasks that ended
				// while it didn't run.
				if run := j.current(); run != nil {
					run.Missed = true
				}
				s.jobs[j.Key] = j
			}
			if file.Deliveries != nil {
				s.deliveries = file.Deliveries
			}
		}
	}
	if config.AppPath != "" {
		app, err := LoadApp(config.AppPath)
		if err != nil {
			return nil, err
		}
		s.app = app
	}
	go s.writeLoop()
	return s, nil
}

// LoadApp reads a GitHub App file; a missing one is none.
func LoadApp(path string) (*github.App, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var app github.App
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("read the GitHub App %s: %w", path, err)
	}
	if app.ID == 0 || app.PEM == "" {
		return nil, fmt.Errorf("the GitHub App %s has no app_id or pem", path)
	}
	return &app, nil
}

// saveLocked has dispatch.json written with the state now, in the
// background. s.mu is held.
func (s *Service) saveLocked() {
	s.notifyLocked()
	if s.config.StatePath == "" {
		return
	}
	now := s.config.Now()
	for id, at := range s.deliveries {
		if now.Sub(at) > deliveryTTL {
			delete(s.deliveries, id)
		}
	}
	file := stateFile{Version: stateVersion, Deliveries: s.deliveries}
	for _, h := range s.hosts {
		file.Hosts = append(file.Hosts, storedHost{Host: *h, Token: h.Token})
	}
	for key, j := range s.jobs {
		if (j.State.ended() || j.State == JobFailed) && now.Sub(j.UpdatedAt) > keepJobs {
			delete(s.jobs, key)
			continue
		}
		file.Jobs = append(file.Jobs, j)
	}
	slices.SortFunc(file.Hosts, func(a, b storedHost) int { return strings.Compare(a.Hostname, b.Hostname) })
	slices.SortFunc(file.Jobs, func(a, b *Job) int { return strings.Compare(a.Key, b.Key) })
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		s.logger.Error("Couldn't keep the dispatch's machines and jobs", "error", err)
		return
	}
	s.pending = append(data, '\n')
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

func (s *Service) writeLoop() {
	defer close(s.stopped)
	for {
		select {
		case <-s.nudge:
		case <-s.done:
			return
		}
		select {
		case <-time.After(writeDelay):
		case <-s.done:
		}
		s.Flush()
	}
}

// Flush writes the state file now, if a change waits.
func (s *Service) Flush() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	data := s.pending
	s.pending = nil
	s.mu.Unlock()
	if data == nil {
		return
	}
	err := os.MkdirAll(filepath.Dir(s.config.StatePath), 0o700)
	if err == nil {
		err = atomicfile.Write(s.config.StatePath, ".dispatch-*", data)
	}
	if err != nil {
		s.logger.Error("Couldn't keep the dispatch's machines and jobs", "file", s.config.StatePath, "error", err)
	}
}

// Close writes what waits and stops writing.
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		<-s.stopped
		s.Flush()
	})
}

// activeLocked is how many jobs h runs: those its launcher reported last,
// or those the hub started on it since, if more. s.mu is held.
func (s *Service) activeLocked(h *Host) int {
	n := 0
	for _, j := range s.jobs {
		if j.Host == h.Hostname && j.working() {
			n++
		}
	}
	reported := 0
	for _, j := range h.Jobs {
		if j.State != host.JobFailed {
			reported++
		}
	}
	return max(n, reported)
}

func (s *Service) online(h *Host) bool {
	every := time.Duration(cmp.Or(h.HeartbeatSeconds, 60)) * time.Second
	return !h.Stopped && s.config.Now().Sub(h.LastSeen) < missedHeartbeats*every
}

// Hosts are the machines, with whether each is online.
func (s *Service) Hosts() []Host {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostsLocked()
}

func (s *Service) hostsLocked() []Host {
	out := make([]Host, 0, len(s.hosts))
	for _, h := range s.hosts {
		c := *h
		c.Online = s.online(h)
		c.Active = s.activeLocked(h)
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Host) int { return strings.Compare(a.Hostname, b.Hostname) })
	return out
}

// Jobs are the jobs, the latest change first.
func (s *Service) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobsLocked()
}

func (s *Service) jobsLocked() []Job {
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.clone())
	}
	slices.SortFunc(out, func(a, b Job) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

// Board is the machines and the jobs at once, as the hub's stream sends
// them.
type Board struct {
	Jobs  []Job  `json:"jobs"`
	Hosts []Host `json:"hosts"`
}

// Board returns the machines and the jobs now.
func (s *Service) Board() Board {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markOnlineLocked()
	return Board{Jobs: s.jobsLocked(), Hosts: s.hostsLocked()}
}

// Subscribe returns a channel that gets a value after the machines or the
// jobs change (read Board then), and cancel, which ends the subscription.
// Changes close together come as one: the channel holds one value.
func (s *Service) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// notifyLocked tells the subscribers of a change. s.mu is held.
func (s *Service) notifyLocked() {
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// CheckOnline tells the subscribers when a machine went offline (or came
// back) without an event: its heartbeats stopped. The hub calls it on its
// own heartbeat.
func (s *Service) CheckOnline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, h := range s.hosts {
		if s.online(h) != s.onlineSeen[name] {
			s.notifyLocked()
			return
		}
	}
}

// markOnlineLocked keeps whether each machine is online, as a Board said
// it. s.mu is held.
func (s *Service) markOnlineLocked() {
	clear(s.onlineSeen)
	for name, h := range s.hosts {
		s.onlineSeen[name] = s.online(h)
	}
}

// HostEvent takes a launcher's host.* event, signed with the hub's
// secret.
//
// The hub's secret is every machine's that joined, so it doesn't name one:
// a machine's name is pinned to the launcher's token it first came with
// (trust on first use), and an event with another token is refused, the
// machine online or not, whatever the event (host.stopped too). Otherwise
// a machine with the secret could take over another's name, and with it
// its jobs' GitHub tokens and the dispatches to it.
func (s *Service) HostEvent(eventType string, data json.RawMessage) error {
	var d host.HostEventData
	if err := json.Unmarshal(data, &d); err != nil || d.Hostname == "" || d.URL == "" {
		return errors.New("a host event names no hostname or URL")
	}
	if d.Token == "" {
		return errors.New("a host event has no launcher's token")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.config.Now()
	h := s.hosts[d.Hostname]
	switch {
	case h == nil:
		s.logger.Info("A machine's launcher joined", "host", d.Hostname, "url", d.URL)
		h = &Host{Token: d.Token}
		s.hosts[d.Hostname] = h
	case h.Token == "":
		// Kept by a hub that didn't pin the token.
		h.Token = d.Token
	case !hmac.Equal([]byte(d.Token), []byte(h.Token)):
		s.logger.Warn("Refused a host event with another launcher's token than the machine's", "host", d.Hostname, "url", d.URL, "event", eventType)
		return fmt.Errorf("the machine %s has another launcher's token", d.Hostname)
	}
	h.HostInfo = d.HostInfo
	h.HeartbeatSeconds = d.HeartbeatSeconds
	h.LastSeen = now
	h.Stopped = eventType == host.EventHostStopped
	if !h.Stopped {
		s.reconcileLocked(h)
	}
	s.saveLocked()
	return nil
}

// reconcileLocked compares the jobs of h the hub knows with those the
// launcher reports: one the launcher dropped while the hub was away ended,
// one it reports failed failed. s.mu is held.
func (s *Service) reconcileLocked(h *Host) {
	reported := map[string]host.JobInfo{}
	for _, j := range h.Jobs {
		reported[j.Key] = j
	}
	now := s.config.Now()
	// A run the hub started lately may be newer than the report, which the
	// launcher made before it.
	grace := 2 * time.Duration(cmp.Or(h.HeartbeatSeconds, 60)) * time.Second
	for _, job := range s.jobs {
		if run := job.current(); run != nil && now.Sub(run.StartedAt) < grace {
			continue
		}
		if job.Host != h.Hostname || job.State == JobDone {
			continue
		}
		lj, ok := reported[job.Key]
		switch {
		case job.State == JobStopped:
			if ok {
				// Stopped while the machine was offline: stop it there now.
				go s.stopOnLauncher(*h, job.Key)
			} else {
				job.State, job.UpdatedAt = JobDone, now
			}
		case !ok && job.State != JobFailed && job.State != JobStarting:
			job.State, job.Error, job.UpdatedAt = JobFailed, "the machine no longer has the job", now
			s.endRunLocked(job, "failed")
		case ok && lj.State == host.JobFailed && job.State != JobFailed:
			job.State, job.Error, job.UpdatedAt = JobFailed, lj.Error, now
		}
	}
}

func (s *Service) stopOnLauncher(h Host, key string) {
	k, err := host.ParseKey(key)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.callLauncher(ctx, h, http.MethodDelete, "/jobs/"+k.Slug(), nil, nil); err != nil && !isCode(err, "job_not_found") {
		s.logger.Warn("Couldn't stop a job on its machine", "job", key, "host", h.Hostname, "error", err)
	}
}

// taskEventData is what a task event's data has of the task's cost.
type taskEventData struct {
	DurationSeconds float64 `json:"duration_seconds"`
	Cost            *struct {
		USD    float64 `json:"usd"`
		Tokens struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			CacheWrite int64 `json:"cache_write"`
			CacheRead  int64 `json:"cache_read"`
		} `json:"tokens"`
		Calls int `json:"calls"`
	} `json:"cost"`
}

// TaskEvent takes an AgentAPI's task.* event: the job of that AgentAPI
// works, or waits for review, and its run adds the task's cost.
func (s *Service) TaskEvent(instanceID, eventType string, data json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.jobs {
		run := job.current()
		if run == nil || run.InstanceID != instanceID {
			continue
		}
		now := s.config.Now()
		switch eventType {
		case "task.started":
			if job.State != JobFailed {
				job.State = JobWorking
			}
		case "task.completed", "task.failed":
			var d taskEventData
			_ = json.Unmarshal(data, &d)
			c := Cost{Tasks: 1, WorkSeconds: d.DurationSeconds}
			if d.Cost != nil {
				c.USD, c.Calls = d.Cost.USD, d.Cost.Calls
				c.Tokens = Tokens{Input: d.Cost.Tokens.Input, Output: d.Cost.Tokens.Output, CacheWrite: d.Cost.Tokens.CacheWrite, CacheRead: d.Cost.Tokens.CacheRead}
			}
			run.Cost.add(c)
			if job.State == JobWorking || job.State == JobStarting {
				job.State = JobWorking
				if job.PRURL != "" {
					job.State = JobReview
				}
			}
		default:
			return
		}
		job.UpdatedAt = now
		s.saveLocked()
		return
	}
}

// endRunLocked ends job's current run. s.mu is held.
func (s *Service) endRunLocked(job *Job, reason string) {
	if run := job.current(); run != nil {
		run.EndedAt, run.EndReason = s.config.Now(), reason
	}
}

func (s *Service) lockJob(key string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	l := &s.jobLocks[h.Sum32()%uint32(len(s.jobLocks))]
	l.Lock()
	return l.Unlock
}

// Dispatch does what req asks.
func (s *Service) Dispatch(ctx context.Context, req Request) (Response, error) {
	owner, name, err := host.ParseRepo(req.Repo)
	if err != nil {
		return Response{}, errorf(http.StatusBadRequest, "bad_request", "%v", err)
	}
	if req.Issue <= 0 {
		return Response{}, errorf(http.StatusBadRequest, "bad_request", "issue is a positive number")
	}
	key := host.Key{Owner: owner, Name: name, Issue: req.Issue}
	defer s.lockJob(key.String())()

	s.mu.Lock()
	if req.DeliveryID != "" {
		if _, seen := s.deliveries[req.DeliveryID]; seen {
			var job Job
			if j := s.jobs[key.String()]; j != nil {
				job = j.clone()
			}
			s.mu.Unlock()
			return Response{Job: job, Duplicate: true}, nil
		}
	}
	s.mu.Unlock()

	var resp Response
	switch req.Action {
	case ActionStart:
		resp, err = s.start(ctx, key, req)
	case ActionSend:
		resp, err = s.send(ctx, key, req)
	case ActionLinkPR:
		resp, err = s.linkPR(key, req)
	case ActionStop:
		resp, err = s.stop(ctx, key)
	default:
		return Response{}, errorf(http.StatusBadRequest, "bad_request", "action %q: want start, send, link_pr or stop", req.Action)
	}
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			s.logger.Warn("Refused a dispatch", "action", req.Action, "job", key.String(), "code", e.Code, "error", e.Message)
		}
		return Response{}, err
	}
	if req.DeliveryID != "" {
		s.mu.Lock()
		s.deliveries[req.DeliveryID] = s.config.Now()
		s.saveLocked()
		s.mu.Unlock()
	}
	return resp, nil
}

func (s *Service) currentApp() *github.App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app
}

// repoClient is the app's client for repo; without an app, ok is false
// and the dispatch works without GitHub (the machines' own credentials).
func (s *Service) repoClient(ctx context.Context, repo string) (as github.As, ok bool, err error) {
	app := s.currentApp()
	if app == nil {
		return github.As{}, false, nil
	}
	token, err := s.gh.RepoToken(ctx, app, repo)
	if err != nil {
		if github.IsStatus(err, http.StatusNotFound) {
			return github.As{}, false, errorf(http.StatusPreconditionFailed, "github_not_installed",
				"the hub's GitHub App isn't installed on %s: install it at %s", repo, app.InstallURL(s.gh.Web, ""))
		}
		return github.As{}, false, errorf(http.StatusBadGateway, "github_failed", "%v", err)
	}
	return s.gh.With(token), true, nil
}

func (s *Service) job(key string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[key]; j != nil {
		c := j.clone()
		return &c
	}
	return nil
}

// update changes the job key under s.mu and keeps it, returning it.
func (s *Service) update(key string, change func(*Job)) Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[key]
	if j == nil {
		j = &Job{Key: key, CreatedAt: s.config.Now()}
		s.jobs[key] = j
	}
	change(j)
	j.UpdatedAt = s.config.Now()
	s.saveLocked()
	return j.clone()
}

func (s *Service) hostByName(name string) (Host, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hosts[name]
	if h == nil || !s.online(h) {
		return Host{}, false
	}
	return *h, true
}

func supports(h Host, agent string) bool {
	if agent == "" {
		return h.DefaultAgent != ""
	}
	return slices.Contains(h.Agents, agent)
}

// pickHost is the machine to start key on: the one asked for, else one
// online that runs the agent and has room, with the repository if one
// has, the fewest jobs first.
func (s *Service) pickHost(key host.Key, agent, want string) (Host, error) {
	s.mu.Lock()
	var hosts []Host
	for _, h := range s.hosts {
		if s.online(h) {
			c := *h
			c.Active = s.activeLocked(h)
			hosts = append(hosts, c)
		}
	}
	s.mu.Unlock()
	slices.SortFunc(hosts, func(a, b Host) int {
		return cmp.Or(cmp.Compare(a.Active, b.Active), strings.Compare(a.Hostname, b.Hostname))
	})
	room := func(h Host) bool { return h.Active < h.MaxJobs }
	has := func(h Host) bool { return slices.Contains(h.Repos, key.Repo()) }
	if want != "" {
		i := slices.IndexFunc(hosts, func(h Host) bool { return h.Hostname == want })
		if i < 0 {
			return Host{}, errorf(http.StatusServiceUnavailable, "host_offline", "the machine %s isn't online", want)
		}
		h := hosts[i]
		switch {
		case !supports(h, agent):
			return Host{}, errorf(http.StatusUnprocessableEntity, "agent_unavailable", "%s doesn't run %q (it runs %v)", want, agent, h.Agents)
		case !has(h) && !h.Clone:
			return Host{}, errorf(http.StatusUnprocessableEntity, "repo_missing", "%s has no clone of %s", want, key.Repo())
		case !room(h):
			return Host{}, errorf(http.StatusConflict, "capacity", "%s runs %d jobs, its most", want, h.Active)
		}
		return h, nil
	}
	var cloners []Host
	for _, h := range hosts {
		if !supports(h, agent) || !room(h) {
			continue
		}
		if has(h) {
			return h, nil
		}
		if h.Clone {
			cloners = append(cloners, h)
		}
	}
	if len(cloners) > 0 {
		return cloners[0], nil
	}
	return Host{}, errorf(http.StatusServiceUnavailable, "no_host", "no machine online runs %s for %s with room", cmp.Or(agent, "an agent"), key.Repo())
}

// issueMeta is what the dispatch reads of an issue.
type issueMeta struct {
	title, url, defaultBranch string
	labels                    []string
}

func (s *Service) readIssue(ctx context.Context, as github.As, ok bool, key host.Key) (issueMeta, error) {
	meta := issueMeta{url: fmt.Sprintf("%s/%s/issues/%d", strings.TrimSuffix(s.gh.Web, "/"), key.Repo(), key.Issue)}
	if !ok {
		return meta, nil
	}
	issue, err := as.Issue(ctx, key.Repo(), key.Issue)
	if err != nil {
		return meta, errorf(http.StatusBadGateway, "github_failed", "%v", err)
	}
	if issue.PullRequest != nil {
		return meta, errorf(http.StatusUnprocessableEntity, "not_an_issue", "%s is a pull request", key)
	}
	repo, err := as.Repo(ctx, key.Repo())
	if err != nil {
		return meta, errorf(http.StatusBadGateway, "github_failed", "%v", err)
	}
	meta.title, meta.url, meta.defaultBranch = issue.Title, cmp.Or(issue.HTMLURL, meta.url), repo.DefaultBranch
	for _, l := range issue.Labels {
		meta.labels = append(meta.labels, l.Name)
	}
	return meta, nil
}

func (s *Service) start(ctx context.Context, key host.Key, req Request) (Response, error) {
	as, gh, err := s.repoClient(ctx, key.Repo())
	if err != nil {
		return Response{}, err
	}
	job := s.job(key.String())
	if job != nil && !job.State.ended() && job.State != JobFailed && (req.Agent == "" || req.Agent == job.Agent) {
		return Response{Job: *job}, nil
	}
	if job != nil && !job.State.ended() && req.Agent != "" && req.Agent != job.Agent && req.Replace != nil && !*req.Replace {
		return Response{}, errorf(http.StatusConflict, "job_active", "%s works on %s; replace to switch agents", job.Agent, key)
	}
	// Read before anything stops: an issue GitHub can't give leaves the
	// work as it is.
	meta, err := s.readIssue(ctx, as, gh, key)
	if err != nil {
		return Response{}, err
	}
	if job != nil && !job.State.ended() && job.State != JobFailed {
		return s.switchAgent(ctx, key, req, job, meta, as, gh)
	}
	return s.launch(ctx, key, req, job, meta, as, gh, "")
}

// launch starts key on a machine: the one it works on (a failed job, an
// agent switch) or a new pick. previous is the agent another run left the
// worktree by, for the takeover prompt. A launch that fails on the job's
// machine leaves the job failed, with why.
func (s *Service) launch(ctx context.Context, key host.Key, req Request, job *Job, meta issueMeta, as github.As, gh bool, previous string) (resp Response, err error) {
	ongoing := job != nil && !job.State.ended()
	if ongoing {
		defer func() {
			if err != nil {
				s.update(key.String(), func(j *Job) {
					s.endRunLocked(j, "failed")
					j.State, j.Error = JobFailed, err.Error()
				})
			}
		}()
	}
	agent := req.Agent
	var h Host
	if ongoing {
		var ok bool
		if h, ok = s.hostByName(job.Host); !ok {
			return Response{}, errorf(http.StatusServiceUnavailable, "host_offline", "%s works on %s, which isn't online", key, job.Host)
		}
		agent = cmp.Or(agent, job.Agent)
		if !supports(h, agent) {
			return Response{}, errorf(http.StatusUnprocessableEntity, "agent_unavailable", "%s, where %s's worktree is, doesn't run %q", h.Hostname, key, agent)
		}
		if job.State == JobFailed && agent != job.Agent {
			// The machine starts the failed job again with another agent,
			// in the same worktree.
			previous = job.Agent
		}
	} else if h, err = s.pickHost(key, agent, req.Host); err != nil {
		return Response{}, err
	}
	agent = cmp.Or(agent, h.DefaultAgent)
	data := promptData{Repo: key.Repo(), Issue: key.Issue, IssueURL: meta.url, DefaultBranch: cmp.Or(meta.defaultBranch, "main"), Credentials: gh, PreviousAgent: agentName(previous)}
	if ongoing {
		data.PRURL = job.PRURL
	}
	prompt := render("start", data)
	if previous != "" {
		prompt = render("takeover", data)
	}
	start := host.StartRequest{Repo: key.Repo(), Issue: key.Issue, Agent: agent, DefaultBranch: meta.defaultBranch, Prompt: prompt}
	if gh {
		app := s.currentApp()
		start.GitUser = &host.GitUser{Name: app.BotName(), Email: app.BotEmail()}
		start.Credentials = host.CredentialsHub
	}
	var started host.StartResponse
	if err := s.callLauncher(ctx, h, http.MethodPost, "/jobs", start, &started); err != nil {
		return Response{}, err
	}
	now := s.config.Now()
	created := !ongoing
	updated := s.update(key.String(), func(j *Job) {
		if created {
			// A new round: its own pull request and cost comment; the runs
			// of the rounds before stay, for the spend of the job.
			j.Round++
			j.PRURL, j.CostCommentID = "", 0
		}
		if run := j.current(); run != nil && run.Agent == agent && previous == "" {
			// A failed job's AgentAPI started again: the same run goes on.
			run.Missed = true
		} else {
			s.endRunLocked(j, "replaced")
			j.Runs = append(j.Runs, Run{Round: j.Round, Agent: agent, Host: h.Hostname, InstanceID: started.Job.InstanceID, StartedAt: now})
		}
		j.Repo, j.Issue, j.Title, j.IssueURL = key.Repo(), key.Issue, cmp.Or(meta.title, j.Title), meta.url
		j.Host, j.Agent, j.InstanceID = h.Hostname, agent, started.Job.InstanceID
		j.DefaultBranch, j.Credentials = started.Job.DefaultBranch, gh
		j.State, j.Error = JobWorking, ""
	})
	s.logger.Info("Dispatched an issue", "job", key.String(), "host", h.Hostname, "agent", agent, "instance", started.Job.InstanceID)
	if gh {
		verb := "開始處理這個 issue"
		if previous != "" {
			verb = fmt.Sprintf("接手這個 issue（之前由 %s 處理）", agentName(previous))
		}
		s.comment(ctx, as, key, key.Issue, fmt.Sprintf("🤖 %s 在 `%s` 上%s。%s", agentName(agent), h.Hostname, verb, s.hubLink(started.Job.InstanceID)))
		s.dropOtherAgentLabels(ctx, as, key, meta.labels, agent)
	}
	return Response{Job: updated, Created: created}, nil
}

// switchAgent stops the agent that works on job, keeping its worktree, and
// has another take over. From the stop on, a failure leaves the job failed
// (launch), not as if the old agent still worked.
func (s *Service) switchAgent(ctx context.Context, key host.Key, req Request, job *Job, meta issueMeta, as github.As, gh bool) (Response, error) {
	h, ok := s.hostByName(job.Host)
	if !ok {
		return Response{}, errorf(http.StatusServiceUnavailable, "host_offline", "%s works on %s, which isn't online", key, job.Host)
	}
	if !supports(h, req.Agent) {
		return Response{}, errorf(http.StatusUnprocessableEntity, "agent_unavailable", "%s, where %s's worktree is, doesn't run %q", h.Hostname, key, req.Agent)
	}
	s.update(key.String(), func(j *Job) { j.State = JobStarting })
	if err := s.callLauncher(ctx, h, http.MethodDelete, "/jobs/"+key.Slug()+"?keep_worktree=true&keep_state=true", nil, nil); err != nil && !isCode(err, "job_not_found") {
		s.update(key.String(), func(j *Job) { j.State, j.Error = JobFailed, err.Error() })
		return Response{}, err
	}
	previous := job.Agent
	s.update(key.String(), func(j *Job) { s.endRunLocked(j, "replaced") })
	return s.launch(ctx, key, req, job, meta, as, gh, previous)
}

func (s *Service) send(ctx context.Context, key host.Key, req Request) (Response, error) {
	job := s.job(key.String())
	if job == nil {
		return Response{}, errorf(http.StatusNotFound, "job_not_found", "no job works on %s", key)
	}
	if job.State.ended() {
		return Response{}, errorf(http.StatusConflict, "job_ended", "the work on %s ended", key)
	}
	if strings.TrimSpace(req.Message) == "" && len(req.Comments) == 0 {
		return Response{}, errorf(http.StatusBadRequest, "bad_request", "a send has a message or comments")
	}
	h, ok := s.hostByName(job.Host)
	if !ok {
		return Response{}, errorf(http.StatusServiceUnavailable, "host_offline", "%s works on %s, which isn't online", key, job.Host)
	}
	content := render("review", promptData{
		Repo: key.Repo(), Issue: key.Issue, PRURL: job.PRURL,
		Author: cmp.Or(req.Author, "使用者"), ReviewState: req.ReviewState, Message: req.Message, Comments: req.Comments,
		Fence: newFence(),
	})
	var answer json.RawMessage
	if err := s.callLauncher(ctx, h, http.MethodPost, "/jobs/"+key.Slug()+"/message", host.MessageRequest{Content: content, DispatchMode: req.DispatchMode}, &answer); err != nil {
		return Response{}, err
	}
	source := sourceOf(ctx, req)
	updated := s.update(key.String(), func(j *Job) {
		if j.State == JobReview {
			j.State = JobWorking
		}
		m := Message{At: s.config.Now(), Source: source, Agent: j.Agent, Text: strings.TrimSpace(req.Message), ReviewState: req.ReviewState, Comments: len(req.Comments)}
		if source != SourceHub {
			m.Author = req.Author
		}
		j.addMessage(m)
	})
	return Response{Job: updated}, nil
}

// fromHubKey marks a dispatch's context as a signed-in browser's.
type fromHubKey struct{}

// FromHub is ctx for a dispatch someone signed in to the hub sent.
func FromHub(ctx context.Context) context.Context {
	return context.WithValue(ctx, fromHubKey{}, true)
}

// sourceOf is where a send came from: the hub's page, else a review (it
// has a state) or an /agent comment, through the workflow.
func sourceOf(ctx context.Context, req Request) MessageSource {
	switch {
	case ctx.Value(fromHubKey{}) != nil:
		return SourceHub
	case req.ReviewState != "":
		return SourceReview
	default:
		return SourceComment
	}
}

func (s *Service) linkPR(key host.Key, req Request) (Response, error) {
	if s.job(key.String()) == nil {
		return Response{}, errorf(http.StatusNotFound, "job_not_found", "no job works on %s", key)
	}
	if !strings.Contains(req.PRURL, "/pull/") {
		return Response{}, errorf(http.StatusBadRequest, "bad_request", "pr_url %q isn't a pull request's", req.PRURL)
	}
	updated := s.update(key.String(), func(j *Job) {
		j.PRURL = req.PRURL
		if !j.State.ended() && j.State != JobFailed {
			j.State = JobReview
		}
	})
	return Response{Job: updated}, nil
}

func (s *Service) stop(ctx context.Context, key host.Key) (Response, error) {
	job := s.job(key.String())
	if job == nil {
		return Response{}, errorf(http.StatusNotFound, "job_not_found", "no job works on %s", key)
	}
	if job.State.ended() {
		return Response{Job: *job}, nil
	}
	state := JobDone
	if h, ok := s.hostByName(job.Host); ok {
		if err := s.callLauncher(ctx, h, http.MethodDelete, "/jobs/"+key.Slug(), nil, nil); err != nil && !isCode(err, "job_not_found") {
			return Response{}, err
		}
	} else {
		state = JobStopped
	}
	as, gh, err := s.repoClient(ctx, key.Repo())
	if err != nil {
		s.logger.Warn("Couldn't reach GitHub as the app to clean up a job", "job", key.String(), "error", err)
	}
	if gh {
		if err := as.DeleteBranch(ctx, key.Repo(), key.Branch()); err != nil {
			s.logger.Warn("Couldn't delete a job's branch", "job", key.String(), "error", err)
		}
	}
	updated := s.update(key.String(), func(j *Job) {
		s.endRunLocked(j, "stopped")
		j.State = state
	})
	if gh {
		s.postCost(ctx, as, key, updated)
	}
	s.logger.Info("Stopped an issue's work", "job", key.String(), "state", state)
	return Response{Job: updated}, nil
}

// postCost writes the job's spend on its pull request (else its issue),
// in the comment it wrote before if there is one.
func (s *Service) postCost(ctx context.Context, as github.As, key host.Key, job Job) {
	if len(job.Runs) == 0 {
		return
	}
	body := costComment(&job, s.config.Location)
	number := key.Issue
	if n, ok := prNumber(job.PRURL); ok {
		number = n
	}
	if job.CostCommentID != 0 {
		if err := as.EditComment(ctx, key.Repo(), job.CostCommentID, body); err == nil {
			return
		}
	}
	id, err := as.Comment(ctx, key.Repo(), number, body)
	if err != nil {
		s.logger.Warn("Couldn't comment a job's spend", "job", key.String(), "error", err)
		return
	}
	s.update(key.String(), func(j *Job) { j.CostCommentID = id })
}

func prNumber(prURL string) (int, bool) {
	_, after, ok := strings.Cut(prURL, "/pull/")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.SplitN(after, "/", 2)[0])
	return n, err == nil && n > 0
}

func (s *Service) comment(ctx context.Context, as github.As, key host.Key, number int, body string) {
	if _, err := as.Comment(ctx, key.Repo(), number, body); err != nil {
		s.logger.Warn("Couldn't comment on GitHub", "job", key.String(), "error", err)
	}
}

// dropOtherAgentLabels removes the agent:* labels but the one of agent,
// so the issue says who works on it.
func (s *Service) dropOtherAgentLabels(ctx context.Context, as github.As, key host.Key, labels []string, agent string) {
	for _, l := range labels {
		if strings.HasPrefix(l, "agent:") && l != "agent:"+agent {
			if err := as.RemoveLabel(ctx, key.Repo(), key.Issue, l); err != nil {
				s.logger.Warn("Couldn't remove a label", "job", key.String(), "label", l, "error", err)
			}
		}
	}
}

// hubLink names where the job opens on the hub.
func (s *Service) hubLink(instanceID string) string {
	app := s.currentApp()
	if app == nil || app.HubURL == "" {
		return ""
	}
	return fmt.Sprintf("在 [hub](%s/) 開啟 `%s` 可以直接對話。", strings.TrimSuffix(app.HubURL, "/"), instanceID)
}

// callLauncher calls h's launcher; an error answer becomes an Error with
// its status and code.
func (s *Service) callLauncher(ctx context.Context, h Host, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	u, err := url.JoinPath(h.URL, "/")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(u, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.config.HTTP.Do(req)
	if err != nil {
		return errorf(http.StatusBadGateway, "host_unreachable", "the machine %s: %v", h.Hostname, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		if code, message := jsonapi.ReadError(data); message != "" {
			return &Error{Status: resp.StatusCode, Code: code, Message: h.Hostname + ": " + message}
		}
		return errorf(http.StatusBadGateway, "host_failed", "the machine %s answered %s", h.Hostname, resp.Status)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
