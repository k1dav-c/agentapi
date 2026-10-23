// Package handoff defines the browser-independent human reply protocol.
package handoff

type Request struct {
	ID        string `json:"id"`
	ServerID  string `json:"server_id"`
	Turn      uint64 `json:"turn"`
	AgentType string `json:"agent_type"`
	Kind      string `json:"kind"` // message or terminal
	Question  string `json:"question,omitempty"`
	Content   string `json:"content"`
}

type Pending struct {
	Request *Request `json:"request"`
}

type Reply struct {
	RequestID string `json:"request_id"`
	ID        string `json:"id"`
	Content   string `json:"content"`
}

type Result struct {
	Outcome string `json:"outcome"` // applied, superseded, invalid, uncertain
}
