package convstate

import (
	"slices"
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/transcript"
)

// A conversation grows with every task: the rich messages and the timeline
// events hold what the agent said and did, about 100 KB a task. The emitter
// keeps those of the latest tasks only, its hot window, when a Persister
// keeps them all; older ones are read from the state database (see
// Page). The messages and the tasks stay: they are a few KB a task, and the
// tasks are derived from all of them.
const (
	// DefaultHotTasks is how many of the latest tasks' rich messages and
	// timeline events the emitter keeps in memory with a state database:
	// about 10 MB at 100 KB a task, a few days of work.
	DefaultHotTasks = 100
	// DefaultRecentTasks is how many of the latest tasks a full replay and
	// a list read without a cursor hold. The chat shows the latest five
	// and loads earlier ones as the user scrolls up.
	DefaultRecentTasks = 20
	// MaxPageTasks is the most tasks a page of the history holds.
	MaxPageTasks = 100
)

// WithHotTasks keeps the rich messages and timeline events of only the
// latest n tasks in memory, at least the recent window. Use it with a
// Persister that can read the others back; 0 keeps them all.
func WithHotTasks(n int) EventEmitterOption {
	return func(e *EventEmitter) { e.hotTasks = max(n, 0) }
}

// WithRecentTasks sets how many of the latest tasks a full replay and a
// page without a cursor hold (DefaultRecentTasks for 0).
func WithRecentTasks(n int) EventEmitterOption {
	return func(e *EventEmitter) {
		if n > 0 {
			e.recentTasks = n
		}
	}
}

// window is what the emitter let go of to keep its hot window.
type window struct {
	// richBase is the stored position of the first rich message held:
	// the persister stores the rich message at index i as richBase+i.
	richBase int
	// cold: rich messages or timeline events were let go of. hotTask is
	// then the ID of the first task whose rich messages are all held, and
	// hotTime its start, from which the timeline events are.
	cold    bool
	hotTask int
	hotTime time.Time
	// coldSeq is the highest seq of a rich message let go of: a client
	// that resumes from before it misses it, and gets a full replay.
	coldSeq uint64
}

// trimLocked lets go of the rich messages and timeline events of the tasks
// before the hot window. Caller holds e.mu.
func (e *EventEmitter) trimLocked() {
	tasks := e.tasks.tasks
	if e.hotTasks <= 0 || len(tasks) <= e.hotTasks {
		return
	}
	first := tasks[len(tasks)-e.hotTasks]
	// The rich messages and events come in the order they happened, so
	// those of earlier tasks come first.
	n := 0
	for n < len(e.richMessages) && (e.richMessages[n].TaskID == nil || *e.richMessages[n].TaskID < first.ID) {
		e.win.coldSeq = max(e.win.coldSeq, e.richSeq[n])
		n++
	}
	m := 0
	for m < len(e.sessionEvents) && e.sessionEvents[m].EventTime.Before(first.StartedAt) {
		m++
	}
	if n == 0 && m == 0 {
		return
	}
	e.win.cold = true
	e.win.hotTask, e.win.hotTime = first.ID, first.StartedAt
	if n > 0 {
		// New slices, so that what was let go of is freed.
		e.richMessages = slices.Clone(e.richMessages[n:])
		e.richSeq = slices.Clone(e.richSeq[n:])
		e.win.richBase += n
		e.richIndex = make(map[richKey]int, len(e.richMessages))
		for i, msg := range e.richMessages {
			e.richIndex[richKey{msg.MessageID, msg.Role}] = i
		}
	}
	if m > 0 {
		e.sessionEvents = slices.Clone(e.sessionEvents[m:])
	}
}

// PageRange is a run of consecutive tasks of the conversation: a page of
// its history.
type PageRange struct {
	// Tasks are the page's tasks, the earliest first, with their numbers.
	Tasks []instanceapi.Task
	// From is the ID of the page's first task, Before that of the task
	// after it (with BeforeTime its start), or 0 when the page reaches
	// the latest. Prelude: the page starts at the beginning of the
	// conversation, so it also holds what came before the first task.
	From       int
	FromTime   time.Time
	Before     int
	BeforeTime time.Time
	Prelude    bool
	// More: tasks came before the page.
	More bool
}

// Page returns the limit tasks (the recent window for 0, at most
// MaxPageTasks) that started before the task before, or the latest ones
// for 0.
func (e *EventEmitter) Page(before, limit int) PageRange {
	e.mu.Lock()
	defer e.unlock()
	return e.pageLocked(before, limit)
}

// pageLocked is Page. Caller holds e.mu.
func (e *EventEmitter) pageLocked(before, limit int) PageRange {
	if limit <= 0 {
		limit = e.recentTasks
	}
	limit = min(limit, MaxPageTasks)
	tasks := e.tasks.tasks
	end := len(tasks)
	switch {
	case before <= 0:
	case e.tasks.inOrder:
		end, _ = slices.BinarySearchFunc(tasks, before, func(task instanceapi.Task, id int) int { return task.ID - id })
	default:
		for end > 0 && tasks[end-1].ID >= before {
			end--
		}
	}
	start := max(0, end-limit)
	r := PageRange{Prelude: start == 0, More: start > 0}
	r.Tasks = make([]instanceapi.Task, 0, end-start)
	for i := start; i < end; i++ {
		r.Tasks = append(r.Tasks, numbered(tasks[i], i))
	}
	if start < len(tasks) {
		r.From, r.FromTime = tasks[start].ID, tasks[start].StartedAt
	}
	if end < len(tasks) {
		r.Before, r.BeforeTime = tasks[end].ID, tasks[end].StartedAt
	}
	return r
}

// numbered returns a copy of task, which is at position i of the
// conversation's tasks, with its number.
func numbered(task instanceapi.Task, i int) instanceapi.Task {
	task = task.Clone()
	task.Number = i + 1
	return task
}

// holdsMessage reports whether the page holds the message with ID id.
func (r PageRange) holdsMessage(id int) bool {
	if len(r.Tasks) == 0 && !r.Prelude {
		return false
	}
	return (r.Prelude || id >= r.From) && (r.Before == 0 || id < r.Before)
}

// HoldsRich reports whether the page holds a rich message of task taskID
// (nil before the first task).
func (r PageRange) HoldsRich(taskID *int) bool {
	if taskID == nil {
		return r.Prelude
	}
	return r.holdsMessage(*taskID)
}

// HoldsEvent reports whether the page holds a timeline event from at.
func (r PageRange) HoldsEvent(at time.Time) bool {
	if len(r.Tasks) == 0 && !r.Prelude {
		return false
	}
	return (r.Prelude || !at.Before(r.FromTime)) && (r.BeforeTime.IsZero() || at.Before(r.BeforeTime))
}

// MessagesIn returns the messages of the page r, which the emitter holds
// all of.
func (e *EventEmitter) MessagesIn(r PageRange) []conv.Message {
	return e.messagesWhere(r.holdsMessage)
}

// Messages returns every message of the conversation: the emitter keeps
// them however long it runs, since they are small (it lets go of the rich
// ones).
func (e *EventEmitter) Messages() []conv.Message {
	return e.messagesWhere(func(int) bool { return true })
}

// messagesWhere returns the messages whose IDs keep says to.
func (e *EventEmitter) messagesWhere(keep func(id int) bool) []conv.Message {
	e.mu.Lock()
	defer e.unlock()
	var msgs []conv.Message
	for _, msg := range e.messages {
		if keep(msg.Id) {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// RichMessagesIn returns the rich messages of the page r, and false if it
// let go of some of them: the state database has them all.
func (e *EventEmitter) RichMessagesIn(r PageRange) ([]transcript.RichMessage, bool) {
	e.mu.Lock()
	defer e.unlock()
	if e.win.cold && (r.Prelude || r.From < e.win.hotTask) {
		return nil, false
	}
	var msgs []transcript.RichMessage
	for _, msg := range e.richMessages {
		if r.HoldsRich(msg.TaskID) {
			msgs = append(msgs, msg)
		}
	}
	return msgs, true
}

// SessionEventsIn returns the timeline events of the page r, and false if
// it let go of some of them.
func (e *EventEmitter) SessionEventsIn(r PageRange) ([]transcript.SessionEvent, bool) {
	e.mu.Lock()
	defer e.unlock()
	if e.win.cold && (r.Prelude || r.FromTime.Before(e.win.hotTime)) {
		return nil, false
	}
	var events []transcript.SessionEvent
	for _, event := range e.sessionEvents {
		if r.HoldsEvent(event.EventTime) {
			events = append(events, event)
		}
	}
	return events, true
}

// Held says where what the emitter holds starts in the state database: the
// position of the first rich message and the ID of the first timeline
// event. What comes before is there only.
func (e *EventEmitter) Held() (richPos, eventID int) {
	e.mu.Lock()
	defer e.unlock()
	eventID = e.nextSessionEventID
	if len(e.sessionEvents) > 0 {
		eventID = e.sessionEvents[0].EventID
	}
	return e.win.richBase, eventID
}

// recentLocked returns the recent window, which a full replay holds.
// Caller holds e.mu.
func (e *EventEmitter) recentLocked() PageRange {
	return e.pageLocked(0, e.recentTasks)
}

// NextEventID is the ID the next timeline event gets.
func (e *EventEmitter) NextEventID() int {
	e.mu.Lock()
	defer e.unlock()
	return e.nextSessionEventID
}
