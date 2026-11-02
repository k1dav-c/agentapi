package agentlog_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/agentlog/pilog"
)

func TestRecords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		format agentlog.Format
		line   string
		want   agentlog.Record
	}{
		{claudelog.Format, `{"parentUuid":null,"message":{"role":"user","content":"hi"},"type":"user","timestamp":"2026-10-20T10:00:00Z","version":"2.1.302"}`, agentlog.Record{Type: "user", Known: true, Valid: true, Version: "2.1.302"}},
		{claudelog.Format, `{"type":"x-new-record","sessionId":"s1"}`, agentlog.Record{Type: "x-new-record", Valid: true}},
		{claudelog.Format, `{"type":"assistant","message":{"content":[{"type":"text","text":"cut`, agentlog.Record{Type: "assistant", Known: true}},
		{codexlog.Format, `{"timestamp":"2026-10-20T10:00:00Z","type":"session_meta","payload":{"id":"t1","cli_version":"0.163.0"}}`, agentlog.Record{Type: "session_meta", Known: true, Valid: true, Version: "0.163.0"}},
		{codexlog.Format, `{"timestamp":"2026-10-20T10:00:00Z","type":"world_state","payload":{"full":true}}`, agentlog.Record{Type: "world_state", Known: true, Valid: true}},
		{codexlog.Format, `{"timestamp":"2026-10-20T10:00:00Z","type":"new_kind","payload":{}}`, agentlog.Record{Type: "new_kind", Valid: true}},
		{pilog.Format, `{"type":"session","version":3,"id":"p1","cwd":"/w"}`, agentlog.Record{Type: "session", Known: true, Valid: true, Version: "session format 3"}},
		{pilog.Format, `not json`, agentlog.Record{}},
	} {
		assert.Equal(t, tc.want, tc.format.Record([]byte(tc.line)), tc.line)
	}
}

func TestDriftIsReportedOncePerLog(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	d := agentlog.NewDrift(claudelog.Format)
	d.Line([]byte(`{"type":"user","message":{"role":"user","content":"hi"},"version":"2.1.302"}`))
	assert.False(t, d.Report(logger, "/logs/s1.jsonl"), "nothing to report")
	d.Line([]byte(`{"type":"x-new-record"}`))
	d.Line([]byte(`{"type":"x-new-record"}`))
	d.Line([]byte(`{"type":"assistant","message":{"content":"cut`))
	d.Line([]byte(`{"type":"x-other"}`))
	assert.True(t, d.Report(logger, "/logs/s1.jsonl"))
	assert.Contains(t, out.String(), "level=WARN")
	assert.Contains(t, out.String(), "agent=claude version=2.1.302 path=/logs/s1.jsonl")
	assert.Contains(t, out.String(), `unknown_record_types="\"x-new-record\": 2, \"x-other\": 1" undecodable_lines=1`)

	out.Reset()
	d.Line([]byte(`{"type":"x-third"}`))
	assert.False(t, d.Report(logger, "/logs/s1.jsonl"), "once per log")
	assert.Empty(t, out.String())
}

// Each kind of drift is reported once per log: the records, and each way
// the record of a model call reads wrong.
func TestDriftReportsEachKindOnce(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	d := agentlog.NewDrift(claudelog.Format)
	d.Line([]byte(`{"type":"user","message":{"role":"user","content":"hi"},"version":"2.9.0"}`))
	d.Call(agentlog.NoTokens)
	d.Call(agentlog.NoTokens)
	assert.True(t, d.Report(logger, "/logs/s1.jsonl"))
	assert.Equal(t, 1, strings.Count(out.String(), "level=WARN"))
	assert.Contains(t, out.String(), `msg="Session log has model calls whose usage reads as no tokens; the agent may have changed its format, and they count no cost" agent=claude version=2.9.0 path=/logs/s1.jsonl calls=2`)
	assert.Equal(t, 2, d.Calls(agentlog.NoTokens))

	out.Reset()
	d.Call(agentlog.NoTokens)
	assert.False(t, d.Report(logger, "/logs/s1.jsonl"), "once per kind")
	d.Call(agentlog.NoModelOrUsage)
	d.Line([]byte(`{"type":"x-new-record"}`))
	assert.True(t, d.Report(logger, "/logs/s1.jsonl"))
	assert.Equal(t, 2, strings.Count(out.String(), "level=WARN"), "the records, and calls without a model or usage")
	assert.Contains(t, out.String(), `msg="Session log has model calls without a model or usage; the agent may have changed its format, and they count no cost" agent=claude version=2.9.0 path=/logs/s1.jsonl calls=1`)

	var none *agentlog.Drift
	none.Call(agentlog.NoTokens)
	assert.Zero(t, none.Calls(agentlog.NoTokens), "a nil Drift counts nothing")
}

// A field of another type is left zero; the rest is read.
func TestUnmarshalKeepsTheRest(t *testing.T) {
	t.Parallel()
	var v struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	}
	require.NoError(t, agentlog.Unmarshal([]byte(`{"input":{"total":5},"output":7}`), &v))
	assert.Equal(t, 0, v.Input)
	assert.Equal(t, 7, v.Output)
	assert.Error(t, agentlog.Unmarshal([]byte(`{"input":`), &v))
}
