package claude

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

func TestRenameOnTopicChange(t *testing.T) {
	proc := newFakeProcess("")
	a, sink := resumed(t, Config{}, "session-1", proc)
	defer func() { require.NoError(t, a.Close()) }()

	turn := func(text string) {
		t.Helper()
		require.NoError(t, a.StartTurn(context.Background(), native.Input{Text: text}))
		proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"session-1"}`))
		proc.send(t, []byte(`{"type":"result","subtype":"success","result":"ok","session_id":"session-1"}`))
		proc.barrier(t)
	}
	turn("Review notification delivery")
	turn("Review notification delivery tests")
	turn("檢查登入逾時")
	var renames []map[string]any
	require.Eventually(t, func() bool {
		renames = nil
		for _, f := range proc.writtenFrames() {
			if subtypeOf(f) == "rename_session" {
				renames = append(renames, f)
			}
		}
		return len(renames) > 0
	}, 5*time.Second, time.Millisecond)
	require.Len(t, renames, 1)
	assert.Equal(t, map[string]any{
		"subtype": "rename_session", "title": "檢查登入逾時", "source": "host",
	}, renames[0]["request"])
	proc.send(t, []byte(`{"type":"system","subtype":"session_title_changed","session_id":"session-1","title":"檢查登入逾時"}`))
	proc.answer(t, renames[0], `{}`)
	proc.barrier(t)
	assert.Equal(t, []string{"檢查登入逾時"}, sink.State().Names)
	assert.Empty(t, sink.State().Refs, "renaming keeps the resumed session")
	assert.Len(t, sink.State().Results, 3)
}
