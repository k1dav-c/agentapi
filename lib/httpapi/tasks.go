package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/instanceapi"
)

// Tasks are part of the summary's contract (lib/instanceapi).
type (
	Task       = instanceapi.Task
	TaskStatus = instanceapi.TaskStatus
)

const (
	TaskStatusRunning   = instanceapi.TaskStatusRunning
	TaskStatusCompleted = instanceapi.TaskStatusCompleted
	TaskStatusFailed    = instanceapi.TaskStatusFailed
	TaskStatusLimited   = instanceapi.TaskStatusLimited
)

var TaskStatusValues = instanceapi.TaskStatusValues

// taskPromptLimit is how much of the prompt a task carries; the message
// has all of it.
const taskPromptLimit = 1 << 10

// taskTracker derives the tasks from the changes of the conversation. The
// emitter calls it with its lock held; each method returns the positions of
// the tasks it changed, to send as updates.
type taskTracker struct {
	tasks []Task
	// owners has the position of the task each user message started or
	// was added to, by the message's ID.
	owners map[int]int
	// ids are the IDs of the messages last taken. inOrder: they and the
	// tasks' IDs increase, so a change goes through only the messages from
	// where it starts. rederive: the tasks weren't derived from the messages
	// (they were restored), so the next change goes through them all.
	ids      []int
	inOrder  bool
	rederive bool
	// open is the position of the running task, or -1.
	open int
	// ran: the agent was running since the open task started, so its being
	// stable again ends the task.
	ran bool
	// since: user messages from before it are history (a restored or
	// resumed conversation), whose tasks are over.
	since  time.Time
	status StatusChangeBody
}

func newTaskTracker(since time.Time) *taskTracker {
	return &taskTracker{open: -1, since: since, owners: map[int]int{}, inOrder: true}
}

// reset forgets the tasks, for a new conversation that starts at now.
func (t *taskTracker) reset(now time.Time) {
	t.tasks, t.open, t.ran, t.since = nil, -1, false, now
	t.ids, t.inOrder, t.rederive = t.ids[:0], true, false
	clear(t.owners)
}

// restore takes back the tasks of a restored conversation, whose messages
// are msgs. A task whose message is gone, or is another one now, goes: an
// AgentAPI from before tasks were kept clears none when it resets or takes
// messages back, and a message row the state database can't read is
// skipped. One that was running when AgentAPI stopped can't go on.
func (t *taskTracker) restore(tasks []Task, msgs []conv.Message, now time.Time) []int {
	at := make(map[int]int, len(msgs))
	for i, msg := range msgs {
		at[msg.Id] = i
	}
	t.tasks = slices.DeleteFunc(tasks, func(task Task) bool {
		i, ok := at[task.ID]
		return !ok || msgs[i].Role != conv.RoleUser || !msgs[i].Time.Equal(task.StartedAt)
	})
	t.open, t.ran = -1, false
	t.ids, t.rederive = t.ids[:0], true
	t.index()
	var changed []int
	for i := range t.tasks {
		if !t.tasks[i].Final() {
			t.end(i, now)
			changed = append(changed, i)
		}
	}
	return changed
}

// index works out owners, and whether the tasks are in order, from the
// tasks.
func (t *taskTracker) index() {
	t.owners = make(map[int]int, len(t.tasks))
	t.inOrder = true
	for i, task := range t.tasks {
		if i > 0 && t.tasks[i-1].ID >= task.ID {
			t.inOrder = false
		}
		// The latest task that holds a message owns it.
		t.owners[task.ID] = i
		for _, id := range task.Steered {
			t.owners[id] = i
		}
	}
}

// owner returns the position of the task a user message started or was
// added to, or -1.
func (t *taskTracker) owner(id int) int {
	if i, ok := t.owners[id]; ok {
		return i
	}
	return -1
}

// messages takes the conversation's messages after a change; msgs[:from]
// are those of the last call, unchanged. Only the tasks of the messages
// from there on change, which while a reply streams in is the latest: an
// update costs as much in a long conversation as in a short one.
func (t *taskTracker) messages(msgs []conv.Message, from int) []int {
	if t.rederive || !t.inOrder {
		return t.derive(msgs)
	}
	from = min(from, len(t.ids), len(msgs))
	if from == len(msgs) && from == len(t.ids) {
		return nil
	}
	last := math.MinInt // the ID of the last message before from
	if from > 0 {
		last = msgs[from-1].Id
	}
	// A change that removed messages, put others in their place, added
	// them out of order or took a task's message from the user goes
	// through them all.
	if len(msgs) < len(t.ids) {
		return t.derive(msgs)
	}
	for i := from; i < len(msgs); i++ {
		id := msgs[i].Id
		if i < len(t.ids) && id != t.ids[i] || i >= len(t.ids) && i > 0 && id <= msgs[i-1].Id {
			return t.derive(msgs)
		}
		if p, ok := t.owners[id]; ok && t.tasks[p].ID == id && (msgs[i].Role != conv.RoleUser || !msgs[i].Time.Equal(t.tasks[p].StartedAt)) {
			return t.derive(msgs)
		}
	}
	at := make(map[int]int, len(msgs)-from+1)
	for i := from; i < len(msgs); i++ {
		at[msgs[i].Id] = i
	}
	// k is the task the messages before from end in, the first whose reply
	// can change; the tasks after it started from there on.
	k := len(t.tasks)
	for k > 0 && t.tasks[k-1].ID > last {
		k--
	}
	if k > 0 {
		k--
		j := from - 1
		for j >= 0 && msgs[j].Id != t.tasks[k].ID {
			j--
		}
		if j < 0 {
			return t.derive(msgs)
		}
		at[t.tasks[k].ID] = j
	}
	changed := t.own(msgs[from:], nil)
	changed = t.replies(msgs, k, at, changed)
	for _, msg := range msgs[len(t.ids):] {
		t.ids = append(t.ids, msg.Id)
	}
	return changed
}

// derive goes through all the messages: it drops the tasks whose message is
// gone, and adds and updates the others.
func (t *taskTracker) derive(msgs []conv.Message) []int {
	at := make(map[int]int, len(msgs))
	for i, msg := range msgs {
		at[msg.Id] = i
	}
	// Tasks whose message was taken back go too (the emitter resyncs
	// clients then).
	var changed []int
	if n := len(t.tasks); n > 0 {
		kept := t.tasks[:0]
		for _, task := range t.tasks {
			if i, ok := at[task.ID]; ok && msgs[i].Role == conv.RoleUser && msgs[i].Time.Equal(task.StartedAt) {
				steered := len(task.Steered)
				task.Steered = slices.DeleteFunc(task.Steered, func(id int) bool {
					_, ok := at[id]
					return !ok
				})
				if len(task.Steered) != steered {
					changed = append(changed, len(kept))
				}
				kept = append(kept, task)
			}
		}
		if len(kept) != n {
			t.tasks = kept
			t.open = -1
			for i := range t.tasks {
				if !t.tasks[i].Final() {
					t.open = i
				}
			}
		}
	}
	t.index()
	t.ids = t.ids[:0]
	for i, msg := range msgs {
		if i > 0 && msg.Id <= msgs[i-1].Id {
			t.inOrder = false
		}
		t.ids = append(t.ids, msg.Id)
	}
	t.rederive = false
	changed = t.own(msgs, changed)
	return t.replies(msgs, 0, at, changed)
}

// own gives the user's messages among msgs that no task holds a task: their
// own, or the running one they steer.
func (t *taskTracker) own(msgs []conv.Message, changed []int) []int {
	for _, msg := range msgs {
		if msg.Role != conv.RoleUser || t.owner(msg.Id) >= 0 {
			continue
		}
		if msg.Time.Before(t.since) {
			// History: its turn is over.
			ended := msg.Time
			t.add(Task{ID: msg.Id, Status: TaskStatusCompleted, Prompt: cut(msg.Message, taskPromptLimit), StartedAt: msg.Time, EndedAt: &ended})
			changed = append(changed, len(t.tasks)-1)
			continue
		}
		if t.open >= 0 && t.ran && t.idle() {
			// Only the grace period after an answer (or an interrupt)
			// keeps the task open; the agent isn't working, so the
			// message starts a task rather than steering this one.
			changed = appendOnce(changed, t.open)
			t.end(t.open, msg.Time)
		}
		if t.open >= 0 {
			t.tasks[t.open].Steered = append(t.tasks[t.open].Steered, msg.Id)
			t.owners[msg.Id] = t.open
			changed = appendOnce(changed, t.open)
			continue
		}
		t.add(Task{ID: msg.Id, Status: TaskStatusRunning, Prompt: cut(msg.Message, taskPromptLimit), StartedAt: msg.Time})
		t.open = len(t.tasks) - 1
		t.ran = t.status.Status == AgentStatusRunning
		changed = append(changed, t.open)
	}
	return changed
}

// add adds a task.
func (t *taskTracker) add(task Task) {
	if n := len(t.tasks); n > 0 && t.tasks[n-1].ID >= task.ID {
		t.inOrder = false
	}
	t.owners[task.ID] = len(t.tasks)
	t.tasks = append(t.tasks, task)
}

// replies updates the replies of the tasks from position k on; at has the
// positions of their messages in msgs. Each task's reply is the agent's
// last message before the next task; a history task ends with it.
func (t *taskTracker) replies(msgs []conv.Message, k int, at map[int]int, changed []int) []int {
	for i := k; i < len(t.tasks); i++ {
		from, ok := at[t.tasks[i].ID]
		if !ok {
			continue
		}
		next := len(msgs)
		if i+1 < len(t.tasks) {
			if n, ok := at[t.tasks[i+1].ID]; ok {
				next = n
			}
		}
		reply := -1
		for j := from + 1; j < next; j++ {
			if msgs[j].Role == conv.RoleAgent && msgs[j].Message != "" {
				reply = j
			}
		}
		if reply < 0 || (t.tasks[i].ReplyID != nil && *t.tasks[i].ReplyID == msgs[reply].Id) {
			continue
		}
		id := msgs[reply].Id
		t.tasks[i].ReplyID = &id
		if t.tasks[i].Status == TaskStatusCompleted && t.tasks[i].StartedAt.Before(t.since) {
			ended := msgs[reply].Time
			t.tasks[i].EndedAt = &ended
		}
		changed = appendOnce(changed, i)
	}
	return changed
}

// taskEndGrace is how long the agent may take to work again once a request
// or terminal dialog it waited on is answered, before its task counts as
// done.
const taskEndGrace = 3 * time.Second

// statusChanged takes the agent's status after a change; now is when. A
// task ends when the agent stops working, unless it waits on a request or a
// terminal dialog. When the agent was idle already and only stopped
// waiting, as an answer was given, it likely works again: confirm reports
// that the task ends if the agent is still idle after taskEndGrace
// (endIfIdle).
func (t *taskTracker) statusChanged(next StatusChangeBody, now time.Time) (changed []int, confirm bool) {
	previous := t.status
	t.status = next
	if t.open < 0 {
		return nil, false
	}
	open := t.open
	switch {
	case next.Lifecycle == LifecycleExited || next.Lifecycle == LifecycleFailed:
		t.tasks[open].Errors = append(t.tasks[open].Errors, "the agent "+string(next.Lifecycle))
		t.end(open, now)
		return []int{open}, false
	case next.Status == AgentStatusRunning:
		t.ran = true
	case t.ran && t.idle():
		if previous.Status == AgentStatusRunning {
			t.end(open, now)
			return []int{open}, false
		}
		return nil, true
	}
	return nil, false
}

// idle reports whether the agent neither works nor waits on an answer.
func (t *taskTracker) idle() bool {
	return t.status.Status == AgentStatusStable && t.status.PendingRequests == 0 && t.status.TerminalPrompt == ""
}

// endIfIdle ends the running task if the agent is still idle, taskEndGrace
// after it stopped waiting on an answer.
func (t *taskTracker) endIfIdle(now time.Time) []int {
	if t.open < 0 || !t.ran || !t.idle() {
		return nil
	}
	open := t.open
	t.end(open, now)
	return []int{open}
}

// agentError notes an error the agent reported in the running task.
func (t *taskTracker) agentError(err ErrorBody) []int {
	if t.open < 0 || err.Level != conv.ErrorLevelError {
		return nil
	}
	t.tasks[t.open].Errors = append(t.tasks[t.open].Errors, err.Message)
	return []int{t.open}
}

// end ends the task at position i, with the outcome its turn had.
func (t *taskTracker) end(i int, now time.Time) {
	task := &t.tasks[i]
	ended := now
	task.EndedAt = &ended
	switch limit := t.status.LimitStop; {
	case limit != nil && !limit.StoppedAt.Before(task.StartedAt):
		task.Status = TaskStatusLimited
		task.Limit = limit
	case len(task.Errors) > 0:
		task.Status = TaskStatusFailed
	default:
		task.Status = TaskStatusCompleted
	}
	if t.open == i {
		t.open, t.ran = -1, false
	}
}

// taskAt returns the ID of the task a rich message from at belongs to: the
// running one, else the latest that started by then; nil before the first.
func (t *taskTracker) taskAt(at time.Time) *int {
	if t.open >= 0 {
		id := t.tasks[t.open].ID
		return &id
	}
	for i := len(t.tasks) - 1; i >= 0; i-- {
		if !t.tasks[i].StartedAt.After(at) {
			id := t.tasks[i].ID
			return &id
		}
	}
	return nil
}

func appendOnce(positions []int, i int) []int {
	for _, p := range positions {
		if p == i {
			return positions
		}
	}
	return append(positions, i)
}

// TaskUpdateBody is the SSE payload for a task that started or changed.
type TaskUpdateBody struct {
	Task
	Seq uint64 `json:"seq" doc:"Sequence number of this change; see message_update."`
}

// notifyStatusLocked sends the agent's status, and the tasks it ended.
// Caller holds e.mu.
func (e *EventEmitter) notifyStatusLocked() {
	body := e.statusChangeBody()
	e.notifyChannels(EventTypeStatusChange, body)
	changed, confirm := e.tasks.statusChanged(body, e.clock.Now())
	e.tasksChangedLocked(changed)
	if confirm {
		if e.taskTimer != nil {
			e.taskTimer.Stop()
		}
		e.taskTimer = e.clock.AfterFunc(taskEndGrace, func() {
			e.mu.Lock()
			defer e.unlock()
			e.tasksChangedLocked(e.tasks.endIfIdle(e.clock.Now()))
		}, "tasks", "end")
	}
}

// tasksChangedLocked stores and sends the tasks at the positions changed.
// Caller holds e.mu.
func (e *EventEmitter) tasksChangedLocked(changed []int) {
	for _, task := range e.tasksStoredLocked(changed) {
		e.notifyChannels(EventTypeTaskUpdate, task)
	}
}

// tasksStoredLocked gives the tasks at the positions changed the next seqs
// and stores them, and returns their updates. Caller holds e.mu.
func (e *EventEmitter) tasksStoredLocked(changed []int) []TaskUpdateBody {
	updates := make([]TaskUpdateBody, 0, len(changed))
	for _, i := range changed {
		task := e.tasks.tasks[i].Clone()
		e.seq++
		e.taskSeq[task.ID] = e.seq
		if e.persister != nil {
			if data, err := json.Marshal(task); err == nil {
				e.persister.PutTask(task.ID, data, e.seq)
			}
		}
		updates = append(updates, TaskUpdateBody{Task: task, Seq: e.seq})
	}
	if len(changed) > 0 && e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	return updates
}

// Tasks returns the conversation's tasks, the earliest first.
func (e *EventEmitter) Tasks() []Task {
	e.mu.Lock()
	defer e.unlock()
	tasks := make([]Task, len(e.tasks.tasks))
	for i, task := range e.tasks.tasks {
		tasks[i] = task.Clone()
	}
	return tasks
}

// TasksResponse is the body of GET /tasks.
type TasksResponse struct {
	Body struct {
		Tasks []Task `json:"tasks" nullable:"false" doc:"The conversation's tasks, the earliest first."`
	}
}

func (s *Server) getTasks(_ context.Context, _ *struct{}) (*TasksResponse, error) {
	resp := &TasksResponse{}
	resp.Body.Tasks = s.emitter.Tasks()
	return resp, nil
}

func (s *Server) registerTaskRoutes() {
	huma.Get(s.api, "/tasks", s.getTasks, func(o *huma.Operation) {
		o.Summary = "Get the conversation's tasks"
		o.Description = "Returns the conversation's tasks: each message of the user's and the agent's turn on it, " +
			"until the agent is done (stable again, with no request or terminal dialog open). Messages the user " +
			"adds while the agent works on a task belong to it (steered). A task is running, completed, failed (the agent reported " +
			"an error or exited) or limited (a usage limit stopped it). /events sends task_update when one starts or changes."
	})
}
