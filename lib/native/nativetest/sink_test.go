package nativetest

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

// errorRecorder records the errors a test reports.
type errorRecorder struct {
	testing.TB
	mu     sync.Mutex
	errors []string
}

func (r *errorRecorder) Error(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprint(args...))
}

func (r *errorRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errors...)
}

func TestCheckUnlocked(t *testing.T) {
	t.Parallel()
	rec := &errorRecorder{TB: t}
	sink := NewSink(rec)
	sink.probeTimeout = 50 * time.Millisecond
	var adapterMu sync.Mutex
	sink.CheckUnlocked(func() {
		adapterMu.Lock()
		defer adapterMu.Unlock()
	})

	sink.TurnStarted()
	assert.Empty(t, rec.recorded(), "a call without the lock passes")

	adapterMu.Lock()
	sink.TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	adapterMu.Unlock()
	assert.Equal(t, []string{"the adapter holds its lock while it calls the Sink"}, rec.recorded())
}

func TestSinkKeepsACopy(t *testing.T) {
	t.Parallel()
	sink := NewSink(t)
	isError := false
	msg := transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", IsError: &isError, Images: []transcript.RichImage{{MediaType: "image/png"}}},
	}}
	sink.RichMessage(msg)
	msg.Content[0].Images[0].URL = "/changed"
	isError = true
	got, ok := sink.Message("m1", "assistant")
	assert.True(t, ok)
	assert.Empty(t, got.Content[0].Images[0].URL)
	assert.False(t, *got.Content[0].IsError)
}
