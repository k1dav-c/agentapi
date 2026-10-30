package instanceapi

// Connection is how a client's watch of an instance's summary stream goes.
type Connection string

const (
	ConnectionConnecting  Connection = "connecting"
	ConnectionConnected   Connection = "connected"
	ConnectionUnreachable Connection = "unreachable"
	// ConnectionStopped: the instance said it stopped (instance.stopped).
	ConnectionStopped Connection = "stopped"
)

// State is what a client that watches many AgentAPIs shows of one, the
// most pressing first: it needs you, works, finished a task you haven't
// seen, waits, waits for what you muted, or can't be followed.
type State string

const (
	StateNeedsYou    State = "needs-you"
	StateWorking     State = "working"
	StateUnread      State = "unread"
	StateIdle        State = "idle"
	StateMuted       State = "muted"
	StateConnecting  State = "connecting"
	StateUnreachable State = "unreachable"
	StateStopped     State = "stopped"
)

// StateOf is the state of an instance whose summary stream is in
// connection, with its latest summary (nil before the first) and muted,
// the AttentionKey someone muted (empty for none).
func StateOf(connection Connection, summary *SummaryBody, muted string) State {
	switch connection {
	case ConnectionStopped:
		return StateStopped
	case ConnectionUnreachable:
		return StateUnreachable
	case ConnectionConnecting, ConnectionConnected:
	}
	if summary == nil {
		return StateConnecting
	}
	if key := AttentionKey(summary); key != "" {
		// Muted until what it waits for changes.
		if muted == key {
			return StateMuted
		}
		return StateNeedsYou
	}
	switch {
	case summary.Attention == AttentionExited:
		return StateStopped
	case summary.Status == AgentStatusRunning:
		return StateWorking
	case summary.Unread:
		return StateUnread
	default:
		return StateIdle
	}
}

// AttentionKey is what an instance waits for, to mute: why it needs you
// and, for a request, which one; empty when it needs nothing.
func AttentionKey(summary *SummaryBody) string {
	if summary == nil || summary.Attention == "" || summary.Attention == AttentionNone || summary.Attention == AttentionExited {
		return ""
	}
	key := string(summary.Attention)
	if summary.Request != nil {
		key += ":" + summary.Request.ID
	}
	return key
}
