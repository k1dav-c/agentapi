package host

// The wire types between the launcher and the hub: the launcher's host.*
// events, which it sends to the hub's /hooks signed like an AgentAPI's
// events, and its requests for a job's GitHub token.

// The launcher's events.
const (
	EventHostStarted   = "host.started"
	EventHostHeartbeat = "host.heartbeat"
	EventHostStopped   = "host.stopped"
)

// HostEventData is the data of a host.* event: the machine, the token the
// hub calls the launcher with, and the one it calls the jobs' AgentAPIs
// with.
type HostEventData struct {
	HostInfo
	Token string `json:"token"`
	// InstanceToken is the API token of every job's AgentAPI, which the
	// launcher made (instance.token): each machine has its own, so the hub
	// needs no --instance-token of theirs.
	InstanceToken string `json:"instance_token,omitempty"`
	// HeartbeatSeconds is how often host.heartbeat comes.
	HeartbeatSeconds int `json:"heartbeat_seconds"`
}

// HubTokenPath is the hub's route that gives a launcher a job's GitHub
// token; the request is signed with the hub's secret.
const HubTokenPath = "/api/github/token"

// HubTokenRequest is POST HubTokenPath.
type HubTokenRequest struct {
	Hostname string `json:"hostname"`
	Job      string `json:"job"`
}

// CredentialsHub, in StartRequest.Credentials, has the job's git and gh
// get their token from the hub; empty uses the launcher's token file if
// there is one, else the machine's own credentials.
const CredentialsHub = "hub"
