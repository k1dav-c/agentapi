package agentstate

import "time"

// LimitStop is a turn that stopped because the account reached a usage
// limit. It holds the queue until the limit resets, or until a
// continuation that is due went out.
type LimitStop struct {
	Message   string    `json:"message" doc:"What the agent said, e.g. You've hit your limit · resets 3pm."`
	StoppedAt time.Time `json:"stopped_at" doc:"When the turn stopped."`
	// ResetsAt is nil when the agent doesn't say.
	ResetsAt *time.Time `json:"resets_at,omitempty" doc:"When the limit resets, if the agent says. Queued messages wait until then."`
	// ContinueAt is set while a continuation is scheduled.
	ContinueAt *time.Time `json:"continue_at,omitempty" doc:"When AgentAPI sends a message that continues the turn, if one is scheduled (POST /limit/continue). Queued messages go out after it."`
}

// RetryWait is a turn that failed on a transient upstream error (an HTTP
// 5xx or 429, an overloaded API) after the agent's own retries. It holds
// the queue until AgentAPI tried the turn again: it sends
// native.ContinuePrompt at RetryAt, so the agent goes on without the user's message twice.
type RetryWait struct {
	Message string `json:"message" doc:"What the agent said, e.g. API Error: 502 Bad Gateway."`
	// APIStatus is 0 when the agent doesn't say.
	APIStatus int       `json:"api_status,omitempty" doc:"The HTTP status the API or gateway answered with, e.g. 502, if the agent says."`
	StoppedAt time.Time `json:"stopped_at" doc:"When the turn failed."`
	Attempt   int       `json:"attempt" doc:"Which retry comes next, from 1."`
	Attempts  int       `json:"attempts" doc:"How many times in a row AgentAPI tries a failed turn again (--retry-delays). After the last, a failure is an error."`
	RetryAt   time.Time `json:"retry_at" doc:"When AgentAPI sends a message that continues the turn. Queued messages go out after it."`
}
