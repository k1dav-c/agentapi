// Package webhook sends an AgentAPI's webhook events (--webhook-url, PUT
// /webhook): Dispatcher queues, signs and delivers them, with retries,
// heartbeats and the {ip} in the public URL; Observe turns the
// conversation's events, read from a convstate.EventEmitter like an /events
// client, into task, request, message and tool events. lib/httpapi wires it
// to the emitter and serves its configuration.
package webhook
