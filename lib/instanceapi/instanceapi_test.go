package instanceapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// A signature covers the timestamp and the body, with the secret.
func TestSignVerify(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"type":"instance.started"}`)
	header := func(at time.Time, signature string) http.Header {
		h := http.Header{}
		h.Set(instanceapi.HeaderTimestamp, strconv.FormatInt(at.Unix(), 10))
		if signature != "" {
			h.Set(instanceapi.HeaderSignature, signature)
		}
		return h
	}
	signature := instanceapi.Sign("s3cret", "1700000000", body)
	assert.Equal(t, "sha256=", signature[:7])

	require.NoError(t, instanceapi.Verify("s3cret", header(now, signature), body, now))
	require.NoError(t, instanceapi.Verify("s3cret", header(now, signature), body, now.Add(9*time.Minute)), "a retry")
	assert.ErrorContains(t, instanceapi.Verify("other", header(now, signature), body, now), "another secret")
	assert.ErrorContains(t, instanceapi.Verify("s3cret", header(now, signature), []byte(`{}`), now), "another secret", "another body")
	assert.ErrorContains(t, instanceapi.Verify("s3cret", header(now, signature), body, now.Add(time.Hour)), "from now")
	assert.ErrorContains(t, instanceapi.Verify("s3cret", header(now, ""), body, now), "no signature")
	assert.ErrorContains(t, instanceapi.Verify("s3cret", http.Header{}, body, now), "no timestamp")
}

// What a hub shows of an instance, the most pressing first.
func TestStateOf(t *testing.T) {
	t.Parallel()
	summary := func(change func(*instanceapi.SummaryBody)) *instanceapi.SummaryBody {
		s := &instanceapi.SummaryBody{Status: instanceapi.AgentStatusStable, Attention: instanceapi.AttentionNone}
		change(s)
		return s
	}
	connected := instanceapi.ConnectionConnected
	request := summary(func(s *instanceapi.SummaryBody) {
		s.Attention = instanceapi.AttentionRequest
		s.Request = &instanceapi.SummaryRequest{ID: "r1"}
	})
	for name, test := range map[string]struct {
		connection instanceapi.Connection
		summary    *instanceapi.SummaryBody
		muted      string
		want       instanceapi.State
	}{
		"needs you":           {connected, request, "", instanceapi.StateNeedsYou},
		"muted":               {connected, request, "request:r1", instanceapi.StateMuted},
		"another request":     {connected, request, "request:r0", instanceapi.StateNeedsYou},
		"working":             {connected, summary(func(s *instanceapi.SummaryBody) { s.Status = instanceapi.AgentStatusRunning }), "", instanceapi.StateWorking},
		"unread":              {connected, summary(func(s *instanceapi.SummaryBody) { s.Unread = true }), "", instanceapi.StateUnread},
		"idle":                {connected, summary(func(*instanceapi.SummaryBody) {}), "", instanceapi.StateIdle},
		"exited":              {connected, summary(func(s *instanceapi.SummaryBody) { s.Attention = instanceapi.AttentionExited }), "", instanceapi.StateStopped},
		"no summary yet":      {instanceapi.ConnectionConnecting, nil, "", instanceapi.StateConnecting},
		"reconnecting":        {instanceapi.ConnectionConnecting, request, "", instanceapi.StateNeedsYou},
		"unreachable":         {instanceapi.ConnectionUnreachable, request, "", instanceapi.StateUnreachable},
		"stopped":             {instanceapi.ConnectionStopped, request, "", instanceapi.StateStopped},
		"no attention at all": {connected, summary(func(s *instanceapi.SummaryBody) { s.Attention = "" }), "", instanceapi.StateIdle},
	} {
		assert.Equal(t, test.want, instanceapi.StateOf(test.connection, test.summary, test.muted), name)
	}

	assert.Equal(t, "request:r1", instanceapi.AttentionKey(request))
	assert.Equal(t, "login", instanceapi.AttentionKey(summary(func(s *instanceapi.SummaryBody) { s.Attention = instanceapi.AttentionLogin })))
	assert.Empty(t, instanceapi.AttentionKey(nil))
	assert.Empty(t, instanceapi.AttentionKey(summary(func(s *instanceapi.SummaryBody) { s.Attention = instanceapi.AttentionExited })))
}

func TestExpandPublicURL(t *testing.T) {
	t.Parallel()
	hostname, _ := os.Hostname()
	assert.Equal(t, "http://dev-1.vpn:3286", instanceapi.ExpandPublicURL("http://dev-1.vpn:{port}", 3286))
	assert.Equal(t, "http://"+hostname+":3284/", instanceapi.ExpandPublicURL("http://{hostname}:{port}/", 3284))
	assert.Equal(t, "https://agent.example.com", instanceapi.ExpandPublicURL("https://agent.example.com", 3284))
	assert.Equal(t, "/home/me/.agentapi/state/"+hostname+"-3285.db", instanceapi.ExpandPlaceholders("/home/me/.agentapi/state/{hostname}-{port}.db", 3285))
}

// agentapi2Summary is a summary as an AgentAPI 2 with the PTY transport
// sent it while its agent showed a dialog in its terminal.
const agentapi2Summary = `{
  "instance": {"id": "dev-2:3284", "name": "api", "server_id": "s2", "hostname": "dev-2", "port": 3284,
    "pid": 4242, "cwd": "/work/api", "agent_type": "aider", "transport": "pty",
    "agentapi_version": "2.5.1", "url": "http://dev-2:3284"},
  "status": "stable", "lifecycle": "running", "attention": "dialog", "title": "Fix the build",
  "pending_requests": 0, "queued_messages": 1,
  "terminal_prompt": "Allow edits to main.go? (Y)es/(N)o",
  "task": {"id": 3, "prompt": "Fix the build", "status": "running", "started_at": "2026-11-01T09:00:00Z"},
  "tasks": 3, "unread": false
}`

// An AgentAPI 2's summary reads with the fields AgentAPI 3 dropped
// ignored, and the rest of it kept. Its terminal dialog is no attention a
// client knows: it doesn't need you, and nothing to mute.
func TestAgentAPI2Summary(t *testing.T) {
	var summary instanceapi.SummaryBody
	require.NoError(t, json.Unmarshal([]byte(agentapi2Summary), &summary))
	assert.Equal(t, "dev-2:3284", summary.Instance.ID)
	assert.Equal(t, instanceapi.Transport("pty"), summary.Instance.Transport, "shown as it came")
	assert.Equal(t, instanceapi.AgentType("aider"), summary.Instance.AgentType)
	assert.Equal(t, "2.5.1", summary.Instance.Version)
	assert.Equal(t, "Fix the build", summary.Title)
	assert.Equal(t, 1, summary.QueuedMessages)
	require.NotNil(t, summary.Task)
	assert.Equal(t, 3, summary.Task.ID)
	assert.Empty(t, instanceapi.AttentionKey(&summary))
	assert.Equal(t, instanceapi.StateIdle, instanceapi.StateOf(instanceapi.ConnectionConnected, &summary, ""))
	summary.Status = instanceapi.AgentStatusRunning
	assert.Equal(t, instanceapi.StateWorking, instanceapi.StateOf(instanceapi.ConnectionConnected, &summary, ""))
}
