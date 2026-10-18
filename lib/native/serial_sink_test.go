package native_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
)

// Calls from many goroutines reach the sink one at a time; the recording
// sink fails the test when two overlap.
func TestSerialSink(t *testing.T) {
	t.Parallel()
	rec := nativetest.NewSink(t)
	sink := native.SerialSink(rec)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink.TurnStarted()
			native.ReportUsage(sink, native.AgentUsage{})
			native.ReportSettings(sink, native.Settings{PermissionMode: native.PermissionModeAsk})
		}()
	}
	wg.Wait()
	state := rec.State()
	assert.Equal(t, 20, state.Started)
	assert.Len(t, state.Usage, 20, "a UsageSink stays one")
	assert.Len(t, state.Settings, 20, "a SettingsSink stays one")
}
