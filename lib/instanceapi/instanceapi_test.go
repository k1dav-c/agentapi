package instanceapi_test

import (
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
}
