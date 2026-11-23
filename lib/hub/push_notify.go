package hub

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/agentapi/lib/dispatch"
	"github.com/coder/agentapi/lib/instanceapi"
)

// The notifiers: what needs you and the finished tasks, from the store's
// changes (notifyPush), and the issues' work, from the dispatch's
// (notifyIssues).

// pushDelay is how long a change of an agent waits before the hub pushes
// it, and is pushed only if it still holds: an approval answered at once,
// or a finished task someone was looking at (the chat marks it read), is
// none.
const pushDelay = 5 * time.Second

// maxPushText is the most characters of a notification's text.
const maxPushText = 200

// pushSeen is what the notifier last saw of an instance.
type pushSeen struct {
	// attention is what it waited for (instanceapi.AttentionKey), "" for
	// nothing: a push goes when that changes to something.
	attention string
	// task is its latest task, and whether it was running.
	task    int
	running bool
}

// seePush is what summary says, for pushSeen; ok is false when the hub
// doesn't know (no summary, or it can't reach the instance), which keeps
// what it saw before.
func seePush(i Instance) (pushSeen, bool) {
	if i.Summary == nil || i.Connection == instanceapi.ConnectionStopped || i.Connection == instanceapi.ConnectionUnreachable {
		return pushSeen{}, false
	}
	seen := pushSeen{attention: instanceapi.AttentionKey(i.Summary)}
	if task := i.Summary.Task; task != nil {
		seen.task = task.ID
		seen.running = task.EndedAt == nil
	}
	return seen, true
}

// notifyPush follows the instances' changes until ctx ends, and pushes
// what needs you and the tasks that finished. What it sees first of an
// instance (after the hub starts, or the instance comes) only tells it
// where the instance stands.
func (h *Hub) notifyPush(ctx context.Context) {
	for ctx.Err() == nil {
		instances, _, ch := h.store.subscribe()
		seen := make(map[string]pushSeen, len(instances))
		for _, i := range instances {
			if s, ok := seePush(i); ok {
				seen[i.ID] = s
			}
		}
		h.followPush(ctx, ch, seen)
		h.store.unsubscribe(ch)
	}
}

// followPush pushes the changes of ch until it closes (it fell behind)
// or ctx ends.
func (h *Hub) followPush(ctx context.Context, ch chan storeEvent, seen map[string]pushSeen) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			switch event.Kind {
			case eventRemoved:
				delete(seen, event.Removed)
			case eventInstance:
				i := event.Instance
				now, ok := seePush(i)
				if !ok {
					continue
				}
				before, known := seen[i.ID]
				seen[i.ID] = now
				if !known {
					continue
				}
				switch {
				case now.attention != "" && now.attention != before.attention:
					h.pushLater(ctx, i.ID, pushNeedsYou, now)
				case now.attention == "" && before.running && !now.running && before.task == now.task:
					h.pushLater(ctx, i.ID, pushFinished, now)
				}
			case eventSettings:
			}
		}
	}
}

// pushLater pushes kind for the instance id after pushDelay, if what it
// says still holds then.
func (h *Hub) pushLater(ctx context.Context, id string, kind pushKind, seen pushSeen) {
	if !h.push.devices.any() {
		return
	}
	h.clock.AfterFunc(pushDelay, func() {
		if ctx.Err() != nil {
			return
		}
		i, ok := h.store.get(id)
		if !ok {
			return
		}
		payload, ok := pushFor(i, kind, seen)
		if !ok {
			return
		}
		h.push.goSendAll(ctx, payload, &i)
	}, "hub", "push")
}

// pushFor is the notification of kind for i, if i still stands where the
// notifier saw it (seen): it still waits for the same thing, and isn't
// muted; or its task is still the one that finished, and unread.
func pushFor(i Instance, kind pushKind, seen pushSeen) (pushPayload, bool) {
	now, ok := seePush(i)
	if !ok {
		return pushPayload{}, false
	}
	name := i.Name
	if name == "" {
		name = i.ID
	}
	payload := pushPayload{Kind: kind, ID: i.ID, Tag: "agentapi-hub-" + i.ID}
	switch kind {
	case pushNeedsYou:
		if now.attention != seen.attention || i.State != instanceapi.StateNeedsYou {
			return pushPayload{}, false
		}
		payload.Title = name + " needs you"
		payload.Body = waitsFor(i.Summary)
	case pushFinished:
		if now.attention != "" || now.task != seen.task || now.running || !i.Summary.Unread {
			return pushPayload{}, false
		}
		payload.Title = name + " finished"
		payload.Body = i.Summary.Title
		if payload.Body == "" && i.Summary.Task != nil {
			payload.Body = i.Summary.Task.Prompt
		}
	case pushTest, pushIssue:
		return pushPayload{}, false
	}
	payload.Body = clip(strings.Join(strings.Fields(payload.Body), " "), maxPushText)
	payload.Title = clip(payload.Title, maxPushText)
	return payload, true
}

// waitsFor is what an instance that needs you waits for, as the hub's
// page says it (waitsFor in chat/src/lib/hub-format.ts).
func waitsFor(summary *instanceapi.SummaryBody) string {
	switch {
	case summary.Attention == instanceapi.AttentionLogin:
		if summary.LoginRequired != nil && summary.LoginRequired.Message != "" {
			return summary.LoginRequired.Message
		}
		return "Log in to continue"
	case summary.Attention == instanceapi.AttentionLimit:
		if summary.LimitStop != nil && summary.LimitStop.Message != "" {
			return summary.LimitStop.Message
		}
		return "The account reached its limit"
	case summary.Request != nil:
		return summary.Request.Title
	default:
		return "Waiting for your answer"
	}
}

// clip cuts s to max characters, with an ellipsis.
func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// jobSeen is what the issues' notifier last saw of a job.
type jobSeen struct {
	state dispatch.JobState
	pr    string
}

// notifyIssues follows the dispatch's jobs until ctx ends, and pushes a
// pull request opened, a job that failed and one done. The jobs it sees
// first only tell it where they stand.
func (h *Hub) notifyIssues(ctx context.Context, d *dispatch.Service) {
	changed, cancel := d.Subscribe()
	defer cancel()
	seen := map[string]jobSeen{}
	for _, job := range d.Board().Jobs {
		seen[job.Key] = jobSeen{state: job.State, pr: job.PRURL}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		jobs := d.Board().Jobs
		next := make(map[string]jobSeen, len(jobs))
		for _, job := range jobs {
			now := jobSeen{state: job.State, pr: job.PRURL}
			next[job.Key] = now
			before, known := seen[job.Key]
			if !known {
				continue
			}
			if payload, ok := issuePush(job, before, now); ok && h.push.devices.any() {
				h.push.goSendAll(ctx, payload, nil)
			}
		}
		seen = next
	}
}

// issuePush is the push for a job that went from before to now, if one.
func issuePush(job dispatch.Job, before, now jobSeen) (pushPayload, bool) {
	payload := pushPayload{Kind: pushIssue, Issue: job.Key, Tag: "agentapi-hub-issue-" + job.Key, Body: job.Title}
	name := job.Repo + "#" + strconv.Itoa(job.Issue)
	switch {
	case now.state == dispatch.JobFailed && before.state != dispatch.JobFailed:
		payload.Title = name + " failed"
		if job.Error != "" {
			payload.Body = job.Error
		}
	case now.state == dispatch.JobDone && before.state != dispatch.JobDone:
		payload.Title = name + " done"
	case now.pr != "" && now.pr != before.pr:
		payload.Title = name + ": pull request opened"
	default:
		return pushPayload{}, false
	}
	payload.Body = clip(strings.Join(strings.Fields(payload.Body), " "), maxPushText)
	payload.Title = clip(payload.Title, maxPushText)
	return payload, true
}

// needingYou counts the agents that need you, for the app icon's badge.
func (h *Hub) needingYou() int {
	n := 0
	for _, i := range h.store.list() {
		if i.State == instanceapi.StateNeedsYou {
			n++
		}
	}
	return n
}

// groupsNow are the groups the agents are in now ("" for none), which the
// devices' Groups are matched against.
func (h *Hub) groupsNow() map[string]bool {
	groups := map[string]bool{}
	for _, i := range h.store.list() {
		groups[i.Group] = true
	}
	return groups
}
