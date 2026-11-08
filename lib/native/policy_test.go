package native_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
)

// The default is what --retry-delays shows in --help.
func TestFormatDelays(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"30s", "1m", "2m", "5m*12"}, native.FormatDelays(native.DefaultRetryDelays()))
	assert.Equal(t, []string{"1m30s*2", "10s", "1m30s"}, native.FormatDelays([]time.Duration{90 * time.Second, 90 * time.Second, 10 * time.Second, 90 * time.Second}))
	assert.Empty(t, native.FormatDelays(nil))
}

// Empty delays turn retries off; nil ones are the default.
func TestPolicyRetriesOff(t *testing.T) {
	t.Parallel()
	assert.False(t, native.Policy{}.RetriesOff())
	assert.True(t, native.Policy{RetryDelays: []time.Duration{}}.RetriesOff())
	assert.False(t, native.Policy{RetryDelays: []time.Duration{time.Second}}.RetriesOff())
}
