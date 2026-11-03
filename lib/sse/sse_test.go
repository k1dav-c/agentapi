package sse_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/sse"
)

func readAll(t *testing.T, stream string, maxSize int) ([]sse.Event, error) {
	t.Helper()
	r := sse.NewReader(strings.NewReader(stream), maxSize)
	var events []sse.Event
	for {
		event, err := r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return events, nil
			}
			return events, err
		}
		events = append(events, event)
	}
}

// An event's data is all its data: lines, joined by newlines; comments,
// unknown fields and events without data are skipped; the last event ID
// carries over; a cut-short event at the end is dropped.
func TestReader(t *testing.T) {
	stream := "\ufeff: comment\n" +
		"event: summary_update\ndata: {\"a\":\ndata:  1}\nid: 7\n\n" +
		"event: nothing\nretry: 10\n\n" +
		"data\n\n" +
		"data: one\r\ndata: two\rdata: three\r\n\r\n" +
		"event: cut\ndata: short"
	events, err := readAll(t, stream, 1<<10)
	require.NoError(t, err)
	assert.Equal(t, []sse.Event{
		{Type: "summary_update", Data: "{\"a\":\n 1}", ID: "7"},
		{Type: "message", Data: "", ID: "7"},
		{Type: "message", Data: "one\ntwo\nthree", ID: "7"},
	}, events)
}

func TestReaderTooLong(t *testing.T) {
	_, err := readAll(t, "data: 0123456789\ndata: 0123456789\n\n", 16)
	assert.ErrorIs(t, err, sse.ErrTooLong)
	_, err = readAll(t, "data: "+strings.Repeat("x", 64)+"\n\n", 16)
	assert.ErrorIs(t, err, sse.ErrTooLong)
}

// What Write writes, Reader reads back as it was.
func TestWriteRoundTrip(t *testing.T) {
	var b bytes.Buffer
	require.NoError(t, sse.Write(&b, "instance", []byte("line 1\nline 2\r\n\nline 4")))
	require.NoError(t, sse.Write(&b, "", []byte(`{"x":1}`)))
	assert.Equal(t, "event: instance\ndata: line 1\ndata: line 2\ndata: \ndata: line 4\n\ndata: {\"x\":1}\n\n", b.String())
	events, err := readAll(t, b.String(), 1<<10)
	require.NoError(t, err)
	assert.Equal(t, []sse.Event{
		{Type: "instance", Data: "line 1\nline 2\n\nline 4"},
		{Type: "message", Data: `{"x":1}`},
	}, events)
	assert.Error(t, sse.Write(&b, "a\nb", nil))
}
