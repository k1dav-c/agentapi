// Package instanceapi is the contract between an AgentAPI and the clients
// that watch many: who an AgentAPI is (WebhookInstance, in its webhook
// events, its summary and its registry entry), its summary (GET /summary,
// summary_update), the envelope and signature of its webhook events, and
// what a hub shows of each one (StateOf). It holds types and small pure
// functions only, so a client depends on it rather than on the server.
//
// lib/httpapi keeps aliases of these types under the same names: the
// OpenAPI schema names them by their Go names.
package instanceapi

import (
	"os"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/util"
)

// Transport is how AgentAPI talks to its agent.
type Transport string

const (
	TransportPTY    Transport = "pty"
	TransportNative Transport = "native"
)

var TransportValues = []Transport{
	TransportPTY,
	TransportNative,
}

func (tr Transport) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "Transport", TransportValues)
}

// WebhookInstance identifies an AgentAPI, so that one receiver can tell
// many apart: in every webhook event, in its summary, and (as a registry
// entry) in the registry of its machine.
type WebhookInstance struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	ServerID string `json:"server_id"`
	Hostname string `json:"hostname,omitempty"`
	Port     int    `json:"port,omitempty"`
	PID      int    `json:"pid,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	// AgentType and Transport say which agent it runs, and how.
	AgentType mf.AgentType `json:"agent_type"`
	Transport Transport    `json:"transport"`
	Version   string       `json:"agentapi_version,omitempty"`
	// URL is where it can be reached (--public-url); a client that finds
	// it elsewhere keeps where it reaches it here.
	URL          string `json:"url,omitempty"`
	WorkspaceURL string `json:"workspace_url,omitempty"`
}

// ExpandPublicURL fills in a public URL's placeholders: {port} is the port
// AgentAPI listens on and {hostname} the machine's name. One setting then
// serves every AgentAPI of a machine (the config file of agentapi hub join).
func ExpandPublicURL(publicURL string, port int) string {
	if !strings.Contains(publicURL, "{") {
		return publicURL
	}
	hostname, _ := os.Hostname()
	return strings.NewReplacer("{port}", strconv.Itoa(port), "{hostname}", hostname).Replace(publicURL)
}
