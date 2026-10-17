package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/mcpconfig"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/statedb"
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
	bind               string
	auth               *tokenAuth // nil when no API token is configured
	srv                *http.Server
	mu                 sync.RWMutex
	stopOnce           sync.Once
	logger             *slog.Logger
	conversation       st.Conversation
	native             *native.Conversation // nil unless the transport is native
	stateDB            *statedb.DB          // nil unless the conversation is kept
	keepUploads        bool
	savedQueue         []QueuedMessage // the queue as last stored in stateDB
	savedNextQueueID   int
	agentio            st.AgentIO
	agentType          mf.AgentType
	emitter            *EventEmitter
	chatBasePath       string
	uploadDir          string
	cwd                string
	clock              quartz.Clock
	shutdownCtx        context.Context
	shutdown           context.CancelFunc
	transport          Transport
	messageQueue       []QueuedMessage
	mcpStore           mcpconfig.Store
	mcpMu              sync.Mutex
	webhook            *webhookDispatcher
	handoffSuppressed  string
	handoffReceipts    map[string]handoffReceipt
	restartAgent       func(context.Context) (int, error)
	jsonlWatcherCancel context.CancelFunc
	jsonlParentCtx     context.Context // parent context for spawning new JSONL watchers
	nextQueueID        int
	// Consecutive non-validation dispatch failures for the queued message
	// identified by queueFailID. Used to drop poison messages.
	queueFailID    int
	queueFailCount int

	// Memoized isTerminalQuestionScreen result for the dispatch loop. The screen
	// string is pointer-stable while unchanged (termexec render cache), so
	// the equality check below is cheap and the multi-regexp scan only runs
	// when the screen actually changes. Guarded by s.mu.
	terminalQuestionScreen string
	terminalQuestionResult bool
	terminalQuestionValid  bool

	// When the Codex update dialog was last dismissed. Guarded by s.mu.
	codexUpdateDismissedAt time.Time
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
	// TerminalColumns is the width of the agent's emulated terminal, 0 when
	// the transport has no terminal.
	TerminalColumns uint16
	// RestartAgent replaces the PTY agent process while keeping AgentAPI alive.
	// It returns the new process PID so the JSONL watcher can be restarted.
	// It is nil for transports or server modes that cannot restart.
	RestartAgent func(context.Context) (int, error)
	// Webhook sends an event whenever the agent status changes.
	Webhook WebhookConfig
	// NativeFactory creates the agent adapter for the native transport.
	NativeFactory native.Factory
	// StateDB, if set, stores the conversation and restores it on start
	// (native transport only).
	StateDB *statedb.DB
	// UploadDir is where uploads and tool result images are kept. Empty
	// means a temporary directory that is removed on stop.
	UploadDir string
	// APIToken, if non-empty, enables token authentication on all API
	// endpoints: a Bearer header, or the session cookie POST /auth/session
	// sets for browsers. Static file routes (/chat/*, /) and /auth/session
	// are exempt so browsers can load the chat UI and sign in.
	APIToken string
	// Bind is the address the server listens on: an IP address or hostname
	// without a port. Empty listens on all interfaces.
	Bind string
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

// ParseBindAddress validates the address to listen on: an IP address or a
// hostname, without a port. IPv6 addresses may be bracketed ("[::1]"); the
// brackets are removed. Empty means all interfaces.
func ParseBindAddress(input string) (string, error) {
	addr := strings.TrimSpace(input)
	if addr == "" {
		return "", nil
	}
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = addr[1 : len(addr)-1]
		if net.ParseIP(addr) == nil {
			return "", fmt.Errorf("'%s' is not a valid IPv6 address", input)
		}
		return addr, nil
	}
	if net.ParseIP(addr) != nil {
		return addr, nil
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return "", fmt.Errorf("'%s' must not include a port; use --port", input)
	}
	if !isValidHostname(addr) {
		return "", fmt.Errorf("'%s' is not a valid IP address or hostname", input)
	}
	return addr, nil
}

// isValidHostname reports whether name is a DNS hostname (RFC 1123 labels).
func isValidHostname(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
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
	bind, err := ParseBindAddress(config.Bind)
	if err != nil {
		return nil, fmt.Errorf("invalid bind address: %w", err)
	}

	logger.Info(fmt.Sprintf("Allowed hosts: %s", strings.Join(allowedHosts, ", ")))
	logger.Info(fmt.Sprintf("Allowed origins: %s", strings.Join(allowedOrigins, ", ")))

	// Enforce allowed hosts in a custom middleware that ignores the port during matching.
	badHostHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid host header. Allowed hosts: "+strings.Join(allowedHosts, ", "), http.StatusBadRequest)
	})
	router.Use(hostAuthorizationMiddleware(allowedHosts, badHostHandler))

	router.Use(requestLogMiddleware(logger))

	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	})
	router.Use(corsMiddleware.Handler)

	// Token auth runs after CORS: preflight requests carry no credentials
	// and are answered by the CORS middleware, and a 401 still gets the CORS
	// headers, so a chat UI on another origin can read it and ask for the
	// token.
	var auth *tokenAuth
	if config.APIToken != "" {
		logger.Info("API token authentication enabled")
		auth = newTokenAuth(config.APIToken)
		router.Use(auth.middleware)
	}

	humaConfig := huma.DefaultConfig("AgentAPI", version.Version)
	humaConfig.Info.Description = "HTTP API for Claude Code, Goose, and Aider.\n\nhttps://github.com/coder/agentapi"
	api := humachi.New(router, humaConfig)
	formatMessage := func(message string, userInput string) string {
		return mf.FormatAgentMessage(config.AgentType, message, userInput)
	}

	isAgentReadyForInitialPrompt := func(message string) bool {
		return mf.IsAgentReadyForInitialPrompt(config.AgentType, message)
	}

	stabilityScreen := func(screen string) string {
		return mf.StabilityRegion(config.AgentType, screen)
	}

	formatToolCall := func(message string) (string, []string) {
		return mf.FormatToolCall(config.AgentType, message)
	}

	webhook, err := newWebhookDispatcher(config.Webhook, logger, config.AgentType, config.Transport)
	if err != nil {
		return nil, fmt.Errorf("configure webhook: %w", err)
	}
	var sessionBytes [16]byte
	if _, err := rand.Read(sessionBytes[:]); err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}
	emitterOptions := []EventEmitterOption{
		WithAgentType(config.AgentType),
		WithTransport(config.Transport),
		WithSessionID(hex.EncodeToString(sessionBytes[:])),
		WithTerminalColumns(config.TerminalColumns),
		WithClock(config.Clock),
		WithStatusChangeHandler(webhook.statusChanged),
	}
	if config.StateDB != nil {
		emitterOptions = append(emitterOptions, WithPersister(config.StateDB))
	}
	emitter := NewEventEmitter(emitterOptions...)
	restored, err := restoreState(config, emitter, logger)
	if err != nil {
		return nil, err
	}

	// Format initial prompt into message parts if provided
	var initialPrompt []st.MessagePart
	if config.InitialPrompt != "" {
		initialPrompt = FormatMessage(config.AgentType, config.InitialPrompt)
	}

	var conversation st.Conversation
	var nativeConversation *native.Conversation
	var observer *nativeObserver
	if config.Transport == TransportNative {
		if config.NativeFactory == nil {
			return nil, fmt.Errorf("native transport requires an adapter factory")
		}
		observer = &nativeObserver{emitter: emitter}
		if config.StateDB != nil {
			observer.persister = config.StateDB
		}
		nativeConversation = native.NewConversation(native.Config{
			Factory:         config.NativeFactory,
			ResumeRef:       restored.resumeRef,
			Settings:        restored.settings,
			InitialMessages: restored.messages,
			InitialPrompt:   config.InitialPrompt,
			Emitter:         emitter,
			Observer:        observer,
			Logger:          logger,
			Clock:           config.Clock,
		})
		conversation = nativeConversation
	} else if config.Transport == TransportACP {
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
			StabilityScreen:        stabilityScreen,
			FormatToolCall:         formatToolCall,
			InitialPrompt:          initialPrompt,
			Logger:                 logger,
			StatePersistenceConfig: config.StatePersistenceConfig,
		}, emitter)
	}

	// Create the directory for uploads: a temporary one unless the
	// conversation is kept, whose messages refer to uploads by path.
	uploadDir := config.UploadDir
	if uploadDir != "" {
		if err := os.MkdirAll(uploadDir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create upload directory: %w", err)
		}
	} else {
		uploadDir, err = os.MkdirTemp("", "agentapi-uploads-")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary directory: %w", err)
		}
	}
	logger.Info("Using upload directory", "uploadDir", uploadDir)

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	webhook.start(shutdownCtx)

	s := &Server{
		router:         router,
		api:            api,
		port:           config.Port,
		bind:           bind,
		auth:           auth,
		conversation:   conversation,
		native:         nativeConversation,
		logger:         logger,
		agentio:        config.AgentIO,
		agentType:      config.AgentType,
		emitter:        emitter,
		chatBasePath:   strings.TrimSuffix(config.ChatBasePath, "/"),
		uploadDir:      uploadDir,
		cwd:            config.CWD,
		clock:          config.Clock,
		shutdownCtx:    shutdownCtx,
		shutdown:       shutdownCancel,
		transport:      config.Transport,
		webhook:        webhook,
		restartAgent:   config.RestartAgent,
		jsonlParentCtx: ctx,
		stateDB:        config.StateDB,
		keepUploads:    config.UploadDir != "",
	}
	if config.StateDB != nil {
		s.restoreQueue(restored.snapshot)
	}
	if mcpconfig.SupportedAgent(config.AgentType) {
		store, err := mcpconfig.NewStore(config.AgentType, config.CWD)
		if err != nil {
			return nil, fmt.Errorf("create MCP config store: %w", err)
		}
		s.mcpStore = store
	}

	if observer != nil {
		observer.storeImages = s.storeRichImages
	}

	// Register API routes
	s.registerRoutes()

	// Start the conversation polling loop if we have an agent IO.
	// AgentIO is nil only when --print-openapi is used (no agent runs).
	// For PTY transport, the process is already running at this point -
	// termexec.StartProcess() blocks until the PTY is created and the process
	// is active. Agent readiness (waiting for the prompt) is handled
	// asynchronously inside conversation.Start() via ReadyForInitialPrompt.
	if config.AgentIO != nil || s.native != nil {
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

// NativeExited delivers the agent's exit when it runs over the native
// transport and exits on its own; it is nil for other transports.
func (s *Server) NativeExited() <-chan error {
	if s.native == nil {
		return nil
	}
	return s.native.Exited()
}

// MarkAgentExited records the terminal lifecycle state before the HTTP server
// shuts down. A nil error represents a clean process exit.
func (s *Server) MarkAgentExited(err error) {
	if err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return
	}
	s.emitter.SetLifecycle(LifecycleExited)
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

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// requestLogMiddleware logs every HTTP request with method, path, and response status.
// SSE and static file routes are excluded to avoid noise.
func requestLogMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Skip noisy routes: static files, SSE streams, and docs.
			if path == "/" || strings.HasPrefix(path, "/chat") ||
				path == "/events" || path == "/internal/screen" ||
				path == "/docs" || path == "/openapi.json" {
				next.ServeHTTP(w, r)
				return
			}

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r)
			logger.Info("request",
				"method", r.Method,
				"path", path,
				"status", rw.statusCode,
			)
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
	case mf.AgentTypePi:
		resolver = &jsonlwatcher.PiResolver{
			PID:       pid,
			CWD:       s.cwd,
			NotBefore: time.Now(),
		}
		parser = jsonlwatcher.NewPiParser()
		sessionEventParser = jsonlwatcher.NewPiSessionEventParser()
	case mf.AgentTypeGoose, mf.AgentTypeAider, mf.AgentTypeGemini, mf.AgentTypeCopilot, mf.AgentTypeAmp,
		mf.AgentTypeCursor, mf.AgentTypeAuggie, mf.AgentTypeAmazonQ, mf.AgentTypeOpencode, mf.AgentTypeKimi,
		mf.AgentTypeCustom:
		// No session log to follow; the chat uses the terminal output.
	}

	if resolver == nil || parser == nil {
		return
	}

	watchCtx, cancel := context.WithCancel(s.jsonlParentCtx)
	s.jsonlWatcherCancel = cancel

	// A new agent process starts a new session, unnamed until the agent
	// names it.
	s.emitter.SetSessionName("")
	namer := jsonlwatcher.NewSessionNamer(string(s.agentType))
	if polled, ok := namer.(jsonlwatcher.PolledSessionNamer); ok {
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
					if name, changed := polled.Poll(); changed {
						s.emitter.SetSessionName(name)
					}
				}
			}
		}()
	}

	w := jsonlwatcher.New(jsonlwatcher.Config{
		Resolver: resolver,
		Parser:   parser,
		Logger:   s.logger,
		OnMessage: func(msg jsonlwatcher.RichMessage) {
			s.storeRichImages(&msg)
			s.emitter.EmitRichMessage(msg)
		},
		OnLine: func(line []byte) {
			if namer != nil {
				if name, changed := namer.Name(line); changed {
					s.emitter.SetSessionName(name)
				}
			}
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
	s.registerAuthRoutes()
	s.registerNativeRoutes()
	s.registerFileRoutes()
	huma.Get(s.api, "/handoff", s.getPendingHandoff)
	huma.Post(s.api, "/handoff/reply", s.replyHandoff)
	// GET /status endpoint
	huma.Get(s.api, "/status", s.getStatus, func(o *huma.Operation) {
		o.Description = "Returns the current status of the agent."
	})
	huma.Get(s.api, "/title", s.getTitle, func(o *huma.Operation) {
		o.Description = "Returns the current human-readable session title and the server state used to derive it."
	})
	huma.Get(s.api, "/workspace", s.getWorkspace, func(o *huma.Operation) {
		o.Description = "Returns the Coder workspace URL injected into the process, when available."
	})

	// GET /messages endpoint
	huma.Get(s.api, "/messages", s.getMessages, func(o *huma.Operation) {
		o.Description = "Returns a list of messages representing the conversation history with the agent."
	})

	// GET /rich-messages endpoint
	huma.Get(s.api, "/rich-messages", s.getRichMessages, func(o *huma.Operation) {
		o.Description = "Returns a list of rich structured messages. " +
			"Each message contains structured content blocks (text, thinking, tool_use, tool_result), " +
			"model information, and token usage data. With the PTY transport they are parsed from the " +
			"session log of agents that have one (claude, codex and pi); with the native transport they " +
			"come from the agent's protocol."
	})

	huma.Get(s.api, "/timeline", s.getTimeline, func(o *huma.Operation) {
		o.Description = "Returns all normalized events from the current agent session, including text, thinking, tool calls, tool results, and system lifecycle events."
	})

	huma.Delete(s.api, "/messages", s.deleteMessages, func(o *huma.Operation) {
		o.Summary = "Delete all messages and restart"
		o.Description = "Clears all conversation state (messages, rich messages, timeline, errors) and restarts the agent in a new session (PTY or native transport). Returns 400 if restart is not supported (e.g. ACP transport)."
		o.Errors = []int{400}
	})

	huma.Get(s.api, "/webhook", s.getWebhookConfig, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Get webhook configuration"
		o.Description = "Returns the mutable run-status webhook configuration."
	})
	huma.Put(s.api, "/webhook", s.updateWebhookConfig, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Update webhook configuration"
		o.Description = "Updates run-status webhook delivery immediately. Startup flags provide the initial values; this endpoint can replace or disable them without restarting the agent."
		o.Errors = []int{400}
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
	huma.Put(s.api, "/queue", s.reorderQueue, func(o *huma.Operation) {
		o.Description = "Reorders the queued messages. The body must list every queued message id exactly once; otherwise 409 is returned."
		o.Errors = []int{409}
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

	// Uploaded images, for the chat to show (not part of the OpenAPI
	// schema: the response is the image itself).
	s.router.Get("/uploads/{checksum}/{name}", s.serveUpload)

	s.router.Handle("/", http.HandlerFunc(s.redirectToChat))

	// Serve static files for the chat interface under /chat
	s.registerStaticFileRoutes()
}

// getStatus handles GET /status. It only reads the emitter, which has its
// own lock, so it answers while s.mu is held for a slow call into the agent
// (a restart, a native turn).
func (s *Server) getStatus(ctx context.Context, input *struct{}) (*StatusResponse, error) {
	snapshot := s.emitter.StatusSnapshot()

	resp := &StatusResponse{}
	resp.Body.Status = snapshot.Status
	resp.Body.Lifecycle = snapshot.Lifecycle
	resp.Body.SessionID = snapshot.SessionID
	resp.Body.RunID = snapshot.RunID
	resp.Body.AgentType = s.agentType
	resp.Body.Transport = s.transport
	resp.Body.Version = snapshot.Version
	resp.Body.TerminalPrompt = snapshot.TerminalPrompt
	resp.Body.TerminalColumns = snapshot.TerminalColumns
	resp.Body.SessionName = snapshot.SessionName
	resp.Body.PendingRequests = snapshot.PendingRequests
	resp.Body.Capabilities = snapshot.Capabilities
	resp.Body.LoginRequired = snapshot.LoginRequired
	resp.Body.Settings = snapshot.Settings
	resp.Body.Epoch = s.emitter.Epoch()

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
			task = shortText(messages[i].Message, 60)
			break
		}
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

// shortText returns s on one line, cut to at most n runes.
func shortText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

func (s *Server) getWorkspace(_ context.Context, _ *struct{}) (*WorkspaceResponse, error) {
	var response WorkspaceResponse
	base := strings.TrimRight(os.Getenv("CODER_URL"), "/")
	owner := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_OWNER_NAME"))
	name := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_NAME"))
	if base != "" && owner != "" && name != "" {
		response.Body.URL = base + "/@" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	}
	return &response, nil
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
	pending, _ := s.pendingHandoffLocked()

	resp := &MessageResponse{}
	mode := input.Body.DispatchMode
	if mode == "" {
		mode = DispatchQueue
	}
	if mode != DispatchQueue && (s.native == nil || input.Body.Type != MessageTypeUser) {
		return nil, huma.Error400BadRequest("dispatch_mode steer and interrupt need the native transport and a user message")
	}
	switch input.Body.Type {
	case MessageTypeUser:
		if strings.TrimSpace(input.Body.Content) == "" {
			return nil, huma.Error400BadRequest("message must not be empty")
		}
		if s.native != nil {
			if err := s.createNativeMessageLocked(input.Body.Content, mode, resp); err != nil {
				return nil, err
			}
			if resp.Body.Queued || resp.Body.Steered {
				// The message doesn't answer a pending request, so the
				// handoff keeps offering it.
				return resp, nil
			}
			break
		}
		var screen string
		if s.transport == TransportPTY {
			screen = s.currentScreenLocked()
		}
		if keys, ok := freeTextReplyKeys(s.agentType, screen); ok {
			if err := s.conversation.Send(formatFreeTextReply(s.agentType, keys, input.Body.Content)...); err != nil {
				if errors.Is(err, st.ErrMessageValidationChanging) {
					return nil, huma.Error409Conflict("the agent's prompt is still updating; try again in a moment")
				}
				return nil, fmt.Errorf("failed to send reply to the agent's prompt: %w", err)
			}
			break
		}
		// Enqueue when earlier messages are still waiting, even if the
		// agent is stable, so messages are delivered in FIFO order. Never
		// type a message into an interactive prompt either: the prompt
		// ignores the text and the carriage return would pick the
		// highlighted option (e.g. approve a permission request). The
		// queue holds it until the prompt is answered.
		if len(s.messageQueue) > 0 || s.conversation.Status() != st.ConversationStatusStable ||
			(screen != "" && isTerminalQuestionScreen(s.agentType, screen)) {
			s.enqueueMessageLocked(input.Body.Content, false)
			resp.Body.Ok = true
			resp.Body.Queued = true
			return resp, nil
		}
		if err := s.conversation.Send(FormatMessage(s.agentType, input.Body.Content)...); err != nil {
			if errors.Is(err, st.ErrMessageValidationChanging) {
				s.enqueueMessageLocked(input.Body.Content, false)
				resp.Body.Ok = true
				resp.Body.Queued = true
				return resp, nil
			}
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	case MessageTypeRaw:
		if s.agentio == nil {
			return nil, huma.Error400BadRequest("raw messages need a terminal; the native transport has none")
		}
		if _, err := s.agentio.Write([]byte(input.Body.Content)); err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	}

	resp.Body.Ok = true
	if pending != nil {
		s.handoffSuppressed = pending.ID
	}

	return resp, nil
}

func (s *Server) getQueue(ctx context.Context, input *struct{}) (*QueueResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resp := &QueueResponse{}
	resp.Body.Messages = append([]QueuedMessage{}, s.messageQueue...)
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

// reorderQueue puts the queued messages in the given order.
func (s *Server) reorderQueue(_ context.Context, input *ReorderQueueRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := input.Body.IDs
	if len(ids) != len(s.messageQueue) {
		return nil, huma.Error409Conflict("the queue changed; list every queued message once")
	}
	byID := make(map[int]QueuedMessage, len(s.messageQueue))
	for _, m := range s.messageQueue {
		byID[m.ID] = m
	}
	reordered := make([]QueuedMessage, 0, len(ids))
	for _, id := range ids {
		m, ok := byID[id]
		if !ok {
			return nil, huma.Error409Conflict("the queue changed; list every queued message once")
		}
		delete(byID, id)
		reordered = append(reordered, m)
	}
	s.messageQueue = reordered
	resp := &QueueMutationResponse{}
	resp.Body.Ok = true
	return resp, nil
}

// enqueueMessageLocked queues a message last, or first if front is set.
func (s *Server) enqueueMessageLocked(content string, front bool) {
	s.nextQueueID++
	msg := QueuedMessage{
		ID:      s.nextQueueID,
		Content: content,
		Time:    s.clock.Now(),
	}
	if front {
		s.messageQueue = slices.Insert(s.messageQueue, 0, msg)
		return
	}
	s.messageQueue = append(s.messageQueue, msg)
}

// startMessageQueue starts the queue dispatch loop. Dispatch is driven by
// polling rather than by subscribing to status events: an event subscriber
// channel is closed by the emitter when it fills up (which a slow
// conversation.Send call could cause), and status events don't fire again
// when a message fails transient validation while the agent stays stable.
// Polling is immune to both.
func (s *Server) startMessageQueue() {
	s.clock.TickerFunc(s.shutdownCtx, messageQueueDispatchInterval, func() error {
		s.dismissCodexUpdatePrompt()
		s.dispatchNextQueuedMessage()
		s.persistQueue()
		return nil
	}, "messageQueueDispatch")
}

func (s *Server) dispatchNextQueuedMessage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.native != nil {
		s.dispatchNativeQueuedLocked()
		return
	}
	// Screen inspection is only needed when a message can be dispatched.
	if len(s.messageQueue) == 0 || s.conversation.Status() != st.ConversationStatusStable {
		return
	}
	if s.emitter != nil && s.transport == TransportPTY {
		s.emitter.mu.Lock()
		screen := s.emitter.screen
		s.emitter.mu.Unlock()
		if !s.terminalQuestionValid || screen != s.terminalQuestionScreen {
			s.terminalQuestionResult = isTerminalQuestionScreen(s.agentType, screen)
			s.terminalQuestionScreen = screen
			s.terminalQuestionValid = true
		}
		if s.terminalQuestionResult {
			return
		}
	}

	next := s.messageQueue[0]
	if err := s.conversation.Send(FormatMessage(s.agentType, next.Content)...); err != nil {
		if errors.Is(err, st.ErrMessageValidationChanging) {
			// Agent became busy again; retry on a later tick.
			return
		}
		attempts, drop := s.queueSendFailedLocked(next, err)
		// A message the agent didn't react to may still be in its input
		// box; retrying would type it a second time.
		notSubmitted := errors.Is(err, st.ErrMessageNotSubmitted)
		if drop || notSubmitted {
			// Drop the poison message so it doesn't block the queue forever.
			s.messageQueue = s.messageQueue[1:]
			msg := fmt.Sprintf("Dropped queued message %q after %d failed attempts: %v", shortText(next.Content, 40), attempts, err)
			if notSubmitted {
				msg = fmt.Sprintf("Queued message was typed but the agent did not submit it; check the agent's input box: %v", err)
			}
			s.emitter.EmitError(msg, st.ErrorLevelError)
		}
		return
	}
	s.messageQueue = s.messageQueue[1:]
}

// queueSendFailedLocked counts a failed attempt to send next, the head of
// the queue. It returns how often in a row sending it failed, and whether
// that is often enough to drop it. Caller holds s.mu.
func (s *Server) queueSendFailedLocked(next QueuedMessage, err error) (int, bool) {
	if s.queueFailID != next.ID {
		s.queueFailID = next.ID
		s.queueFailCount = 0
	}
	s.queueFailCount++
	s.logger.Error("Failed to send queued message", "queueId", next.ID, "attempt", s.queueFailCount, "error", err)
	return s.queueFailCount, s.queueFailCount >= maxQueueDispatchAttempts
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

	// Create checksum-based subdirectory in the upload directory
	dir := filepath.Join(s.uploadDir, checksum)
	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	// Save individual file with original filename (extract just the base filename for security)
	filename := filepath.Base(formData.File.Filename)

	outPath := filepath.Join(dir, filename)
	err = os.WriteFile(outPath, buf, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	resp := &UploadResponse{}
	resp.Body.Ok = true
	resp.Body.FilePath = outPath
	return resp, nil
}

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
	subscriberId, ch, stateEvents := s.emitter.SubscribeSince(since, epoch)
	defer s.emitter.Unsubscribe(subscriberId)

	s.logger.Info("New subscriber", "subscriberId", subscriberId)
	replay := make([]Event, 0, len(stateEvents))
	var replaySeq uint64
	for _, event := range stateEvents {
		if body, ok := event.Payload.(SessionSyncBody); ok {
			replaySeq = body.Seq
		}
		if event.Type == EventTypeScreenUpdate || (event.Type == EventTypeSessionSync && !sync) {
			continue
		}
		replay = append(replay, event)
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
			if event.Type == EventTypeScreenUpdate || (event.Type == EventTypeSessionSync && !sync) {
				continue
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

// ListenAddr returns the address Start listens on, e.g. ":3284" (all
// interfaces), "127.0.0.1:3284" or "[::1]:3284".
func (s *Server) ListenAddr() string {
	return net.JoinHostPort(s.bind, strconv.Itoa(s.port))
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.srv = &http.Server{
		Addr:    s.ListenAddr(),
		Handler: s.router,
	}

	return s.srv.ListenAndServe()
}

// Stop gracefully stops the HTTP server and the agent. It is safe to call
// multiple times.
func (s *Server) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		// Ends the queue loop and the event streams, which the HTTP
		// shutdown would otherwise wait for.
		s.shutdown()
		// No request can change the queue once the HTTP server and the
		// agent have stopped, so the queue is stored after both.
		if s.srv != nil {
			if err = s.srv.Shutdown(ctx); errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
		}
		if s.native != nil {
			if closeErr := s.native.Close(); closeErr != nil {
				s.logger.Error("Failed to stop the agent", "error", closeErr)
			}
		}
		s.persistQueue()
		s.cleanupUploadDir()
	})
	return err
}

// cleanupUploadDir removes the upload directory and all its contents, unless
// the conversation that refers to them is kept.
func (s *Server) cleanupUploadDir() {
	if s.keepUploads {
		return
	}
	if err := os.RemoveAll(s.uploadDir); err != nil {
		s.logger.Error("Failed to clean up upload directory", "uploadDir", s.uploadDir, "error", err)
	} else {
		s.logger.Info("Cleaned up upload directory", "uploadDir", s.uploadDir)
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
