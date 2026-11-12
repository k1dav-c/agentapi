package host

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/jsonapi"
	"github.com/coder/agentapi/lib/netaddr"
	"github.com/coder/agentapi/lib/procenv"
)

// Timings of the launcher.
const (
	// readyTimeout is how long a job's AgentAPI has to serve.
	readyTimeout = 60 * time.Second
	// stopTimeout is how long a job's AgentAPI has to stop before it is
	// killed.
	stopTimeout = 20 * time.Second
	// checkInterval is how often the launcher checks the jobs' AgentAPIs.
	checkInterval = 5 * time.Second
	// scanInterval is how often it looks for repositories again.
	scanInterval = 5 * time.Minute
	// restartWindow and maxRestarts: a job whose AgentAPI exited after
	// maxRestarts restarts in restartWindow is left failed.
	restartWindow = 30 * time.Minute
	maxRestarts   = 5
)

// restartDelays are the waits before a job's AgentAPI that exited starts
// again, by the restarts it had in restartWindow: one for each of the
// maxRestarts, about eight minutes in all before it fails.
var restartDelays = [maxRestarts]time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second, 2 * time.Minute, 5 * time.Minute}

// Launcher starts and stops the jobs of a machine.
type Launcher struct {
	config Config
	logger *slog.Logger
	// files is the token file's credentials, hub the hub's; each job uses
	// the ones it was started with (Job.Credentials).
	files         Credentials
	hub           *HubCredentials
	runID         string
	hostToken     string
	instanceToken string
	hostname      string
	client        *http.Client
	resolver      *netaddr.Resolver
	// locks serialize what changes a job ("job:<key>") and what changes a
	// clone ("repo:<owner/name>"): a slow clone holds up its repository's
	// jobs, not the machine's.
	locks keyLocks

	// mu guards the fields below and the jobs in store.
	mu    sync.Mutex
	store *store
	// saveDirty is whether the latest jobs.json write failed; check retries
	// it so a transition isn't left only in memory until another change.
	saveDirty bool
	repos     map[string]string
	// reserved are the jobs being started, before they are in the store:
	// they count toward max-jobs, hold their port, and their git gets a
	// token while it clones and fetches.
	reserved map[string]reservation
	now      func() time.Time
}

// reservation is a job being started.
type reservation struct {
	mode  string
	port  int
	agent string
}

// New reads the launcher's jobs and tokens, making the tokens it lacks.
func New(config Config, logger *slog.Logger) (*Launcher, error) {
	if !Supported {
		return nil, errUnsupportedPlatform
	}
	if err := os.MkdirAll(config.JobsDir(), 0o700); err != nil {
		return nil, err
	}
	s, err := loadStore(config.StateFile())
	if err != nil {
		return nil, err
	}
	hostToken, err := readOrMakeToken(config.TokenFile)
	if err != nil {
		return nil, err
	}
	instanceToken, err := readOrMakeToken(config.InstanceTokenFile)
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	return &Launcher{
		config:        config,
		logger:        logger,
		files:         FileCredentials{Path: config.GitHubTokenFile},
		hub:           &HubCredentials{URL: config.HubURL, Secret: config.HubSecret, Hostname: hostname, HostToken: hostToken, Client: &http.Client{Timeout: 30 * time.Second}},
		runID:         randomID(),
		hostToken:     hostToken,
		instanceToken: instanceToken,
		hostname:      hostname,
		client:        &http.Client{Timeout: 10 * time.Second},
		resolver:      &netaddr.Resolver{Logger: logger},
		store:         s,
		repos:         map[string]string{},
		reserved:      map[string]reservation{},
		now:           time.Now,
	}, nil
}

var errUnsupportedPlatform = errors.New("agentapi host runs on Linux and macOS")

// credentialsMode is where a job's git and gh get their token: the hub
// (CredentialsHub) when it asks and the launcher reports to one, else the
// token file if there is one, else none (the machine's own credentials).
func (l *Launcher) credentialsMode(asked string) string {
	switch {
	case asked == CredentialsHub && l.hub.Available():
		return CredentialsHub
	case l.files.Available():
		return credentialsFile
	default:
		return ""
	}
}

// credentialsFile, in Job.Credentials, is the token file's.
const credentialsFile = "file"

// readOrMakeToken reads the token in path, writing a new random one when
// the file is missing or empty.
func readOrMakeToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := atomicfile.Write(path, ".token-*", []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

// Run takes over the jobs the launcher kept, starting again the AgentAPIs
// that stopped, and checks them until ctx ends. The AgentAPIs keep running
// after it returns.
func (l *Launcher) Run(ctx context.Context) {
	l.rescan(ctx)
	l.takeOver()
	go l.reportLoop(ctx)
	check := time.NewTicker(checkInterval)
	defer check.Stop()
	scan := time.NewTicker(scanInterval)
	defer scan.Stop()
	for {
		l.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			l.rescan(ctx)
		case <-check.C:
		}
	}
}

// takeOver adopts the AgentAPIs of the jobs kept that still run and starts
// the others again. It runs before the launcher serves.
func (l *Launcher) takeOver() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, job := range l.store.jobs {
		switch job.State {
		case JobFailed:
			continue
		case JobStopping:
			// The launcher stopped while it stopped the job: its AgentAPI
			// mustn't go on unwatched.
			if alive(job.PID, job.InstanceID) {
				kill(job.PID)
			}
			l.fail(job, errors.New("the launcher stopped while it stopped the job; stop it again"))
			continue
		case JobStarting, JobRunning, JobRestarting:
		}
		if alive(job.PID, job.InstanceID) {
			l.logger.Info("Took over a job", "job", job.Key, "pid", job.PID, "port", job.Port)
			job.State, job.since = JobStarting, l.now()
			continue
		}
		if !job.RestartAt.IsZero() {
			// Waiting to start again after it exited: check does.
			continue
		}
		l.logger.Info("Starting a job's AgentAPI again", "job", job.Key)
		if err := l.launch(job); err != nil {
			l.fail(job, err)
		}
	}
	l.saveLocked()
}

// check moves the jobs whose AgentAPI serves to running, fails those that
// don't serve in time, and starts again those whose AgentAPI exited. A job
// someone starts or stops now is left to them.
func (l *Launcher) check(ctx context.Context) {
	l.mu.Lock()
	if l.saveDirty {
		_ = l.saveLocked()
	}
	jobs := slices.Clone(l.store.jobs)
	l.mu.Unlock()
	for _, job := range jobs {
		unlock, ok := l.locks.tryLock("job:" + job.Key)
		if !ok {
			continue
		}
		l.checkJob(ctx, job)
		unlock()
	}
}

func (l *Launcher) checkJob(ctx context.Context, job *Job) {
	l.mu.Lock()
	state, pid, marker, since := job.State, job.PID, job.InstanceID, job.since
	l.mu.Unlock()
	if state == JobFailed || state == JobStopping {
		return
	}
	if !alive(pid, marker) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.relaunchLocked(job)
		l.saveLocked()
		return
	}
	if state != JobStarting && state != JobRestarting {
		return
	}
	status, err := l.instanceStatus(ctx, job)
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case err == nil && (status.Lifecycle == instanceapi.LifecycleReady || status.Lifecycle == instanceapi.LifecycleRunning):
		job.State, job.Error, job.ServerID = JobRunning, "", status.ServerID
	case l.now().Sub(since) > readyTimeout:
		// Alive, but not serving: a PID taken by another process, or an
		// AgentAPI that hangs. Stopped, it is started again by the next
		// check, up to maxRestarts.
		l.logger.Warn("A job's AgentAPI didn't serve in time; stopping it", "job", job.Key, "pid", pid)
		kill(pid)
		job.since = l.now()
	default:
		return
	}
	l.saveLocked()
}

// relaunchLocked starts job's AgentAPI, which exited, again: once it
// waited restartDelays for the restarts it had lately, and unless it had
// maxRestarts already, when it leaves the job failed. The restarts are
// kept in jobs.json, so a launcher restarted counts them on. l.mu is held.
func (l *Launcher) relaunchLocked(job *Job) {
	now := l.now()
	job.Restarts = slices.DeleteFunc(job.Restarts, func(t time.Time) bool { return now.Sub(t) > restartWindow })
	if job.RestartAt.IsZero() {
		// It just exited.
		if len(job.Restarts) >= maxRestarts {
			l.fail(job, fmt.Errorf("its AgentAPI exited %d times in %s; see %s", len(job.Restarts)+1, restartWindow, l.config.jobFiles(job.key()).log()))
			return
		}
		delay := restartDelays[len(job.Restarts)]
		job.State, job.RestartAt = JobRestarting, now.Add(delay)
		l.logger.Warn("A job's AgentAPI exited; starting it again after a while", "job", job.Key, "pid", job.PID, "delay", delay)
		return
	}
	// The check that set RestartAt comes again a delay later, a moment
	// early: a tick's slack keeps it from waiting one more check.
	if now.Add(100 * time.Millisecond).Before(job.RestartAt) {
		return
	}
	job.RestartAt = time.Time{}
	job.Restarts = append(job.Restarts, now)
	l.logger.Info("Starting a job's AgentAPI again", "job", job.Key, "restart", len(job.Restarts))
	if err := l.launch(job); err != nil {
		l.fail(job, err)
		return
	}
	job.State = JobRestarting
}

// fail leaves job failed with err. l.mu is held.
func (l *Launcher) fail(job *Job, err error) {
	l.logger.Error("A job failed", "job", job.Key, "error", err)
	job.State, job.Error = JobFailed, err.Error()
}

// saveLocked persists the current jobs, marking a failed write for check to
// retry. l.mu is held.
func (l *Launcher) saveLocked() error {
	err := l.store.save()
	l.saveDirty = err != nil
	if err != nil {
		l.logger.Error("Couldn't write the launcher's jobs", "path", l.store.path, "error", err)
	}
	return err
}

func (l *Launcher) rescan(ctx context.Context) {
	repos := ScanRepos(ctx, l.config.Scan, l.config.Workdir)
	l.mu.Lock()
	l.repos = repos
	l.mu.Unlock()
}

// StartRequest is POST /jobs.
type StartRequest struct {
	Repo  string `json:"repo"`
	Issue int    `json:"issue"`
	// Agent is claude, codex, pi or native-demo; empty is the machine's
	// default.
	Agent string `json:"agent,omitempty"`
	// DefaultBranch is the branch a new job's branch starts from, by
	// default the remote's HEAD.
	DefaultBranch string `json:"default_branch,omitempty"`
	// Prompt is the agent's first message.
	Prompt  string   `json:"prompt,omitempty"`
	GitUser *GitUser `json:"git_user,omitempty"`
	// Credentials is CredentialsHub for the job's git and gh to get the
	// hub's token.
	Credentials string `json:"credentials,omitempty"`
}

// gitTimeout bounds the clone or fetch and the worktree of a start.
const gitTimeout = 10 * time.Minute

// StartTimeout bounds a start: its git (gitTimeout), then its AgentAPI
// (readyTimeout). A hub waits that long for POST /jobs.
const StartTimeout = gitTimeout + readyTimeout

// Start starts the job of req.Issue: the worktree on its branch, then its
// AgentAPI. A job that runs already with the same agent is returned as it
// is (created false). A failed one is started again, with req's agent:
// another agent is a new run in the same worktree.
//
// The start goes on when ctx ends (the hub's request timed out, or the
// hub went away), bounded by its own timeouts (StartTimeout): a clone cut
// short would leave half a repository, and an AgentAPI killed as it starts
// a job the hub may yet adopt from the launcher's next report.
func (l *Launcher) Start(ctx context.Context, req StartRequest) (JobInfo, bool, error) {
	ctx = context.WithoutCancel(ctx)
	owner, name, err := ParseRepo(req.Repo)
	if err != nil {
		return JobInfo{}, false, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "%v", err)
	}
	if req.Issue <= 0 {
		return JobInfo{}, false, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "issue is a positive number")
	}
	key := Key{Owner: owner, Name: name, Issue: req.Issue}
	agent := cmp.Or(req.Agent, l.config.Agent)
	if !slices.Contains(l.config.Agents, agent) {
		return JobInfo{}, false, jsonapi.Errorf(http.StatusUnprocessableEntity, "agent_unavailable", "this machine doesn't run %q (it runs %v)", agent, l.config.Agents)
	}
	defer l.locks.lock("job:" + key.String())()

	l.mu.Lock()
	existing := l.store.get(key.String())
	l.mu.Unlock()
	if existing != nil {
		return l.restart(ctx, existing, agent, req)
	}

	mode := l.credentialsMode(req.Credentials)
	port, release, err := l.reserve(key, mode, agent)
	if err != nil {
		return JobInfo{}, false, err
	}
	defer release()
	gitCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	repoDir, worktree, base, err := l.prepare(gitCtx, key, req, mode)
	if err != nil {
		return JobInfo{}, false, err
	}
	files := l.config.jobFiles(key)
	if err := os.MkdirAll(files.dir, 0o700); err != nil {
		return JobInfo{}, false, err
	}
	job := &Job{
		Key:           key.String(),
		Agent:         agent,
		Run:           files.nextRun(),
		Port:          port,
		InstanceID:    l.hostname + "~" + key.Slug(),
		Repo:          repoDir,
		Worktree:      worktree,
		Branch:        key.Branch(),
		DefaultBranch: base,
		State:         JobStarting,
		StartedAt:     l.now(),
		Prompt:        req.Prompt,
		GitUser:       req.GitUser,
		Credentials:   mode,
	}
	if err := l.writeGHShim(key, mode); err != nil {
		return JobInfo{}, false, err
	}
	l.mu.Lock()
	l.store.put(job)
	err = l.launch(job)
	if err == nil {
		// A live child whose PID was never saved would be untracked after a
		// launcher restart. Treat this like any other start failure and stop
		// it in failStart; a successful retry there keeps the failed record.
		err = l.saveLocked()
	} else {
		_ = l.saveLocked()
	}
	l.mu.Unlock()
	if err == nil {
		err = l.waitReady(ctx, job)
	}
	if err != nil {
		return l.failStart(job, err)
	}
	l.logger.Info("Started a job", "job", job.Key, "agent", agent, "port", port, "worktree", worktree)
	return l.info(job), true, nil
}

// restart answers a start of a job the launcher has: the one that runs, or
// a failed one started again. l.locks holds the job.
func (l *Launcher) restart(ctx context.Context, job *Job, agent string, req StartRequest) (JobInfo, bool, error) {
	l.mu.Lock()
	state, current := job.State, job.Agent
	l.mu.Unlock()
	if state != JobFailed {
		if current != agent {
			return JobInfo{}, false, jsonapi.Errorf(http.StatusConflict, "job_active",
				"%s runs %s here; stop it, keeping the worktree, to switch to %s", job.Key, current, agent)
		}
		return l.info(job), false, nil
	}
	l.mu.Lock()
	if l.activeLocked() >= l.config.MaxJobs {
		n := l.activeLocked()
		l.mu.Unlock()
		return JobInfo{}, false, jsonapi.Errorf(http.StatusConflict, "capacity", "this machine runs %d jobs, its most", n)
	}
	// An AgentAPI the launcher lost track of mustn't hold the port.
	if alive(job.PID, job.InstanceID) {
		kill(job.PID)
	}
	l.mu.Unlock()
	port := job.Port
	if !portFree(port) {
		var err error
		if port, err = l.freePort(); err != nil {
			return JobInfo{}, false, err
		}
	}
	mode := l.credentialsMode(req.Credentials)
	if err := l.writeGHShim(job.key(), mode); err != nil {
		return JobInfo{}, false, err
	}
	l.mu.Lock()
	job.Port, job.Restarts, job.RestartAt, job.Error = port, nil, time.Time{}, ""
	job.Credentials = mode
	if req.GitUser != nil {
		job.GitUser = req.GitUser
	}
	if agent != job.Agent {
		// Another agent: a run of its own, with its first message.
		job.Agent, job.Run, job.Prompt = agent, l.config.jobFiles(job.key()).nextRun(), req.Prompt
	}
	job.State, job.since = JobStarting, l.now()
	err := l.launch(job)
	if err == nil {
		err = l.saveLocked()
	} else {
		_ = l.saveLocked()
	}
	l.mu.Unlock()
	if err == nil {
		err = l.waitReady(ctx, job)
	}
	if err != nil {
		return l.failStart(job, err)
	}
	l.logger.Info("Started a failed job again", "job", job.Key, "agent", agent, "port", port)
	return l.info(job), false, nil
}

// activeLocked is how many jobs run or are being started. l.mu is held.
func (l *Launcher) activeLocked() int {
	n := len(l.reserved)
	for _, job := range l.store.jobs {
		if job.State != JobFailed {
			n++
		}
	}
	return n
}

// reserve holds a place and a port for key while it is prepared, and lets
// its git get a token; release lets them go.
func (l *Launcher) reserve(key Key, mode, agent string) (port int, release func(), err error) {
	l.mu.Lock()
	if n := l.activeLocked(); n >= l.config.MaxJobs {
		l.mu.Unlock()
		return 0, nil, jsonapi.Errorf(http.StatusConflict, "capacity", "this machine runs %d jobs, its most", n)
	}
	l.reserved[key.String()] = reservation{mode: mode, agent: agent}
	l.mu.Unlock()
	release = func() {
		l.mu.Lock()
		delete(l.reserved, key.String())
		l.mu.Unlock()
	}
	if port, err = l.freePort(); err != nil {
		release()
		return 0, nil, err
	}
	l.mu.Lock()
	l.reserved[key.String()] = reservation{mode: mode, port: port, agent: agent}
	l.mu.Unlock()
	return port, release, nil
}

// prepare gets key's repository up to date and its worktree on the job's
// branch, one job of a repository at a time.
func (l *Launcher) prepare(ctx context.Context, key Key, req StartRequest, mode string) (repoDir, worktree, base string, err error) {
	defer l.locks.lock("repo:" + key.Repo())()
	repoDir, err = l.repoDir(ctx, key, mode)
	if err != nil {
		return "", "", "", err
	}
	env := l.gitEnv(key, req.GitUser, mode)
	if _, err := git(ctx, repoDir, env, "fetch", "--quiet", "origin"); err != nil {
		return "", "", "", jsonapi.Errorf(http.StatusBadGateway, "git_failed", "%v", err)
	}
	base = cmp.Or(req.DefaultBranch, defaultBranch(ctx, repoDir))
	worktree = filepath.Join(repoDir+worktreesSuffix, strconv.Itoa(key.Issue))
	if err := ensureWorktree(ctx, repoDir, worktree, key.Branch(), base, env); err != nil {
		return "", "", "", err
	}
	return repoDir, worktree, base, nil
}

func (l *Launcher) failStart(job *Job, err error) (JobInfo, bool, error) {
	l.mu.Lock()
	if alive(job.PID, job.InstanceID) {
		kill(job.PID)
	}
	l.fail(job, err)
	l.saveLocked()
	l.mu.Unlock()
	return JobInfo{}, false, jsonapi.Errorf(http.StatusInternalServerError, "start_failed", "start %s: %v", job.Key, err)
}

func (l *Launcher) info(job *Job) JobInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return job.info()
}

// repoDir finds key's repository, cloning it when the launcher may. A
// clone goes to a directory of its own first, so one cut short leaves
// nothing behind.
func (l *Launcher) repoDir(ctx context.Context, key Key, mode string) (string, error) {
	l.mu.Lock()
	dir, ok := l.repos[key.Repo()]
	l.mu.Unlock()
	if ok {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
	}
	l.rescan(ctx)
	l.mu.Lock()
	dir, ok = l.repos[key.Repo()]
	l.mu.Unlock()
	if ok {
		return dir, nil
	}
	if !l.config.CloneMissing() {
		return "", jsonapi.Errorf(http.StatusUnprocessableEntity, "repo_missing", "this machine has no clone of %s", key.Repo())
	}
	dir = filepath.Join(l.config.Workdir, key.Owner, key.Name)
	if _, err := os.Stat(dir); err == nil {
		return "", jsonapi.Errorf(http.StatusConflict, "repo_conflict", "%s exists but isn't a clone of %s", dir, key.Repo())
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "."+key.Name+".clone-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	remote := "git@github.com:" + key.Repo() + ".git"
	if mode != "" {
		remote = "https://github.com/" + key.Repo() + ".git"
	}
	l.logger.Info("Cloning a repository", "repo", key.Repo(), "dir", dir)
	if _, err := git(ctx, tmp, l.gitEnv(key, nil, mode), "clone", "--quiet", remote, "."); err != nil {
		return "", jsonapi.Errorf(http.StatusBadGateway, "clone_failed", "%v", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	l.mu.Lock()
	l.repos[key.Repo()] = dir
	l.mu.Unlock()
	return dir, nil
}

// defaultBranch is the branch origin's HEAD names, else main.
func defaultBranch(ctx context.Context, repoDir string) string {
	ref, err := git(ctx, repoDir, nil, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		if branch, ok := strings.CutPrefix(ref, "origin/"); ok && branch != "" {
			return branch
		}
	}
	return "main"
}

// ensureWorktree puts a worktree of branch at path: the one there, a local
// or remote branch of that name, or a new branch from origin's base.
func ensureWorktree(ctx context.Context, repoDir, path, branch, base string, env []string) error {
	if _, err := os.Stat(path); err == nil {
		head, err := git(ctx, path, nil, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || head != branch {
			return jsonapi.Errorf(http.StatusConflict, "worktree_conflict", "%s exists and isn't a worktree on %s", path, branch)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, _ = git(ctx, repoDir, nil, "worktree", "prune")
	has := func(ref string) bool {
		_, err := git(ctx, repoDir, nil, "rev-parse", "--verify", "--quiet", ref)
		return err == nil
	}
	var args []string
	switch {
	case has("refs/heads/" + branch):
		args = []string{"worktree", "add", "--quiet", path, branch}
	case has("refs/remotes/origin/" + branch):
		args = []string{"worktree", "add", "--quiet", "-b", branch, path, "origin/" + branch}
	case has("refs/remotes/origin/" + base):
		args = []string{"worktree", "add", "--quiet", "--no-track", "-b", branch, path, "origin/" + base}
	default:
		return jsonapi.Errorf(http.StatusUnprocessableEntity, "no_base_branch", "origin has no branch %s to start %s from", base, branch)
	}
	if _, err := git(ctx, repoDir, env, args...); err != nil {
		return jsonapi.Errorf(http.StatusConflict, "worktree_failed", "%v", err)
	}
	return nil
}

// freePort is the first port of the range no job uses or holds that this
// machine can listen on.
func (l *Launcher) freePort() (int, error) {
	l.mu.Lock()
	used := map[int]bool{}
	for _, job := range l.store.jobs {
		used[job.Port] = true
	}
	for _, r := range l.reserved {
		used[r.port] = true
	}
	l.mu.Unlock()
	for port := l.config.firstPort; port <= l.config.lastPort; port++ {
		if !used[port] && portFree(port) {
			return port, nil
		}
	}
	return 0, jsonapi.Errorf(http.StatusConflict, "no_port", "no port of %d-%d is free", l.config.firstPort, l.config.lastPort)
}

// portFree reports whether this machine can listen on port.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// launch starts job's AgentAPI. l.mu is held.
func (l *Launcher) launch(job *Job) error {
	key := job.key()
	files := l.config.jobFiles(key)
	if err := os.MkdirAll(files.dir, 0o700); err != nil {
		return err
	}
	// The server refuses a PID file a running server wrote.
	_ = os.Remove(files.pidFile())
	command, ok := l.config.AgentCommands[job.Agent]
	if !ok {
		return fmt.Errorf("agent %q has no command", job.Agent)
	}
	args := []string{
		"server",
		"--port", strconv.Itoa(job.Port),
		"--type", job.Agent,
		"--name", key.String(),
		"--instance-id", job.InstanceID,
		"--state-db", files.stateDB(job.Run),
		"--pid-file", files.pidFile(),
	}
	if l.config.Path != "" {
		args = append(args, "--config", l.config.Path)
	}
	if job.Prompt != "" {
		args = append(args, "--initial-prompt", job.Prompt)
	}
	args = append(append(args, "--"), command...)
	logFile, err := os.OpenFile(files.log(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(l.config.AgentAPI, args...)
	cmd.Dir = job.Worktree
	cmd.Env = l.jobEnv(key, job.GitUser, job.Credentials)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// A freshly started process has a new random identity until /status says
	// what it is; never carry the previous process's into this run.
	job.ServerID = ""
	if err := startDetached(cmd); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start %s: %w", l.config.AgentAPI, err)
	}
	done := make(chan struct{})
	job.PID, job.done, job.since = cmd.Process.Pid, done, l.now()
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		close(done)
	}()
	return nil
}

// waitReady waits for job's AgentAPI to serve.
func (l *Launcher) waitReady(ctx context.Context, job *Job) error {
	l.mu.Lock()
	done := job.done
	l.mu.Unlock()
	deadline := time.NewTimer(readyTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		status, err := l.instanceStatus(ctx, job)
		if err == nil {
			switch status.Lifecycle {
			case instanceapi.LifecycleReady, instanceapi.LifecycleRunning:
				l.mu.Lock()
				job.State, job.Error, job.ServerID = JobRunning, "", status.ServerID
				_ = l.saveLocked()
				l.mu.Unlock()
				return nil
			case instanceapi.LifecycleExited, instanceapi.LifecycleFailed:
				return fmt.Errorf("the agent %s: %s", status.Lifecycle, l.logTail(job))
			case instanceapi.LifecycleStarting, instanceapi.LifecycleRestarting:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("its AgentAPI exited: %s", l.logTail(job))
		case <-deadline.C:
			return fmt.Errorf("its AgentAPI didn't serve in %s: %s", readyTimeout, l.logTail(job))
		case <-tick.C:
		}
	}
}

// logTail is the end of job's AgentAPI log, for an error.
func (l *Launcher) logTail(job *Job) string {
	data, err := os.ReadFile(l.config.jobFiles(job.key()).log())
	if err != nil {
		return "no log"
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, " | ")
}

// instanceStatus is what the launcher needs of GET /status: the lifecycle,
// and the random identity that distinguishes this process from the previous
// one with the same instance ID.
type instanceStatus struct {
	Lifecycle instanceapi.LifecycleState `json:"lifecycle"`
	ServerID  string                     `json:"server_id"`
}

// instanceStatus returns what GET /status of job's AgentAPI says.
func (l *Launcher) instanceStatus(ctx context.Context, job *Job) (instanceStatus, error) {
	resp, err := l.instanceRequest(ctx, job, http.MethodGet, "/status", nil)
	if err != nil {
		return instanceStatus{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return instanceStatus{}, fmt.Errorf("GET /status: %s", resp.Status)
	}
	var status instanceStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return instanceStatus{}, err
	}
	return status, nil
}

func (l *Launcher) instanceRequest(ctx context.Context, job *Job, method, path string, body []byte) (*http.Response, error) {
	l.mu.Lock()
	port := job.Port
	l.mu.Unlock()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:"+strconv.Itoa(port)+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+l.instanceToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return l.client.Do(req)
}

// MessageRequest is POST /jobs/{key}/message.
type MessageRequest struct {
	Content string `json:"content"`
	// DispatchMode is queue (the default), steer or interrupt.
	DispatchMode string `json:"dispatch_mode,omitempty"`
}

// Message sends a user message to the job's agent, returning its
// AgentAPI's answer.
func (l *Launcher) Message(ctx context.Context, key Key, req MessageRequest) (int, []byte, error) {
	if strings.TrimSpace(req.Content) == "" {
		return 0, nil, jsonapi.Errorf(http.StatusBadRequest, "bad_request", "content is empty")
	}
	l.mu.Lock()
	job := l.store.get(key.String())
	var state JobState
	if job != nil {
		state = job.State
	}
	l.mu.Unlock()
	if job == nil {
		return 0, nil, jsonapi.Errorf(http.StatusNotFound, "job_not_found", "no job %s on this machine", key)
	}
	if state == JobFailed || state == JobStopping {
		return 0, nil, jsonapi.Errorf(http.StatusConflict, "job_not_running", "job %s is %s", key, state)
	}
	payload := map[string]string{"content": req.Content, "type": "user"}
	if req.DispatchMode != "" {
		payload["dispatch_mode"] = req.DispatchMode
	}
	body, _ := json.Marshal(payload)
	resp, err := l.instanceRequest(ctx, job, http.MethodPost, "/message", body)
	if err != nil {
		return 0, nil, jsonapi.Errorf(http.StatusBadGateway, "instance_unreachable", "job %s: %v", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, answer, err
}

// StopOptions are DELETE /jobs/{key}'s.
type StopOptions struct {
	// KeepWorktree keeps the worktree and the branch, to switch agents.
	KeepWorktree bool
	// KeepState keeps the job's state databases.
	KeepState bool
}

// Stop stops the job's AgentAPI and removes what the job left.
func (l *Launcher) Stop(ctx context.Context, key Key, opts StopOptions) error {
	defer l.locks.lock("job:" + key.String())()
	l.mu.Lock()
	job := l.store.get(key.String())
	if job == nil {
		l.mu.Unlock()
		return jsonapi.Errorf(http.StatusNotFound, "job_not_found", "no job %s on this machine", key)
	}
	job.State = JobStopping
	pid, marker := job.PID, job.InstanceID
	repo, worktree, branch := job.Repo, job.Worktree, job.Branch
	l.saveLocked()
	l.mu.Unlock()

	l.stopProcess(key, pid, marker)
	var errs []error
	if !opts.KeepWorktree {
		errs = l.removeWorktree(ctx, key, repo, worktree, branch)
	}
	files := l.config.jobFiles(key)
	if opts.KeepState {
		_ = os.RemoveAll(files.bin())
		_ = os.Remove(files.pidFile())
	} else if err := os.RemoveAll(files.dir); err != nil {
		errs = append(errs, err)
	}
	l.mu.Lock()
	l.store.remove(key.String())
	l.saveLocked()
	l.mu.Unlock()
	l.logger.Info("Stopped a job", "job", key.String(), "kept_worktree", opts.KeepWorktree, "kept_state", opts.KeepState)
	if err := errors.Join(errs...); err != nil {
		return jsonapi.Errorf(http.StatusInternalServerError, "cleanup_failed", "stopped %s, but: %v", key, err)
	}
	return nil
}

// stopProcess stops a job's AgentAPI, pid if marker still names it: it
// asks, then kills it after stopTimeout.
func (l *Launcher) stopProcess(key Key, pid int, marker string) {
	if !alive(pid, marker) {
		return
	}
	if err := terminate(pid); err != nil {
		l.logger.Warn("Couldn't stop a job's AgentAPI", "job", key.String(), "pid", pid, "error", err)
	}
	deadline := l.now().Add(stopTimeout)
	for alive(pid, marker) && l.now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if alive(pid, marker) {
		l.logger.Warn("A job's AgentAPI didn't stop; killing it", "job", key.String(), "pid", pid)
		kill(pid)
	}
}

// removeWorktree removes a job's worktree and its local branch from repo.
func (l *Launcher) removeWorktree(ctx context.Context, key Key, repo, worktree, branch string) []error {
	defer l.locks.lock("repo:" + key.Repo())()
	var errs []error
	if _, err := os.Stat(worktree); err == nil {
		if _, err := git(ctx, repo, nil, "worktree", "remove", "--force", worktree); err != nil {
			errs = append(errs, err)
		}
	}
	_, _ = git(ctx, repo, nil, "worktree", "prune")
	if _, err := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if _, err := git(ctx, repo, nil, "branch", "--quiet", "-D", branch); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Jobs are the launcher's jobs, with those being prepared (a clone, a
// fetch) as starting, without an AgentAPI yet: the hub waits for a start
// it lost the answer of while the launcher reports it.
func (l *Launcher) Jobs() []JobInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]JobInfo, 0, len(l.store.jobs)+len(l.reserved))
	for _, job := range l.store.jobs {
		out = append(out, job.info())
	}
	for _, key := range slices.Sorted(maps.Keys(l.reserved)) {
		if l.store.get(key) == nil {
			out = append(out, JobInfo{Key: key, Agent: l.reserved[key].agent, State: JobStarting})
		}
	}
	return out
}

// HostInfo is GET /host.
type HostInfo struct {
	Hostname        string   `json:"hostname"`
	URL             string   `json:"url"`
	AgentAPIVersion string   `json:"agentapi_version"`
	Repos           []string `json:"repos"`
	Agents          []string `json:"agents"`
	DefaultAgent    string   `json:"default_agent"`
	MaxJobs         int      `json:"max_jobs"`
	Clone           bool     `json:"clone"`
	// CredentialsAvailable is whether the token file has a token;
	// HubCredentials whether the launcher can get the hub's.
	CredentialsAvailable bool      `json:"credentials_available"`
	HubCredentials       bool      `json:"hub_credentials"`
	Jobs                 []JobInfo `json:"jobs"`
}

// Info is what the launcher tells the hub of the machine.
func (l *Launcher) Info(ctx context.Context) HostInfo {
	l.mu.Lock()
	repos := slices.Sorted(maps.Keys(l.repos))
	l.mu.Unlock()
	jobs := l.Jobs()
	return HostInfo{
		Hostname:             l.hostname,
		URL:                  l.PublicURL(ctx),
		AgentAPIVersion:      version.Version,
		Repos:                repos,
		Agents:               l.config.Agents,
		DefaultAgent:         l.config.Agent,
		MaxJobs:              l.config.MaxJobs,
		Clone:                l.config.CloneMissing(),
		CredentialsAvailable: l.files.Available(),
		HubCredentials:       l.hub.Available(),
		Jobs:                 jobs,
	}
}

// PublicURL is the URL the hub reaches the launcher at.
func (l *Launcher) PublicURL(ctx context.Context) string {
	u := l.config.PublicURL
	if u == "" {
		u = "http://{hostname}:{port}"
		if l.config.HubURL != "" {
			u = "http://{ip}:{port}"
		}
	}
	u = instanceapi.ExpandPlaceholders(u, l.config.Port)
	if instanceapi.HasIP(u) {
		ip, err := l.resolver.ResolveContext(ctx, netaddr.Target(l.config.HubURL))
		if err != nil || ip == "" {
			ip = l.hostname
		}
		u = instanceapi.ExpandIP(u, ip)
	}
	return u
}

// gitEnv is the environment of git for key: the launcher's own git and,
// through jobEnv, the job's. With credentials, github.com goes through
// HTTPS and the launcher's credential helper only, SSH remotes included.
func (l *Launcher) gitEnv(key Key, user *GitUser, mode string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		// The GIT_CONFIG_COUNT entries of the environment the launcher runs
		// in; GIT_CONFIG_GLOBAL and the like stay.
		if name == "GIT_CONFIG_COUNT" || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") || name == EnvJob {
			return true
		}
		return user != nil && (strings.HasPrefix(name, "GIT_AUTHOR_") || strings.HasPrefix(name, "GIT_COMMITTER_"))
	})
	env = append(env, EnvJob+"="+key.Slug(), "GIT_TERMINAL_PROMPT=0")
	var config [][2]string
	if mode != "" {
		helper := "!" + shellQuote(l.config.AgentAPI) + " host git-credential --socket " + shellQuote(l.config.SocketPath())
		config = append(config,
			// An empty helper drops the ones configured before, such as a
			// stored personal token.
			[2]string{"credential.https://github.com.helper", ""},
			[2]string{"credential.https://github.com.helper", helper},
			// The helper gets the repository's path, and answers for the
			// job's repository alone.
			[2]string{"credential.https://github.com.useHttpPath", "true"},
			[2]string{"url.https://github.com/.insteadOf", "git@github.com:"},
			[2]string{"url.https://github.com/.insteadOf", "ssh://git@github.com/"},
		)
	}
	if user != nil {
		config = append(config, [2]string{"user.name", user.Name}, [2]string{"user.email", user.Email})
		env = append(env,
			"GIT_AUTHOR_NAME="+user.Name, "GIT_AUTHOR_EMAIL="+user.Email,
			"GIT_COMMITTER_NAME="+user.Name, "GIT_COMMITTER_EMAIL="+user.Email)
	}
	if len(config) > 0 {
		env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(config)))
		for i, kv := range config {
			env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
		}
	}
	return env
}

// jobEnv is the environment of job's AgentAPI: none of AgentAPI's
// variables of the shell the launcher runs in (a PID file of another
// AgentAPI would be overwritten) nor of an agent session, the job's API
// token, its git setup and gh's shim first on PATH.
func (l *Launcher) jobEnv(key Key, user *GitUser, mode string) []string {
	env := procenv.ForAgent(procenv.WithoutAgentAPI(l.gitEnv(key, user, mode)))
	env = append(env, EnvJob+"="+key.Slug(), "AGENTAPI_API_TOKEN="+l.instanceToken)
	if l.config.reportsToHub() {
		// The hub adds up each job's cost from its task events.
		env = append(env, "AGENTAPI_WEBHOOK_EVENTS="+strings.Join(withTaskEvents(l.config.HubEvents), " "))
	}
	if _, err := os.Stat(filepath.Join(l.config.jobFiles(key).bin(), "gh")); err == nil {
		for i, kv := range env {
			if path, ok := strings.CutPrefix(kv, "PATH="); ok {
				env[i] = "PATH=" + l.config.jobFiles(key).bin() + string(os.PathListSeparator) + path
			}
		}
	}
	return env
}

// writeGHShim puts a gh in the job's bin that runs the real one with the
// job's token, when there are credentials and gh is installed.
func (l *Launcher) writeGHShim(key Key, mode string) error {
	bin := l.config.jobFiles(key).bin()
	_ = os.RemoveAll(bin)
	if mode == "" {
		return nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	if gh, err = filepath.Abs(gh); err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	script := "#!/bin/sh\n# Written by agentapi host: gh with this job's GitHub token.\n" +
		"token=$(" + shellQuote(l.config.AgentAPI) + " host gh-token --socket " + shellQuote(l.config.SocketPath()) + ") || exit 1\n" +
		"GH_TOKEN=\"$token\" exec " + shellQuote(gh) + " \"$@\"\n"
	return os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o700)
}

// token is the GitHub token of a job that runs here, or is being
// started, for its git and gh. caller is the process that asks, when the
// platform tells (0 when it doesn't): it must run in the job's AgentAPI,
// or be the launcher's own git while it starts the job, so an agent of
// another job can't take this one's token.
func (l *Launcher) token(ctx context.Context, key Key, caller int) (Token, error) {
	l.mu.Lock()
	var mode string
	owner := 0
	if job := l.store.get(key.String()); job != nil && job.State != JobFailed && job.State != JobStopping {
		mode, owner = job.Credentials, job.PID
	} else if r, ok := l.reserved[key.String()]; ok {
		mode, owner = r.mode, os.Getpid()
	} else {
		l.mu.Unlock()
		return Token{}, jsonapi.Errorf(http.StatusNotFound, "job_not_found", "no job %s runs on this machine", key)
	}
	l.mu.Unlock()
	if caller != 0 && !descendsFrom(caller, owner) {
		return Token{}, jsonapi.Errorf(http.StatusForbidden, "not_the_job", "process %d doesn't run in job %s", caller, key)
	}
	var creds Credentials
	switch mode {
	case CredentialsHub:
		creds = l.hub
	case credentialsFile:
		creds = l.files
	default:
		return Token{}, jsonapi.Errorf(http.StatusServiceUnavailable, "no_credentials", "job %s uses this machine's own GitHub credentials", key)
	}
	token, err := creds.Token(ctx, key)
	if err != nil {
		return Token{}, jsonapi.Errorf(http.StatusBadGateway, "token_failed", "%v", err)
	}
	return token, nil
}

// withTaskEvents are the config file's webhook events with the task
// events and the instance events the hub needs.
func withTaskEvents(events []string) []string {
	if len(events) == 0 {
		events = []string{"instance.*"}
	}
	out := slices.Clone(events)
	for _, need := range []string{"instance.*", "task.*"} {
		if !slices.Contains(out, need) && !slices.Contains(out, "*") {
			out = append(out, need)
		}
	}
	return out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
