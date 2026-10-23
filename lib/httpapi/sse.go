package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
)

// sseHeartbeatInterval is how often a heartbeat event is sent to each
// /events subscriber so clients can detect silently dead connections.
const sseHeartbeatInterval = 15 * time.Second

// eventSeq is the seq of a change event, 0 for other events.
func eventSeq(event Event) uint64 {
	switch body := event.Payload.(type) {
	case MessageUpdateBody:
		return body.Seq
	case RichMessageUpdateBody:
		return body.Seq
	case RequestUpdateBody:
		return body.Seq
	}
	return 0
}

// SubscribeEventsInput lets a client that cached the conversation resume
// from where it left off.
type SubscribeEventsInput struct {
	Sync  bool   `query:"sync" doc:"Opt in to session_sync events and incremental replay. Clients that don't opt in get the full replay and no session_sync events, as before."`
	Since uint64 `query:"since" doc:"With sync: highest seq the client has applied. With a matching epoch, the replay holds only later changes."`
	Epoch string `query:"epoch" doc:"With sync: epoch from the client's last session_sync. If it doesn't match, the full state is replayed."`
	// LastEventID is sent by EventSource when it reconnects by itself.
	LastEventID uint64 `header:"Last-Event-ID" doc:"With sync: used as since when since is not given. With sync, events carry SSE ids: the last event of the replay has the replay's seq and each later message, rich message or request update has its own, so a reconnecting EventSource resumes where it left off (pass epoch in the URL, e.g. from GET /status)."`
}

// subscribeEvents is an SSE endpoint that sends events to the client.
func (s *Server) subscribeEvents(ctx context.Context, input *SubscribeEventsInput, send sse.Sender) {
	// session_sync is opt-in: existing clients (e.g. agentapi-sdk-go) reject
	// event types they don't know.
	sync := input.Sync
	since, epoch := input.Since, input.Epoch
	if since == 0 {
		since = input.LastEventID
	}
	if !sync {
		since, epoch = 0, ""
	}
	wants := func(t EventType) bool { return ConversationEvents(t) && (sync || t != EventTypeSessionSync) }
	subscriberId, ch, replay := s.emitter.SubscribeTo(wants, since, epoch)
	defer s.emitter.Unsubscribe(subscriberId)

	s.logger.Info("New subscriber", "subscriberId", subscriberId)
	var replaySeq uint64
	if len(replay) > 0 {
		if body, ok := replay[0].Payload.(SessionSyncBody); ok {
			replaySeq = body.Seq
		}
	}
	for i, event := range replay {
		msg := sse.Message{Data: event.Payload}
		// Replayed changes are not in seq order, so only the end of the
		// replay says how far the client is.
		if sync && i == len(replay)-1 {
			msg.ID = int(replaySeq)
		}
		if err := send(msg); err != nil {
			s.logger.Error("Failed to send event", "subscriberId", subscriberId, "error", err)
			return
		}
	}

	heartbeat := s.clock.NewTicker(sseHeartbeatInterval, "sseHeartbeat")
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				s.logger.Info("Channel closed", "subscriberId", subscriberId)
				return
			}
			msg := sse.Message{Data: event.Payload}
			if sync {
				msg.ID = int(eventSeq(event))
			}
			if err := send(msg); err != nil {
				s.logger.Error("Failed to send event", "subscriberId", subscriberId, "error", err)
				return
			}
		case <-heartbeat.C:
			if err := send.Data(HeartbeatBody{Time: s.clock.Now()}); err != nil {
				s.logger.Error("Failed to send heartbeat", "subscriberId", subscriberId, "error", err)
				return
			}
		case <-s.shutdownCtx.Done():
			s.logger.Info("Server stop initiated, unsubscribing.", "subscriberId", subscriberId)
			return
		case <-ctx.Done():
			s.logger.Info("Context done", "subscriberId", subscriberId)
			return
		}
	}
}

func (s *Server) subscribeScreen(ctx context.Context, input *struct{}, send sse.Sender) {
	subscriberId, ch, stateEvents := s.emitter.SubscribeTo(ScreenEvents, 0, "")
	defer s.emitter.Unsubscribe(subscriberId)
	s.logger.Info("New screen subscriber", "subscriberId", subscriberId)
	for _, event := range stateEvents {
		if err := send.Data(event.Payload); err != nil {
			s.logger.Error("Failed to send screen event", "subscriberId", subscriberId, "error", err)
			return
		}
	}
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				s.logger.Info("Screen channel closed", "subscriberId", subscriberId)
				return
			}
			if err := send.Data(event.Payload); err != nil {
				s.logger.Error("Failed to send screen event", "subscriberId", subscriberId, "error", err)
				return
			}
		case <-s.shutdownCtx.Done():
			s.logger.Info("Server stop initiated, unsubscribing.", "subscriberId", subscriberId)
			return
		case <-ctx.Done():
			s.logger.Info("Screen context done", "subscriberId", subscriberId)
			return
		}
	}
}

func (s *Server) registerEventRoutes() {
	// GET /events endpoint
	sse.Register(s.api, huma.Operation{
		OperationID: "subscribeEvents",
		Method:      http.MethodGet,
		Path:        "/events",
		Summary:     "Subscribe to events",
		Description: "The events are sent as Server-Sent Events (SSE). Initially, the endpoint returns a list of events needed to reconstruct the current state of the conversation and the agent's status. After that, it only returns events that have occurred since the last event was sent.\n\nThe server ends the stream when the client falls too far behind, and when the conversation changes in a way updates can't express (a message was taken back), which gives it a new epoch. Reconnect to get the state again: with sync, the old epoch gets the full state.\n\nNote: When an agent is running, the last message in the conversation history is updated frequently, and the endpoint sends a new message update event each time.",
		Middlewares: []func(huma.Context, func(huma.Context)){sseMiddleware},
	}, map[string]any{
		// Mapping of event type name to Go struct for that event.
		"message_update":      MessageUpdateBody{},
		"status_change":       StatusChangeBody{},
		"agent_error":         ErrorBody{},
		"rich_message_update": RichMessageUpdateBody{},
		"heartbeat":           HeartbeatBody{},
		"session_sync":        SessionSyncBody{},
		"request_update":      RequestUpdateBody{},
	}, s.subscribeEvents)

	sse.Register(s.api, huma.Operation{
		OperationID: "subscribeScreen",
		Method:      http.MethodGet,
		Path:        "/internal/screen",
		Summary:     "Subscribe to screen",
		Hidden:      true,
		Middlewares: []func(huma.Context, func(huma.Context)){sseMiddleware},
	}, map[string]any{
		"screen": ScreenUpdateBody{},
	}, s.subscribeScreen)
}
