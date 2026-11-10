package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/transcript"
)

// The lists of the conversation (GET /messages, /rich-messages, /timeline,
// /tasks) are read a page of tasks at a time: the latest ones without a
// cursor, as the full /events replay holds them, and earlier ones with
// before. The emitter holds the rich messages and timeline events of the
// latest tasks only, with a state database (convstate's hot window); a
// page of earlier ones is read from it.

// pageOf returns what a list response says of the page r.
func pageOf(r convstate.PageRange) Page {
	page := Page{HasMore: r.More}
	if r.More {
		page.NextBefore = r.From
	}
	return page
}

// richMessagesIn returns the rich messages of the page r: from memory, or
// from the state database for earlier tasks.
func (s *Server) richMessagesIn(r convstate.PageRange) ([]transcript.RichMessage, error) {
	if msgs, ok := s.emitter.RichMessagesIn(r); ok || s.stateDB == nil {
		return msgs, nil
	}
	stored, err := s.stateDB.RichMessagesOf(r.From, r.Before, r.Prelude)
	if err != nil {
		return nil, err
	}
	msgs := stored[:0]
	for _, msg := range stored {
		if r.HoldsRich(msg.TaskID) {
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

// sessionEventsIn returns the timeline events of the page r: from memory,
// or from the state database for earlier tasks.
func (s *Server) sessionEventsIn(r convstate.PageRange) ([]transcript.SessionEvent, error) {
	if events, ok := s.emitter.SessionEventsIn(r); ok || s.stateDB == nil {
		return events, nil
	}
	stored, err := s.stateDB.SessionEventsOf(r.FromTime, r.BeforeTime, r.Prelude)
	if err != nil {
		return nil, err
	}
	events := stored[:0]
	for _, event := range stored {
		if r.HoldsEvent(event.EventTime) {
			events = append(events, event)
		}
	}
	return events, nil
}

// TimelineExportRequest is GET /timeline/export's query.
type TimelineExportRequest struct {
	Subagents bool `query:"subagents" doc:"Also stream the events of the subagents the agent ran in the session (native Claude Code and Codex), after the agent's own."`
}

// exportTimeline handles GET /timeline/export: the whole timeline as JSON
// Lines, read from the state database a part at a time when there is one.
func (s *Server) exportTimeline(ctx context.Context, input *TimelineExportRequest) (*huma.StreamResponse, error) {
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Content-Type", "application/x-ndjson")
		hctx.SetStatus(http.StatusOK)
		w := hctx.BodyWriter()
		line := func(data []byte) error {
			if _, err := w.Write(data); err != nil {
				return err
			}
			_, err := io.WriteString(w, "\n")
			return err
		}
		var err error
		if s.stateDB != nil {
			err = s.stateDB.EachSessionEvent(line)
		} else {
			for _, event := range s.emitter.SessionEvents() {
				if err = writeJSONLine(line, event); err != nil {
					break
				}
			}
		}
		if err == nil && input.Subagents && s.sessions != nil {
			for _, event := range s.subagentEvents(s.emitter.NextEventID()) {
				if err = writeJSONLine(line, event); err != nil {
					break
				}
			}
		}
		if err != nil {
			// The status went out with the first line: the client sees a
			// cut download.
			s.logger.Warn("Failed to export the timeline", "error", err)
		}
	}}, nil
}

// writeJSONLine writes v as a line of JSON with line.
func writeJSONLine(line func([]byte) error, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return line(data)
}
