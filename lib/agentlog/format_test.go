package agentlog_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

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
