// Package dispatch is the hub's issue dispatch: it starts work on a GitHub
// issue on a machine's launcher (agentapi host), passes the reviews of its
// pull request to the agent, and stops it when the pull request closes. It
// keeps which machine and AgentAPI work on each issue, what each run cost,
// and the hub's GitHub App, which the launchers get their jobs' tokens
// from. See docs/specs/issue-dispatch.zh-TW.md.
package dispatch

import (
	"slices"
	"time"

	"github.com/coder/agentapi/lib/host"
)

// Action is what a dispatch asks for.
type Action string

const (
	ActionStart  Action = "start"
	ActionSend   Action = "send"
	ActionLinkPR Action = "link_pr"
	ActionStop   Action = "stop"
)

// JobState is where an issue's work is.
type JobState string

const (
	JobStarting JobState = "starting"
	JobWorking  JobState = "working"
	JobReview   JobState = "review"
	JobFailed   JobState = "failed"
	JobDone     JobState = "done"
	// JobStopped is a job whose machine was offline when it was stopped:
	// the machine removes it when it is back.
	JobStopped JobState = "stopped"
)

// ended reports whether a job's work ended; a start begins a new round.
func (s JobState) ended() bool { return s == JobDone || s == JobStopped }

// Request is POST /api/dispatch.
type Request struct {
	Action Action `json:"action" enum:"start,send,link_pr,stop"`
	// DeliveryID makes a dispatch sent again (a workflow run again) count
	// once.
	DeliveryID string `json:"delivery_id,omitempty"`
	Repo       string `json:"repo"`
	Issue      int    `json:"issue"`
	// Agent is claude, codex or pi; empty is the machine's default.
	Agent string `json:"agent,omitempty"`
	// Host picks the machine; empty lets the hub pick.
	Host string `json:"host,omitempty"`
	// Replace, for a start of a job another agent works on, switches
	// agents (the default); false refuses.
	Replace *bool `json:"replace,omitempty"`

	// Message and the rest are a send's: a review or comment, by Author.
	Message      string          `json:"message,omitempty"`
	Author       string          `json:"author,omitempty"`
	ReviewState  string          `json:"review_state,omitempty"`
	Comments     []ReviewComment `json:"comments,omitempty"`
	DispatchMode string          `json:"dispatch_mode,omitempty"`

	// PRURL is a link_pr's pull request.
	PRURL string `json:"pr_url,omitempty"`
}

// ReviewComment is a review's comment on a line.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Body string `json:"body"`
}

// Response is POST /api/dispatch's answer.
type Response struct {
	Job     Job  `json:"job"`
	Created bool `json:"created"`
	// Duplicate is a dispatch whose delivery ID the hub took already.
	Duplicate bool `json:"duplicate,omitempty"`
	// starting is a start the hub took now, which keeps its delivery ID
	// itself.
	starting bool
}

// Host is a machine whose launcher reports to the hub.
type Host struct {
	host.HostInfo
	// Token is what the hub calls the launcher with: kept in
	// dispatch.json (storedHost), never sent.
	Token            string    `json:"-"`
	HeartbeatSeconds int       `json:"heartbeat_seconds"`
	LastSeen         time.Time `json:"last_seen"`
	Stopped          bool      `json:"stopped,omitempty"`
	// Online is whether the launcher was heard from lately, and Active
	// how many jobs it runs: those it reported, or more the hub started on
	// it since. Both are set when the hub sends it.
	Online bool `json:"online"`
	Active int  `json:"active"`
}

// Job is an issue's work.
type Job struct {
	Key           string   `json:"key"`
	Repo          string   `json:"repo"`
	Issue         int      `json:"issue"`
	Title         string   `json:"title,omitempty"`
	Host          string   `json:"host"`
	Agent         string   `json:"agent"`
	InstanceID    string   `json:"instance_id"`
	State         JobState `json:"state" enum:"starting,working,review,failed,done,stopped"`
	Error         string   `json:"error,omitempty"`
	IssueURL      string   `json:"issue_url"`
	PRURL         string   `json:"pr_url,omitempty"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	// Credentials is whether the job's git and gh get the hub's tokens.
	Credentials bool      `json:"credentials"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Runs        []Run     `json:"runs"`
	// Round counts the times work on the issue started anew (after it
	// was done); each round has its pull request and cost comment.
	Round         int   `json:"round"`
	CostCommentID int64 `json:"cost_comment_id,omitempty"`
	// Messages are the last messages the job's agents were sent (send),
	// the oldest first: at most maxMessages, each cut to maxMessageText.
	Messages []Message `json:"messages,omitempty"`
}

// MessageSource is where a message to a job's agent came from.
type MessageSource string

const (
	// SourceHub: someone signed in to the hub, from its Issues page.
	SourceHub MessageSource = "hub"
	// SourceReview: a pull request review, through the workflow.
	SourceReview MessageSource = "review"
	// SourceComment: an /agent comment, through the workflow.
	SourceComment MessageSource = "comment"
)

// Message is a message a job's agent was sent.
type Message struct {
	At     time.Time     `json:"at"`
	Source MessageSource `json:"source" enum:"hub,review,comment"`
	// Author is the GitHub login of a review or comment.
	Author string `json:"author,omitempty"`
	// Agent is the agent it went to.
	Agent string `json:"agent"`
	Text  string `json:"text"`
	// ReviewState is a review's (changes_requested, commented).
	ReviewState string `json:"review_state,omitempty"`
	// Comments counts a review's comments on lines.
	Comments int `json:"comments,omitempty"`
}

// The bounds of the messages a job keeps.
const (
	maxMessages    = 50
	maxMessageText = 2000
)

// addMessage keeps m on the job, the oldest dropped past maxMessages and
// its text cut to maxMessageText characters.
func (j *Job) addMessage(m Message) {
	if r := []rune(m.Text); len(r) > maxMessageText {
		m.Text = string(r[:maxMessageText-1]) + "…"
	}
	j.Messages = append(j.Messages, m)
	if over := len(j.Messages) - maxMessages; over > 0 {
		j.Messages = slices.Delete(j.Messages, 0, over)
	}
}

// working reports whether the job's work goes on: it holds a place on its
// machine.
func (j *Job) working() bool { return !j.State.ended() && j.State != JobFailed }

// clone is a copy of the job that shares nothing with it.
func (j *Job) clone() Job {
	c := *j
	c.Runs = slices.Clone(j.Runs)
	c.Messages = slices.Clone(j.Messages)
	return c
}

// current is the run that works now, if any.
func (j *Job) current() *Run {
	if n := len(j.Runs); n > 0 && j.Runs[n-1].EndedAt.IsZero() {
		return &j.Runs[n-1]
	}
	return nil
}

// Run is one agent's work on a job.
type Run struct {
	// Round is the job's round the run is of.
	Round      int       `json:"round"`
	Agent      string    `json:"agent"`
	Host       string    `json:"host"`
	InstanceID string    `json:"instance_id"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitzero"`
	// EndReason is replaced (another agent took over), stopped or failed.
	EndReason string `json:"end_reason,omitempty"`
	Cost      Cost   `json:"cost"`
	// Missed is whether the hub may have missed a task's cost: it
	// restarted, or an event didn't reach it.
	Missed bool `json:"missed,omitempty"`
}

// Cost is a run's API-equivalent spend, from its tasks' task.completed and
// task.failed events.
type Cost struct {
	USD         float64 `json:"usd"`
	Tokens      Tokens  `json:"tokens"`
	Calls       int     `json:"calls"`
	Tasks       int     `json:"tasks"`
	WorkSeconds float64 `json:"work_seconds"`
}

// Tokens are a cost's tokens.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
}

func (c *Cost) add(o Cost) {
	c.USD += o.USD
	c.Tokens.Input += o.Tokens.Input
	c.Tokens.Output += o.Tokens.Output
	c.Tokens.CacheWrite += o.Tokens.CacheWrite
	c.Tokens.CacheRead += o.Tokens.CacheRead
	c.Calls += o.Calls
	c.Tasks += o.Tasks
	c.WorkSeconds += o.WorkSeconds
}
