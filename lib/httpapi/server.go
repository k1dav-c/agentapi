package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/mcpconfig"
	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/x/acpio"
	"github.com/coder/quartz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

const (
	// messageQueueDispatchInterval is how often the queue dispatch loop
	// checks whether the head of the queue can be sent to the agent.
	messageQueueDispatchInterval = 500 * time.Millisecond
	// sseHeartbeatInterval is how often a heartbeat event is sent to each
	// /events subscriber so clients can detect silently dead connections.
	sseHeartbeatInterval = 15 * time.Second
	// maxQueueDispatchAttempts is how many consecutive non-transient send
	// failures are tolerated before a queued message is dropped.
	maxQueueDispatchAttempts = 5
)

// Server represents the HTTP server
type Server struct {
	router             chi.Router
	api                huma.API
	port               int
	srv                *http.Server
	mu                 sync.RWMutex
	stopOnce           sync.Once
	logger             *slog.Logger
	conversation       st.Conversation
	agentio            st.AgentIO
	agentType          mf.AgentType
	emitter            *EventEmitter
	chatBasePath       string
	tempDir            string
	cwd                string
	clock              quartz.Clock
	shutdownCtx        context.Context
	shutdown           context.CancelFunc
	transport          Transport
	messageQueue       []QueuedMessage
	mcpStore           mcpconfig.Store
	mcpMu              sync.Mutex
	webhook            *webhookDispatcher
	restartAgent       func(context.Context) (int, error)
	jsonlWatcherCancel context.CancelFunc
	jsonlParentCtx     context.Context // parent context for spawning new JSONL watchers
	nextQueueID        int
	// Consecutive non-validation dispatch failures for the queued message
	// identified by queueFailID. Used to drop poison messages.
	queueFailID    int
	queueFailCount int
}

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

// That's about 40 frames per second. It's slightly less
// because the action of taking a snapshot takes time too.
const snapshotInterval = 25 * time.Millisecond

type ServerConfig struct {
	AgentType              mf.AgentType
	AgentIO                st.AgentIO
	Transport              Transport
	Port                   int
	ChatBasePath           string
	AllowedHosts           []string
	AllowedOrigins         []string
	InitialPrompt          string
	Clock                  quartz.Clock
	StatePersistenceConfig st.StatePersistenceConfig
	AgentPID               int // PID of the agent process, 0 to disable JSONL watcher
	AgentStartedAt         time.Time
	CWD                    string // Working directory (used by Codex resolver)
	// RestartAgent replaces the PTY agent process while keeping AgentAPI alive.
	// It returns the new process PID so the JSONL watcher can be restarted.
	// It is nil for transports or server modes that cannot restart.
	RestartAgent func(context.Context) (int, error)
	// Webhook sends an event whenever the agent status changes.
	Webhook WebhookConfig
	// APIToken, if non-empty, enables Bearer token authentication on all API
	// endpoints. Static file routes (/chat/*, /) are exempt so browsers can
	// load the chat UI without a token.
	APIToken string
}

// Validate allowed hosts don't contain whitespace, commas, schemes, or ports.
// Viper/Cobra use different separators (space for env vars, comma for flags),
// so these characters likely indicate user error.
func parseAllowedHosts(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("the list must not be empty")
	}
	if slices.Contains(input, "*") {
		return []string{"*"}, nil
	}
	// First pass: whitespace & comma checks (surface these errors first)
	// Viper/Cobra use different separators (space for env vars, comma for flags),
	// so these characters likely indicate user error.
	for _, item := range input {
		for _, r := range item {
			if unicode.IsSpace(r) {
				return nil, fmt.Errorf("'%s' contains whitespace characters, which are not allowed", item)
			}
		}
		if strings.Contains(item, ",") {
			return nil, fmt.Errorf("'%s' contains comma characters, which are not allowed", item)
		}
	}
	// Second pass: scheme check
	for _, item := range input {
		if strings.Contains(item, "http://") || strings.Contains(item, "https://") {
			return nil, fmt.Errorf("'%s' must not include http:// or https://", item)
		}
	}
	hosts := make([]*url.URL, 0, len(input))
	// Third pass: url parse
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		u, err := url.Parse("http://" + trimmed)
		if err != nil {
			return nil, fmt.Errorf("'%s' is not a valid host: %w", item, err)
		}
		hosts = append(hosts, u)
	}
	// Fourth pass: port check
	for _, u := range hosts {
		if u.Port() != "" {
			return nil, fmt.Errorf("'%s' must not include a port", u.Host)
		}
	}
	hostStrings := make([]string, 0, len(hosts))
	for _, u := range hosts {
		hostStrings = append(hostStrings, u.Hostname())
	}
	return hostStrings, nil
}

// Validate allowed origins
func parseAllowedOrigins(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("the list must not be empty")
	}
	if slices.Contains(input, "*") {
		return []string{"*"}, nil
	}
	// Viper/Cobra use different separators (space for env vars, comma for flags),
	// so these characters likely indicate user error.
	for _, item := range input {
		for _, r := range item {
			if unicode.IsSpace(r) {
				return nil, fmt.Errorf("'%s' contains whitespace characters, which are not allowed", item)
			}
		}
		if strings.Contains(item, ",") {
			return nil, fmt.Errorf("'%s' contains comma characters, which are not allowed", item)
		}
	}
	origins := make([]string, 0, len(input))
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		u, err := url.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("'%s' is not a valid origin: %w", item, err)
		}
		origins = append(origins, fmt.Sprintf("%s://%s", u.Scheme, u.Host))
	}
	return origins, nil
}

// NewServer creates a new server instance
func NewServer(ctx context.Context, config ServerConfig) (*Server, error) {
	router := chi.NewMux()

	logger := logctx.From(ctx)

	if config.Clock == nil {
		config.Clock = quartz.NewReal()
	}

	allowedHosts, err := parseAllowedHosts(config.AllowedHosts)
	if err != nil {
		return nil, fmt.Errorf("failed to parse allowed hosts: %w", err)
	}
	allowedOrigins, err := parseAllowedOrigins(config.AllowedOrigins)
	if err != nil {
		return nil, fmt.Errorf("failed to parse allowed origins: %w", err)
	}

	logger.Info(fmt.Sprintf("Allowed hosts: %s", strings.Join(allowedHosts, ", ")))
	logger.Info(fmt.Sprintf("Allowed origins: %s", strings.Join(allowedOrigins, ", ")))

	// Enforce allowed hosts in a custom middleware that ignores the port during matching.
	badHostHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid host header. Allowed hosts: "+strings.Join(allowedHosts, ", "), http.StatusBadRequest)
	})
	router.Use(hostAuthorizationMiddleware(allowedHosts, badHostHandler))

	if config.APIToken != "" {
		logger.Info("API token authentication enabled")
		router.Use(tokenAuthMiddleware(config.APIToken))
	}

	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	})
	router.Use(corsMiddleware.Handler)

	humaConfig := huma.DefaultConfig("AgentAPI", version.Version)
	humaConfig.Info.Description = "HTTP API for Claude Code, Goose, and Aider.\n\nhttps://github.com/coder/agentapi"
	api := humachi.New(router, humaConfig)
	formatMessage := func(message string, userInput string) string {
		return mf.FormatAgentMessage(config.AgentType, message, userInput)
	}

	isAgentReadyForInitialPrompt := func(message string) bool {
		return mf.IsAgentReadyForInitialPrompt(config.AgentType, message)
	}

	formatToolCall := func(message string) (string, []string) {
		return mf.FormatToolCall(config.AgentType, message)
	}

	webhook, err := newWebhookDispatcher(config.Webhook, logger, config.AgentType, config.Transport)
	if err != nil {
		return nil, fmt.Errorf("configure webhook: %w", err)
	}
	emitterOptions := []EventEmitterOption{
		WithAgentType(config.AgentType),
		WithClock(config.Clock),
		WithStatusChangeHandler(webhook.statusChanged),
	}
	emitter := NewEventEmitter(emitterOptions...)

	// Format initial prompt into message parts if provided
	var initialPrompt []st.MessagePart
	if config.InitialPrompt != "" {
		initialPrompt = FormatMessage(config.AgentType, config.InitialPrompt)
	}

	var conversation st.Conversation
	if config.Transport == TransportACP {
		// For ACP, cast AgentIO to *acpio.ACPAgentIO
		acpIO, ok := config.AgentIO.(*acpio.ACPAgentIO)
		if !ok {
			return nil, fmt.Errorf("ACP transport requires ACPAgentIO")
		}
		conversation = acpio.NewACPConversation(ctx, acpIO, logger, initialPrompt, emitter, config.Clock)
	} else {
		conversation = st.NewPTY(ctx, st.PTYConversationConfig{
			AgentType:              config.AgentType,
			AgentIO:                config.AgentIO,
			Clock:                  config.Clock,
			SnapshotInterval:       snapshotInterval,
			ScreenStabilityLength:  2 * time.Second,
			FormatMessage:          formatMessage,
			ReadyForInitialPrompt:  isAgentReadyForInitialPrompt,
			FormatToolCall:         formatToolCall,
			InitialPrompt:          initialPrompt,
			Logger:                 logger,
			StatePersistenceConfig: config.StatePersistenceConfig,
		}, emitter)
	}

	// Create temporary directory for uploads
	tempDir, err := os.MkdirTemp("", "agentapi-uploads-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	logger.Info("Created temporary directory for uploads", "tempDir", tempDir)

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	webhook.start(shutdownCtx)

	s := &Server{
		router:         router,
		api:            api,
		port:           config.Port,
		conversation:   conversation,
		logger:         logger,
		agentio:        config.AgentIO,
		agentType:      config.AgentType,
		emitter:        emitter,
		chatBasePath:   strings.TrimSuffix(config.ChatBasePath, "/"),
		tempDir:        tempDir,
		cwd:            config.CWD,
		clock:          config.Clock,
		shutdownCtx:    shutdownCtx,
		shutdown:       shutdownCancel,
		transport:      config.Transport,
		webhook:        webhook,
		restartAgent:   config.RestartAgent,
		jsonlParentCtx: ctx,
	}
	if mcpconfig.SupportedAgent(config.AgentType) {
		store, err := mcpconfig.NewStore(config.AgentType, config.CWD)
		if err != nil {
			return nil, fmt.Errorf("create MCP config store: %w", err)
		}
		s.mcpStore = store
	}

	// Register API routes
	s.registerRoutes()

	// Start the conversation polling loop if we have an agent IO.
	// AgentIO is nil only when --print-openapi is used (no agent runs).
	// For PTY transport, the process is already running at this point -
	// termexec.StartProcess() blocks until the PTY is created and the process
	// is active. Agent readiness (waiting for the prompt) is handled
	// asynchronously inside conversation.Start() via ReadyForInitialPrompt.
	if config.AgentIO != nil {
		s.conversation.Start(ctx)
		s.startMessageQueue()
	}

	// Start the JSONL watcher to capture rich structured messages.
	// This runs alongside the PTY conversation as a sidecar, providing
	// structured content blocks, tool calls, thinking, and usage data.
	s.startJSONLWatcher(config.AgentPID)

	return s, nil
}

// Handler returns the underlying chi.Router for testing purposes.
func (s *Server) Handler() http.Handler {
	return s.router
}

// hostAuthorizationMiddleware enforces that the request Host header matches one of the allowed
// hosts, ignoring any port in the comparison. If allowedHosts is empty, all hosts are allowed.
// Always uses url.Parse("http://" + r.Host) to robustly extract the hostname (handles IPv6).
func hostAuthorizationMiddleware(allowedHosts []string, badHostHandler http.Handler) func(next http.Handler) http.Handler {
	// Copy for safety; also build a map for O(1) lookups with case-insensitive keys.
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, h := range allowedHosts {
		allowed[strings.ToLower(h)] = struct{}{}
	}
	wildcard := slices.Contains(allowedHosts, "*")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard { // wildcard semantics: allow all
				next.ServeHTTP(w, r)
				return
			}
			// Extract hostname from the Host header using url.Parse; ignore any port.
			hostHeader := r.Host
			if hostHeader == "" {
				badHostHandler.ServeHTTP(w, r)
				return
			}
			if u, err := url.Parse("http://" + hostHeader); err == nil {
				hostname := u.Hostname()
				if _, ok := allowed[strings.ToLower(hostname)]; ok {
					next.ServeHTTP(w, r)
					return
				}
			}
			badHostHandler.ServeHTTP(w, r)
		})
	}
}

// tokenAuthMiddleware enforces Bearer token authentication on all requests
// except static file routes (/chat/* and /) so browsers can load the UI.
func tokenAuthMiddleware(token string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt static file routes so browsers can open the chat UI.
			path := r.URL.Path
			if path == "/" || strings.HasPrefix(path, "/chat") {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) || strings.TrimPrefix(auth, prefix) != token {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// startJSONLWatcher stops any existing JSONL watcher and starts a new one
// for the given agent PID. It is called once during server creation and
// again after each agent restart so that tool calls, thinking, and usage
// data from the new session are captured.
func (s *Server) startJSONLWatcher(pid int) {
	// Stop the previous watcher, if any.
	if s.jsonlWatcherCancel != nil {
		s.jsonlWatcherCancel()
		s.jsonlWatcherCancel = nil
	}

	if pid <= 0 {
		return
	}

	var resolver jsonlwatcher.SessionResolver
	var parser jsonlwatcher.LineParser
	var sessionEventParser jsonlwatcher.SessionEventParser

	switch s.agentType {
	case mf.AgentTypeClaude:
		resolver = &jsonlwatcher.ClaudeResolver{PID: pid}
		parser = jsonlwatcher.NewClaudeParser()
		sessionEventParser = jsonlwatcher.NewClaudeSessionEventParser()
	case mf.AgentTypeCodex:
		resolver = &jsonlwatcher.CodexResolver{
			PID:       pid,
			CWD:       s.cwd,
			NotBefore: time.Now(),
		}
		parser = jsonlwatcher.NewCodexParser()
		sessionEventParser = jsonlwatcher.NewCodexSessionEventParser()
	}

	if resolver == nil || parser == nil {
		return
	}

	watchCtx, cancel := context.WithCancel(s.jsonlParentCtx)
	s.jsonlWatcherCancel = cancel

	w := jsonlwatcher.New(jsonlwatcher.Config{
		Resolver: resolver,
		Parser:   parser,
		Logger:   s.logger,
		OnMessage: func(msg jsonlwatcher.RichMessage) {
			s.emitter.EmitRichMessage(msg)
		},
		OnLine: func(line []byte) {
			events, err := sessionEventParser.ParseSessionEvents(line)
			if err != nil {
				s.logger.Debug("Failed to normalize session event", "error", err)
				return
			}
			s.emitter.EmitSessionEvents(events)
		},
	})
	go w.Start(watchCtx)
}

// sseMiddleware creates middleware that prevents proxy buffering for SSE endpoints
func sseMiddleware(ctx huma.Context, next func(huma.Context)) {
	// Disable proxy buffering for SSE endpoints
	ctx.SetHeader("Cache-Control", "no-cache, no-store, must-revalidate")
	ctx.SetHeader("Pragma", "no-cache")
	ctx.SetHeader("Expires", "0")
	ctx.SetHeader("X-Accel-Buffering", "no") // nginx
	ctx.SetHeader("X-Proxy-Buffering", "no") // generic proxy
	ctx.SetHeader("Connection", "keep-alive")

	next(ctx)
}

// registerRoutes sets up all API endpoints
func (s *Server) registerRoutes() {
	// GET /status endpoint
	huma.Get(s.api, "/status", s.getStatus, func(o *huma.Operation) {
		o.Description = "Returns the current status of the agent."
	})
	huma.Get(s.api, "/title", s.getTitle, func(o *huma.Operation) {
		o.Description = "Returns the current human-readable session title and the server state used to derive it."
	})

	// GET /messages endpoint
	huma.Get(s.api, "/messages", s.getMessages, func(o *huma.Operation) {
		o.Description = "Returns a list of messages representing the conversation history with the agent."
	})

	// GET /rich-messages endpoint
	huma.Get(s.api, "/rich-messages", s.getRichMessages, func(o *huma.Operation) {
		o.Description = "Returns a list of rich structured messages parsed from the agent's session log. " +
			"Each message contains structured content blocks (text, thinking, tool_use, tool_result), " +
			"model information, and token usage data. Only available for agent types with session log " +
			"support (currently 'claude' and 'codex') running via PTY transport."
	})

	huma.Get(s.api, "/timeline", s.getTimeline, func(o *huma.Operation) {
		o.Description = "Returns all normalized events from the current agent session, including text, thinking, tool calls, tool results, and system lifecycle events."
	})

	huma.Get(s.api, "/webhook", s.getWebhookConfig, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Get webhook configuration"
		o.Description = "Returns the mutable run-status webhook configuration. The signing secret is never returned."
	})
	huma.Put(s.api, "/webhook", s.updateWebhookConfig, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Update webhook configuration"
		o.Description = "Updates run-status webhook delivery immediately. Startup flags provide the initial values; this endpoint can replace or disable them without restarting the agent."
		o.Errors = []int{400}
	})

	huma.Get(s.api, "/usage", s.getUsage, func(o *huma.Operation) {
		o.Tags = []string{"Usage"}
		o.Summary = "Get rate limit usage"
		o.Description = "Returns the current rate limit utilization from the Anthropic API, including 5-hour and 7-day window usage, overage status, and subscription info."
	})

	huma.Get(s.api, "/mcp", s.getMCP, func(o *huma.Operation) {
		configureMCPGetOperation(o)
	})
	huma.Put(s.api, "/mcp", s.updateMCP, func(o *huma.Operation) {
		configureMCPUpdateOperation(o)
	})
	huma.Post(s.api, "/mcp/check", s.checkMCP, configureMCPChildOperation("Check MCP server connectivity"))
	huma.Post(s.api, "/mcp/servers", s.createMCPServer, configureMCPChildOperation("Create an MCP server"))
	huma.Patch(s.api, "/mcp/servers/{name}", s.patchMCPServer, configureMCPChildOperation("Update an MCP server"))
	huma.Delete(s.api, "/mcp/servers/{name}", s.deleteMCPServer, configureMCPChildOperation("Delete an MCP server"))
	huma.Get(s.api, "/mcp/profiles", s.getMCPProfiles, configureMCPChildOperation("List MCP profiles"))
	huma.Put(s.api, "/mcp/profiles/{name}", s.putMCPProfile, configureMCPChildOperation("Import or replace an MCP profile"))
	huma.Delete(s.api, "/mcp/profiles/{name}", s.deleteMCPProfile, configureMCPChildOperation("Delete an MCP profile"))
	huma.Post(s.api, "/mcp/profiles/{name}/apply", s.applyMCPProfile, configureMCPChildOperation("Apply an MCP profile"))
	// Huma populates inferred request/response media types after the operation
	// configuration callback, so attach named payload examples once both
	// operations have been registered.
	mcpPath := s.api.OpenAPI().Paths["/mcp"]
	addMCPExamples(mcpPath.Get, false)
	addMCPExamples(mcpPath.Put, true)

	// POST /message endpoint
	huma.Post(s.api, "/message", s.createMessage, func(o *huma.Operation) {
		o.Description = "Send a message to the agent. User messages are queued when the agent is busy."
	})

	huma.Get(s.api, "/queue", s.getQueue, func(o *huma.Operation) {
		o.Description = "Returns user messages waiting to be sent to the agent."
	})
	huma.Put(s.api, "/queue/{id}", s.updateQueuedMessage, func(o *huma.Operation) {
		o.Description = "Updates a queued user message."
	})
	huma.Delete(s.api, "/queue/{id}", s.deleteQueuedMessage, func(o *huma.Operation) {
		o.Description = "Deletes a queued user message."
	})

	huma.Post(s.api, "/upload", s.uploadFiles, func(o *huma.Operation) {
		o.Description = "Upload files to the specified upload path."
	})

	// GET /events endpoint
	sse.Register(s.api, huma.Operation{
		OperationID: "subscribeEvents",
		Method:      http.MethodGet,
		Path:        "/events",
		Summary:     "Subscribe to events",
		Description: "The events are sent as Server-Sent Events (SSE). Initially, the endpoint returns a list of events needed to reconstruct the current state of the conversation and the agent's status. After that, it only returns events that have occurred since the last event was sent.\n\nNote: When an agent is running, the last message in the conversation history is updated frequently, and the endpoint sends a new message update event each time.",
		Middlewares: []func(huma.Context, func(huma.Context)){sseMiddleware},
	}, map[string]any{
		// Mapping of event type name to Go struct for that event.
		"message_update":      MessageUpdateBody{},
		"status_change":       StatusChangeBody{},
		"agent_error":         ErrorBody{},
		"rich_message_update": RichMessageUpdateBody{},
		"heartbeat":           HeartbeatBody{},
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

	s.router.Handle("/", http.HandlerFunc(s.redirectToChat))

	// Serve static files for the chat interface under /chat
	s.registerStaticFileRoutes()
}

// getStatus handles GET /status
func (s *Server) getStatus(ctx context.Context, input *struct{}) (*StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := s.conversation.Status()
	agentStatus := convertStatus(status)

	resp := &StatusResponse{}
	resp.Body.Status = agentStatus
	resp.Body.AgentType = s.agentType
	resp.Body.Transport = s.transport

	return resp, nil
}

func (s *Server) getTitle(ctx context.Context, input *struct{}) (*TitleResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := convertStatus(s.conversation.Status())
	task := ""
	messages := s.conversation.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == st.ConversationRoleUser {
			task = strings.Join(strings.Fields(messages[i].Message), " ")
			break
		}
	}
	runes := []rune(task)
	if len(runes) > 60 {
		task = strings.TrimSpace(string(runes[:59])) + "…"
	}
	state := "Ready"
	if status == AgentStatusRunning {
		state = "Running"
	}
	title := state + " · AgentAPI"
	if task != "" {
		title = state + " · " + task + " — AgentAPI"
	}

	resp := &TitleResponse{}
	resp.Body.Title = title
	resp.Body.Task = task
	resp.Body.Status = status
	resp.Body.AgentType = s.agentType
	return resp, nil
}

// getMessages handles GET /messages
func (s *Server) getMessages(ctx context.Context, input *struct{}) (*MessagesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resp := &MessagesResponse{}
	msgs := s.conversation.Messages()
	resp.Body.Messages = make([]Message, len(msgs))
	for i, msg := range msgs {
		resp.Body.Messages[i] = Message{
			Id:      msg.Id,
			Role:    msg.Role,
			Content: msg.Message,
			Time:    msg.Time,
		}
	}

	return resp, nil
}

// getRichMessages handles GET /rich-messages
func (s *Server) getRichMessages(ctx context.Context, input *struct{}) (*RichMessagesResponse, error) {
	resp := &RichMessagesResponse{}
	resp.Body.Messages = s.emitter.RichMessages()
	if resp.Body.Messages == nil {
		resp.Body.Messages = []jsonlwatcher.RichMessage{}
	}
	return resp, nil
}

func (s *Server) getTimeline(ctx context.Context, input *struct{}) (*TimelineResponse, error) {
	events := s.emitter.SessionEvents()
	if events == nil {
		events = []jsonlwatcher.SessionEvent{}
	}
	resp := &TimelineResponse{}
	resp.Body.Events = events
	return resp, nil
}

// createMessage handles POST /message
func (s *Server) createMessage(ctx context.Context, input *MessageRequest) (*MessageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := &MessageResponse{}
	switch input.Body.Type {
	case MessageTypeUser:
		if strings.TrimSpace(input.Body.Content) == "" {
			return nil, huma.Error400BadRequest("message must not be empty")
		}
		// Enqueue when earlier messages are still waiting, even if the
		// agent is stable, so messages are delivered in FIFO order.
		if len(s.messageQueue) > 0 || s.conversation.Status() != st.ConversationStatusStable {
			s.enqueueMessageLocked(input.Body.Content)
			resp.Body.Ok = true
			resp.Body.Queued = true
			return resp, nil
		}
		if err := s.conversation.Send(FormatMessage(s.agentType, input.Body.Content)...); err != nil {
			if errors.Is(err, st.ErrMessageValidationChanging) {
				s.enqueueMessageLocked(input.Body.Content)
				resp.Body.Ok = true
				resp.Body.Queued = true
				return resp, nil
			}
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	case MessageTypeRaw:
		if _, err := s.agentio.Write([]byte(input.Body.Content)); err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	}

	resp.Body.Ok = true

	return resp, nil
}

func (s *Server) getQueue(ctx context.Context, input *struct{}) (*QueueResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resp := &QueueResponse{}
	resp.Body.Messages = append([]QueuedMessage(nil), s.messageQueue...)
	return resp, nil
}

func (s *Server) updateQueuedMessage(ctx context.Context, input *UpdateQueuedMessageRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content := strings.TrimSpace(input.Body.Content)
	if content == "" {
		return nil, huma.Error400BadRequest("message must not be empty")
	}
	for i := range s.messageQueue {
		if s.messageQueue[i].ID == input.ID {
			s.messageQueue[i].Content = content
			resp := &QueueMutationResponse{}
			resp.Body.Ok = true
			return resp, nil
		}
	}
	return nil, huma.Error404NotFound("queued message not found")
}

func (s *Server) deleteQueuedMessage(ctx context.Context, input *DeleteQueuedMessageRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.messageQueue {
		if s.messageQueue[i].ID == input.ID {
			s.messageQueue = append(s.messageQueue[:i], s.messageQueue[i+1:]...)
			resp := &QueueMutationResponse{}
			resp.Body.Ok = true
			return resp, nil
		}
	}
	return nil, huma.Error404NotFound("queued message not found")
}

func (s *Server) enqueueMessageLocked(content string) {
	s.nextQueueID++
	s.messageQueue = append(s.messageQueue, QueuedMessage{
		ID:      s.nextQueueID,
		Content: content,
		Time:    s.clock.Now(),
	})
}

// startMessageQueue starts the queue dispatch loop. Dispatch is driven by
// polling rather than by subscribing to status events: an event subscriber
// channel is closed by the emitter when it fills up (which a slow
// conversation.Send call could cause), and status events don't fire again
// when a message fails transient validation while the agent stays stable.
// Polling is immune to both.
func (s *Server) startMessageQueue() {
	s.clock.TickerFunc(s.shutdownCtx, messageQueueDispatchInterval, func() error {
		s.dispatchNextQueuedMessage()
		return nil
	}, "messageQueueDispatch")
}

func (s *Server) dispatchNextQueuedMessage() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.messageQueue) == 0 || s.conversation.Status() != st.ConversationStatusStable {
		return
	}
	next := s.messageQueue[0]
	if err := s.conversation.Send(FormatMessage(s.agentType, next.Content)...); err != nil {
		if errors.Is(err, st.ErrMessageValidationChanging) {
			// Agent became busy again; retry on a later tick.
			return
		}
		if s.queueFailID != next.ID {
			s.queueFailID = next.ID
			s.queueFailCount = 0
		}
		s.queueFailCount++
		s.logger.Error("Failed to send queued message", "queueId", next.ID, "attempt", s.queueFailCount, "error", err)
		if s.queueFailCount >= maxQueueDispatchAttempts {
			// Drop the poison message so it doesn't block the queue forever.
			s.messageQueue = s.messageQueue[1:]
			s.emitter.EmitError(
				fmt.Sprintf("Dropped queued message after %d failed attempts: %v", s.queueFailCount, err),
				st.ErrorLevelError,
			)
		}
		return
	}
	s.messageQueue = s.messageQueue[1:]
}

// uploadFiles handles POST /upload
func (s *Server) uploadFiles(ctx context.Context, input *struct {
	RawBody huma.MultipartFormFiles[UploadRequest]
},
) (*UploadResponse, error) {
	formData := input.RawBody.Data()

	file := formData.File.File

	// Limit file size to 10MB
	const maxFileSize = 10 << 20 // 10MB
	buf, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	if len(buf) > maxFileSize {
		return nil, huma.Error400BadRequest("file size exceeds 10MB limit")
	}

	// Calculate checksum of the uploaded file to create unique subdirectory
	hash := sha256.Sum256(buf)
	checksum := hex.EncodeToString(hash[:8]) // Use first 8 bytes (16 hex chars)

	// Create checksum-based subdirectory in tempDir
	uploadDir := filepath.Join(s.tempDir, checksum)
	err = os.MkdirAll(uploadDir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	// Save individual file with original filename (extract just the base filename for security)
	filename := filepath.Base(formData.File.Filename)

	outPath := filepath.Join(uploadDir, filename)
	err = os.WriteFile(outPath, buf, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	resp := &UploadResponse{}
	resp.Body.Ok = true
	resp.Body.FilePath = outPath
	return resp, nil
}

// subscribeEvents is an SSE endpoint that sends events to the client
func (s *Server) subscribeEvents(ctx context.Context, input *struct{}, send sse.Sender) {
	subscriberId, ch, stateEvents := s.emitter.Subscribe()
	defer s.emitter.Unsubscribe(subscriberId)

	s.logger.Info("New subscriber", "subscriberId", subscriberId)
	for _, event := range stateEvents {
		if event.Type == EventTypeScreenUpdate {
			continue
		}
		if err := send.Data(event.Payload); err != nil {
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
			if event.Type == EventTypeScreenUpdate {
				continue
			}
			if err := send.Data(event.Payload); err != nil {
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
	subscriberId, ch, stateEvents := s.emitter.Subscribe()
	defer s.emitter.Unsubscribe(subscriberId)
	s.logger.Info("New screen subscriber", "subscriberId", subscriberId)
	for _, event := range stateEvents {
		if event.Type != EventTypeScreenUpdate {
			continue
		}
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
			if event.Type != EventTypeScreenUpdate {
				continue
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

// Start starts the HTTP server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	s.srv = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}

	return s.srv.ListenAndServe()
}

// Stop gracefully stops the HTTP server. It is safe to call multiple times.
func (s *Server) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		s.shutdown()

		// Clean up temporary directory
		s.cleanupTempDir()

		if s.srv != nil {
			if err = s.srv.Shutdown(ctx); errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
		}
	})
	return err
}

// cleanupTempDir removes the temporary directory and all its contents
func (s *Server) cleanupTempDir() {
	if err := os.RemoveAll(s.tempDir); err != nil {
		s.logger.Error("Failed to clean up temporary directory", "tempDir", s.tempDir, "error", err)
	} else {
		s.logger.Info("Cleaned up temporary directory", "tempDir", s.tempDir)
	}
}

func (s *Server) SaveState(source string) error {
	if err := s.conversation.SaveState(); err != nil {
		s.logger.Error("Failed to save conversation state", "source", source, "error", err)
		return err
	}
	return nil
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
