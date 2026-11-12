package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/util"
)

// JobState is what a job's AgentAPI is doing, as the launcher sees it.
type JobState string

const (
	// JobStarting is a job whose AgentAPI the launcher started and hasn't
	// seen serve yet.
	JobStarting JobState = "starting"
	// JobRunning is a job whose AgentAPI serves.
	JobRunning JobState = "running"
	// JobRestarting is a job whose AgentAPI exited, which the launcher
	// starts again.
	JobRestarting JobState = "restarting"
	// JobFailed is a job whose AgentAPI exited too often: the launcher
	// leaves it stopped until the job is started again.
	JobFailed JobState = "failed"
	// JobStopping is a job the launcher is stopping.
	JobStopping JobState = "stopping"
)

var JobStateValues = []JobState{JobStarting, JobRunning, JobRestarting, JobFailed, JobStopping}

func (JobState) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "LauncherJobState", JobStateValues)
}

// GitUser is who a job's commits are by.
type GitUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Job is an issue a machine works on: a worktree on the issue's branch and
// the AgentAPI that runs the agent in it.
type Job struct {
	Key   string `json:"key"`
	Agent string `json:"agent"`
	// Run counts the agents that worked on the job here: each run has a
	// state database of its own, since agents can't read each other's
	// sessions.
	Run        int    `json:"run"`
	Port       int    `json:"port"`
	InstanceID string `json:"instance_id"`
	// ServerID is the random identity of this AgentAPI process, learned from
	// /status once it serves. It is empty while a new process starts.
	ServerID      string   `json:"server_id,omitempty"`
	Repo          string   `json:"repo"`
	Worktree      string   `json:"worktree"`
	Branch        string   `json:"branch"`
	DefaultBranch string   `json:"default_branch"`
	State         JobState `json:"state"`
	Error         string   `json:"error,omitempty"`
	// PID is the job's AgentAPI, as its PID file says.
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Prompt is the job's first message, kept to start its AgentAPI
	// again: with the state database it isn't sent twice.
	Prompt  string   `json:"prompt,omitempty"`
	GitUser *GitUser `json:"git_user,omitempty"`
	// Credentials is where its git and gh get a token: hub, file, or
	// empty for the machine's own.
	Credentials string `json:"credentials,omitempty"`
	// Restarts are when the launcher started the AgentAPI again after it
	// exited, within restartWindow; RestartAt, when set, is when it starts
	// it again next (restartDelays).
	Restarts  []time.Time `json:"restarts,omitempty"`
	RestartAt time.Time   `json:"restart_at,omitzero"`

	// since is when its state last changed, for the time it has to serve;
	// done closes when an AgentAPI this launcher started exits.
	since time.Time
	done  chan struct{}
}

// JobInfo is what the launcher tells of a job: its API's answers and its
// reports to the hub, without the first message, git identity and paths
// of the clone it keeps for itself.
type JobInfo struct {
	Key           string    `json:"key"`
	Agent         string    `json:"agent"`
	Run           int       `json:"run"`
	Port          int       `json:"port"`
	InstanceID    string    `json:"instance_id"`
	ServerID      string    `json:"server_id,omitempty"`
	Branch        string    `json:"branch"`
	DefaultBranch string    `json:"default_branch"`
	Worktree      string    `json:"worktree"`
	State         JobState  `json:"state"`
	Error         string    `json:"error,omitempty"`
	Credentials   string    `json:"credentials,omitempty"`
	PID           int       `json:"pid,omitempty"`
	StartedAt     time.Time `json:"started_at"`
}

func (j *Job) info() JobInfo {
	return JobInfo{
		Key: j.Key, Agent: j.Agent, Run: j.Run, Port: j.Port, InstanceID: j.InstanceID, ServerID: j.ServerID, Branch: j.Branch,
		DefaultBranch: j.DefaultBranch, Worktree: j.Worktree, State: j.State, Error: j.Error,
		Credentials: j.Credentials, PID: j.PID, StartedAt: j.StartedAt,
	}
}

// key is the job's Key; a job in the store has a valid one.
func (j *Job) key() Key {
	k, _ := ParseKey(j.Key)
	return k
}

// jobFiles are the paths of a job's files, in its directory.
type jobFiles struct {
	dir string
}

func (c Config) jobFiles(k Key) jobFiles { return jobFiles{dir: filepath.Join(c.JobsDir(), k.Slug())} }

func (f jobFiles) stateDB(run int) string {
	return filepath.Join(f.dir, fmt.Sprintf("state-%d.db", run))
}
func (f jobFiles) pidFile() string { return filepath.Join(f.dir, "agentapi.pid") }
func (f jobFiles) log() string     { return filepath.Join(f.dir, "agentapi.log") }
func (f jobFiles) bin() string     { return filepath.Join(f.dir, "bin") }

// nextRun is the first run without a state database.
func (f jobFiles) nextRun() int {
	run := 1
	for {
		if _, err := os.Stat(f.stateDB(run)); errors.Is(err, os.ErrNotExist) {
			return run
		}
		run++
	}
}

// store is the launcher's jobs, kept in jobs.json.
type store struct {
	path string
	jobs []*Job
}

type storeFile struct {
	Version int    `json:"version"`
	Jobs    []*Job `json:"jobs"`
}

const storeVersion = 1

func loadStore(path string) (*store, error) {
	s := &store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if file.Version != storeVersion {
		return nil, fmt.Errorf("read %s: version %d, this launcher reads %d", path, file.Version, storeVersion)
	}
	for _, job := range file.Jobs {
		if _, err := ParseKey(job.Key); err == nil {
			s.jobs = append(s.jobs, job)
		}
	}
	return s, nil
}

func (s *store) save() error {
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Jobs: s.jobs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(s.path, ".jobs-*", append(data, '\n'))
}

func (s *store) get(key string) *Job {
	for _, job := range s.jobs {
		if job.Key == key {
			return job
		}
	}
	return nil
}

func (s *store) put(job *Job) {
	if i := slices.IndexFunc(s.jobs, func(j *Job) bool { return j.Key == job.Key }); i >= 0 {
		s.jobs[i] = job
		return
	}
	s.jobs = append(s.jobs, job)
}

func (s *store) remove(key string) {
	s.jobs = slices.DeleteFunc(s.jobs, func(j *Job) bool { return j.Key == key })
}
