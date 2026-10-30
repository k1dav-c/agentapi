package agentio_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native/agentio"
)

func TestDecode(t *testing.T) {
	t.Parallel()
	var v struct {
		Type  string `json:"type"`
		Count int    `json:"count"`
		Inner struct {
			Done bool `json:"done"`
			Name string
		} `json:"inner"`
	}
	changed, err := agentio.Decode([]byte(`{"type":"turn","count":"3","inner":{"done":"yes","name":"n"}}`), &v)
	require.NoError(t, err, "a field of another type doesn't lose the record")
	require.NotNil(t, changed)
	assert.Equal(t, "count", changed.Field, "the first mismatch is reported")
	assert.Equal(t, "turn", v.Type)
	assert.Equal(t, "n", v.Inner.Name)

	_, err = agentio.Decode([]byte(`[1,2]`), &v)
	assert.Error(t, err, "a value that doesn't fit at all")
	_, err = agentio.Decode([]byte(`{"type":`), &v)
	assert.Error(t, err)
	changed, err = agentio.Decode([]byte(`{"type":"turn"}`), &v)
	assert.NoError(t, err)
	assert.Nil(t, changed)
}

func TestDrift(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	d := agentio.NewDrift("Codex", slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	d.SetVersion("0.160.0")
	var v struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
		At int64 `json:"at"`
	}
	for range 3 {
		require.NoError(t, d.Decode("turn/completed", []byte(`{"turn":{"id":"t1"},"at":"soon"}`), &v))
		assert.Equal(t, "t1", v.Turn.ID)
	}
	d.Unknown("turn/paused")
	d.Unknown("turn/paused")
	require.Error(t, d.Decode("turn/started", []byte(`"turn"`), &v))

	assert.Equal(t, map[string]int{
		"changed turn/completed at": 3,
		"unknown turn/paused":       2,
		"undecodable turn/started":  1,
	}, d.Counts())
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3, "each kind warns once: %s", buf.String())
	assert.Contains(t, lines[0], `field=at value=string`)
	assert.Contains(t, lines[0], `version=0.160.0`)
	assert.Contains(t, lines[1], `type=turn/paused`)
	assert.Contains(t, lines[2], `type=turn/started`)

	var none *agentio.Drift
	none.Unknown("x")
	require.NoError(t, none.Decode("x", []byte(`{"at":"soon"}`), &v), "a nil Drift decodes and counts nothing")
	assert.Empty(t, none.Counts())
}
