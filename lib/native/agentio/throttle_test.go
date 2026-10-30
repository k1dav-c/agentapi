package agentio_test

import (
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native/agentio"
	"github.com/coder/agentapi/lib/transcript"
)

func TestThrottle(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	th := agentio.NewThrottle[string](clock, 100*time.Millisecond)
	builds := 0
	text := func(id, s string) func() (transcript.RichMessage, bool) {
		return func() (transcript.RichMessage, bool) {
			builds++
			return transcript.RichMessage{MessageID: id, Content: []transcript.RichContentBlock{{Type: "text", Text: s}}}, s != ""
		}
	}

	_, ok := th.Changed("a", text("a", ""))
	assert.False(t, ok, "nothing to show yet")
	assert.False(t, th.Sent("a"))
	msg, ok := th.Changed("a", text("a", "H"))
	require.True(t, ok, "the first snapshot goes out at once")
	assert.Equal(t, "H", msg.Content[0].Text)
	assert.True(t, th.Sent("a"))
	for _, s := range []string{"He", "Hel"} {
		_, ok := th.Changed("a", text("a", s))
		assert.False(t, ok, "held back within the interval")
	}
	assert.Equal(t, 2, builds, "a change held back isn't built")
	assert.True(t, th.Held("a"))
	assert.Equal(t, clock.Now().Add(100*time.Millisecond), th.Next())

	clock.Advance(50 * time.Millisecond)
	_, ok = th.Changed("b", text("b", "x"))
	assert.True(t, ok)
	_, ok = th.Changed("b", text("b", "xy"))
	assert.False(t, ok)
	assert.Empty(t, th.Flush(false), "nothing is due yet")

	clock.Advance(50 * time.Millisecond)
	flushed := th.Flush(false)
	require.Len(t, flushed, 1)
	assert.Equal(t, "Hel", flushed[0].Content[0].Text, "the latest change")
	assert.False(t, th.Held("a"))
	assert.Equal(t, clock.Now().Add(50*time.Millisecond), th.Next(), "b's change is due next")

	msg, ok = th.Send("b", text("b", "xyz"))
	require.True(t, ok, "sent at once, in place of the change held back")
	assert.Equal(t, "xyz", msg.Content[0].Text)
	assert.True(t, th.Next().IsZero())
	assert.Empty(t, th.Flush(true))

	_, ok = th.Changed("a", text("a", "Hello"))
	assert.False(t, ok)
	flushed = th.Flush(true)
	require.Len(t, flushed, 1, "all of them once the output ended")
	assert.Equal(t, "Hello", flushed[0].Content[0].Text)

	_, _ = th.Changed("a", text("a", "Hello!"))
	th.Forget("a")
	assert.False(t, th.Sent("a"))
	assert.Empty(t, th.Flush(true), "a message forgotten drops its change")
	assert.True(t, th.Next().IsZero())
	_, ok = th.Changed("a", text("a", "again"))
	assert.True(t, ok, "and starts over")
	th.Reset()
	assert.False(t, th.Sent("a"))
}

func TestInterruptDeadline(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := agentio.NewInterruptDeadline(clock, 10*time.Second)
	assert.True(t, d.Next().IsZero())
	assert.False(t, d.Due())
	d.Start()
	start := clock.Now()
	clock.Advance(4 * time.Second)
	d.Start()
	assert.Equal(t, start.Add(10*time.Second), d.Next(), "a second interrupt doesn't move it")
	clock.Advance(6*time.Second - time.Millisecond)
	assert.False(t, d.Due())
	clock.Advance(time.Millisecond)
	assert.True(t, d.Due())
	d.Stop()
	assert.False(t, d.Due())
	assert.True(t, d.Next().IsZero())
}

func TestEarliest(t *testing.T) {
	t.Parallel()
	now := time.Now()
	assert.True(t, agentio.Earliest().IsZero())
	assert.True(t, agentio.Earliest(time.Time{}, time.Time{}).IsZero())
	assert.Equal(t, now, agentio.Earliest(time.Time{}, now.Add(time.Second), now))
}
