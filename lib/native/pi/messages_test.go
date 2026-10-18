package pi

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
)

// TestThrottleBuildsWhatItSends: a message that changes faster than the
// snapshot interval is built only when a snapshot is sent, not for every
// change.
func TestThrottleBuildsWhatItSends(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	builds := 0
	build := func() (jsonlwatcher.RichMessage, bool) {
		builds++
		return jsonlwatcher.RichMessage{MessageID: "m", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: strconv.Itoa(builds)}}}, true
	}
	require.True(t, h.a.runOnLoop(func() {
		for range 1000 {
			h.a.emitThrottled("m", build)
		}
	}))
	assert.Equal(t, 1, builds, "the first change is sent at once")
	h.advance(snapshotInterval)
	assert.Equal(t, 2, builds, "the latest state is built when the interval ends")
	assert.Len(t, h.sink.Snapshots("m"), 2)
}

// TestStreamedToolCall: the arguments of a tool call show once they are
// complete; the deltas before are partial JSON.
func TestStreamedToolCall(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	h.write(`{"type":"message_start","message":{"role":"assistant","content":[],"model":"m","timestamp":6}}`)
	h.write(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"write"}}`)
	content := strings.Repeat("x", 100)
	for _, delta := range []string{`{"path":"a.txt",`, `"content":"`, content, `"}`} {
		h.write(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":0,"delta":` + strconv.Quote(delta) + `}}`)
	}
	h.settle()
	snaps := h.sink.Snapshots("assistant-6")
	require.Len(t, snaps, 1)
	require.Len(t, snaps[0].Content, 1)
	assert.Equal(t, "write", snaps[0].Content[0].ToolName)
	assert.Nil(t, snaps[0].Content[0].ToolInput)
	_, timer := h.clock.Peek()
	assert.False(t, timer, "the deltas don't change the snapshot")

	h.write(`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"c1","name":"write","arguments":{"path":"a.txt","content":"` + content + `"}}}}`)
	h.settle()
	h.advance(snapshotInterval)
	snaps = h.sink.Snapshots("assistant-6")
	require.Len(t, snaps, 2)
	assert.JSONEq(t, `{"path":"a.txt","content":"`+content+`"}`, string(snaps[1].Content[0].ToolInput))
}

// TestTurnEndSendsTheStream: a run that ends without message_end for the
// message it streamed still shows its last state.
func TestTurnEndSendsTheStream(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	cmd := h.prompt("p", "hi")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	h.write(`{"type":"message_start","message":{"role":"assistant","content":[],"model":"m","timestamp":7}}`)
	for _, d := range []string{"a", "b"} {
		h.write(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"` + d + `"}}`)
	}
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	snaps := h.sink.Snapshots("assistant-7")
	require.Len(t, snaps, 2)
	assert.Equal(t, []string{"ab"}, texts(snaps[1], "text"))
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
	_, timer := h.clock.Peek()
	assert.False(t, timer)
}

// TestExitSendsWhatWaits: when Pi exits mid-stream, the text and tool
// output that waited for the snapshot interval are still shown.
func TestExitSendsWhatWaits(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	h.write(`{"type":"message_start","message":{"role":"assistant","content":[],"model":"m","timestamp":5}}`)
	for _, d := range []string{"a", "b", "c"} {
		h.write(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"` + d + `"}}`)
	}
	h.write(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"seq 2"}}`)
	h.write(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"1"}]}}`)
	h.write(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"1\n2"}]}}`)
	h.settle()
	require.Len(t, h.sink.Snapshots("assistant-5"), 1)
	require.Len(t, h.sink.Snapshots("result-c1"), 1)

	h.pi.exit(nil)
	<-h.a.loopDone
	snaps := h.sink.Snapshots("assistant-5")
	assert.Equal(t, []string{"abc"}, texts(snaps[len(snaps)-1], "text"))
	results := h.sink.Snapshots("result-c1")
	require.Len(t, results, 2)
	assert.Equal(t, "1\n2", results[1].Content[0].Text)
	assert.Equal(t, "running", results[1].Content[0].Status)
	_, timer := h.clock.Peek()
	assert.False(t, timer)
}
