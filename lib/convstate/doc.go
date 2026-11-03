// Package convstate keeps the state of the conversation that AgentAPI's
// clients read, apart from how they read it (lib/httpapi):
//
//   - EventEmitter stores the messages, rich messages, timeline, requests,
//     status, screen and errors the transports report, numbers every
//     change (epoch and seq), and replays and streams them to subscribers.
//   - It derives the tasks (tasks.go) and the summary (summary.go) from
//     them, and keeps the message queue as the server last reported it
//     (queue.go).
//   - With a Persister it hands every change to the state database.
//
// The emitter's lock guards all of it. A subscriber that falls behind its
// buffer is dropped, so nothing the emitter calls under its lock waits on
// one; the Persister, the status change handler and the instance function
// are called under it and must not block either.
package convstate
