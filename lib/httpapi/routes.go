package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

func (s *Server) NormalizeSchema(schema any) any {
	switch val := (schema).(type) {
	case *any:
		s.NormalizeSchema(*val)
	case []any:
		for i := range val {
			s.NormalizeSchema(&val[i])
		}
		sort.SliceStable(val, func(i, j int) bool {
			return fmt.Sprintf("%v", val[i]) < fmt.Sprintf("%v", val[j])
		})
	case map[string]any:
		for k := range val {
			valUnderKey := val[k]
			s.NormalizeSchema(&valUnderKey)
			val[k] = valUnderKey
		}
	}
	return schema
}

func (s *Server) GetOpenAPI() string {
	jsonBytes, err := s.api.OpenAPI().Downgrade()
	if err != nil {
		return ""
	}
	// unmarshal the json and pretty print it
	var jsonObj any
	if err := json.Unmarshal(jsonBytes, &jsonObj); err != nil {
		return ""
	}

	// Normalize
	normalized := s.NormalizeSchema(jsonObj)

	prettyJSON, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return ""
	}
	return string(prettyJSON)
}

// guard wraps an endpoint's handler so that check, when it fails, answers
// instead: endpoints of one transport (requireNative), or of agents whose
// MCP configuration AgentAPI manages (requireMCP), say so in the route
// table.
func guard[I, O any](check func() error, handler func(context.Context, *I) (*O, error)) func(context.Context, *I) (*O, error) {
	return func(ctx context.Context, input *I) (*O, error) {
		if err := check(); err != nil {
			return nil, err
		}
		return handler(ctx, input)
	}
}

// registerRoutes sets up all API endpoints
func (s *Server) registerRoutes() {
	s.registerAuthRoutes()
	s.registerNativeRoutes()
	s.registerLimitRoutes()
	s.registerSessionRoutes()
	s.registerSubagentRoutes()
	s.registerFileRoutes()
	s.registerFilePreviewRoutes()
	s.registerTerminalRoutes()
	s.registerHandoffRoutes()
	s.registerConversationRoutes()
	s.registerTaskRoutes()
	s.registerWebhookRoutes()
	s.registerMCPRoutes()
	s.registerQueueRoutes()
	s.registerUploadRoutes()
	s.registerEventRoutes()

	s.router.Handle("/", http.HandlerFunc(s.redirectToChat))

	// Serve static files for the chat interface under /chat
	s.registerStaticFileRoutes()
}

// registerStaticFileRoutes sets up routes for serving static files
func (s *Server) registerStaticFileRoutes() {
	chatHandler := FileServerWithIndexFallback(s.chatBasePath)

	// Mount the file server at /chat
	s.router.Handle("/chat", http.StripPrefix("/chat", chatHandler))
	s.router.Handle("/chat/*", http.StripPrefix("/chat", chatHandler))
}

func (s *Server) redirectToChat(w http.ResponseWriter, r *http.Request) {
	rdir, err := url.JoinPath(s.chatBasePath, "embed")
	if err != nil {
		s.logger.Error("Failed to construct redirect URL", "error", err)
		http.Error(w, "Failed to redirect", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, rdir, http.StatusTemporaryRedirect)
}
