package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/quartz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/coder/agentapi/internal/version"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/mcpconfig"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/pricing"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/sessions"
	"github.com/coder/agentapi/lib/shellterm"
	"github.com/coder/agentapi/lib/statedb"
	"github.com/coder/agentapi/lib/transcript"
	"github.com/coder/agentapi/lib/webhook"
)

// Server represents the HTTP server
type Server struct {
	router chi.Router
	api    huma.API
	port   int
	bind   string
	auth   *tokenAuth // nil when no API token is configured
	srv    *http.Server
	// mu serializes what changes the agent or acts on its state: messages
	// and their queue, answers, settings, restarts. Reads that only need a
	// snapshot don't take it. Locks are taken in this order, never the
	// other way round: mu, the conversation's, the updater's, the
	// emitter's, then the webhook dispatcher's and the state DB's.
	mu           sync.Mutex
	stopOnce     sync.Once
	stopping     bool        // set under mu once Stop closed the agent: no more restarts
	restarting   atomic.Bool // set under mu while a restart runs without it (runRestartLocked)
	updating     atomic.Bool // set under mu once an update restarts AgentAPI: messages wait in the queue, and no agent restart begins
	logger       *slog.Logger
	conversation conv.Conversation
	native       *native.Conversation // nil unless the transport is native
	sessions     *sessions.Store      // the agent's sessions; nil unless native and it keeps logs
	stateDB      *statedb.DB          // nil unless the conversation is kept
	// queueStoreMu guards what storeQueue last stored in stateDB: the
	// queue, its last ID and the version of the queue they are.
	queueStoreMu       sync.Mutex
	savedQueue         []QueuedMessage
	savedNextQueueID   int
	savedQueueVersion  uint64
	agentio            st.AgentIO
	agentType          mf.AgentType
	emitter            *EventEmitter
	chatBasePath       string
	uploads            *uploadStore
	uploadPruneNow     chan struct{} // nil until startUploadPruning
	uploadPruneDone    chan struct{} // closed once pruning stopped; nil until it starts
	cwd                string
	clock              quartz.Clock
	shutdownCtx        context.Context
	shutdown           context.CancelFunc
	transport          Transport
	queue              messageQueue  // guarded by mu
	queueLoop          quartz.Waiter // the queue's loop, nil until it starts
	mcpStore           mcpconfig.Store
	mcpMu              sync.Mutex
	webhook            *webhook.Dispatcher
	registry           *instanceRegistry // nil without a registry directory
	updater            *updater
	handoff            handoffState
	restartAgent       func(context.Context) (int, error)
	updateAgent        func(context.Context)
	terminal           *shellterm.Terminal // nil without a terminal
	jsonlWatcherCancel context.CancelFunc
	jsonlWatcherDone   *sync.WaitGroup
	jsonlParentCtx     context.Context // parent context for spawning new JSONL watchers

	// When the Codex update dialog was last dismissed. Guarded by s.mu.
	codexUpdateDismissedAt time.Time
}

// That's about 40 frames per second. It's slightly less
// because the action of taking a snapshot takes time too.
const snapshotInterval = 25 * time.Millisecond

type ServerConfig struct {
	AgentType      mf.AgentType
	Transport      Transport
	Port           int
	ChatBasePath   string
	AllowedHosts   []string
	AllowedOrigins []string
	InitialPrompt  string
	Clock          quartz.Clock
	CWD            string // Working directory (used by Codex resolver)
	// Webhook sends an event whenever the agent status changes.
	Webhook WebhookConfig
	// Name is what clients call this AgentAPI (GET /summary, webhook
	// events); empty is the working directory's name.
	Name string
	// PTY and Native configure the transport of the same name; the other
	// is ignored.
	PTY    PTYConfig
	Native NativeConfig
	// StateDB, if set, stores the conversation and restores it on start
	// (the native and PTY transports).
	StateDB *statedb.DB
	// UploadDir is where uploads and tool result images are kept. Empty
	// means a temporary directory that is removed on stop. Either way the
	// files AgentAPI stored there (<checksum>/<name>) that the conversation
	// no longer refers to are removed once they are old (uploadGrace).
	UploadDir string
	// APIToken, if non-empty, enables token authentication on all API
	// endpoints: a Bearer header, or the session cookie POST /auth/session
	// sets for browsers. Static file routes (/chat/*, /) and /auth/session
	// are exempt so browsers can load the chat UI and sign in.
	APIToken string
	// Bind is the address the server listens on: an IP address or hostname
	// without a port. Empty listens on all interfaces.
	Bind string
	// RegistryDir, if set, is where the server writes an entry while it
	// serves, for clients that find the AgentAPIs of a machine there.
	RegistryDir string
	// RestartsInPlace says in the registry entry that the process restarts
	// in place on SIGUSR2 (see cmd/server).
	RestartsInPlace bool
	// Update is how the server updates itself through its API (POST
	// /update); the zero value doesn't.
	Update UpdateConfig
}

// PTYConfig is what the PTY transport needs.
type PTYConfig struct {
	// AgentIO is the agent's terminal; nil runs no agent (--print-openapi).
	AgentIO st.AgentIO
	// TerminalColumns is the width of the agent's emulated terminal.
	TerminalColumns uint16
	// AgentPID is the agent's process, whose session log the JSONL watcher
	// reads; 0 runs no watcher.
	AgentPID int
	// RestartAgent replaces the agent process while keeping AgentAPI
	// alive. It returns the new process PID so the JSONL watcher can be
	// restarted. It is nil when the agent can't be restarted.
	RestartAgent func(context.Context) (int, error)
}

// NativeConfig is what the native transport needs.
type NativeConfig struct {
	// Factory creates the agent's adapter.
	Factory native.Factory
	// Terminal offers a shell in the agent's working directory (GET
	// /terminal).
	Terminal bool
	// UpdateAgent, if set, updates the agent's CLI before a restart starts
	// a new one (Codex, Pi).
	UpdateAgent func(context.Context)
	// AutoContinue continues a turn a usage limit stopped once the limit
	// resets.
	AutoContinue bool
	// Prices price the model calls of the agent's sessions; nil is the
	// built-in prices.
	Prices *pricing.Table
	// PiSessionDir is the --session-dir Pi runs with, where its session
	// logs are; empty without one.
	PiSessionDir string
	// RestartDelays are native.Config.RestartDelays: the waits before an
	// agent that exited is started again. Nil is the default.
	RestartDelays []time.Duration
}

// agentIO is the conversation's view of a PTY agent, nil for a native one
// or when no agent runs.
func (c ServerConfig) agentIO() st.AgentIO {
	if c.Transport == TransportNative {
		return nil
	}
	return c.PTY.AgentIO
}

// CheckConfig returns an error NewServer would return for config before it
// sets anything up: for its allowed hosts and origins, bind address and
// webhook. NewServer also opens what the state database holds.
func CheckConfig(config ServerConfig) error {
	if _, _, _, err := parseListenConfig(config); err != nil {
		return err
	}
	if err := webhook.Check(config.Webhook); err != nil {
		return fmt.Errorf("configure webhook: %w", err)
	}
	return nil
}

// parseListenConfig parses the allowed hosts and origins and the bind
// address of config.
func parseListenConfig(config ServerConfig) (hosts, origins []string, bind string, err error) {
	if hosts, err = parseAllowedHosts(config.AllowedHosts); err != nil {
		return nil, nil, "", fmt.Errorf("failed to parse allowed hosts: %w", err)
	}
	if origins, err = parseAllowedOrigins(config.AllowedOrigins); err != nil {
		return nil, nil, "", fmt.Errorf("failed to parse allowed origins: %w", err)
	}
	if bind, err = ParseBindAddress(config.Bind); err != nil {
		return nil, nil, "", fmt.Errorf("invalid bind address: %w", err)
	}
	return hosts, origins, bind, nil
}

// NewServer creates a new server instance
func NewServer(ctx context.Context, config ServerConfig) (*Server, error) {
	router := chi.NewMux()

	logger := logctx.From(ctx)

	if config.Clock == nil {
		config.Clock = quartz.NewReal()
	}

	allowedHosts, allowedOrigins, bind, err := parseListenConfig(config)
	if err != nil {
		return nil, err
	}

	logger.Info(fmt.Sprintf("Allowed hosts: %s", strings.Join(allowedHosts, ", ")))
	logger.Info(fmt.Sprintf("Allowed origins: %s", strings.Join(allowedOrigins, ", ")))

	// Enforce allowed hosts in a custom middleware that ignores the port during matching.
	badHostHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid host header. Allowed hosts: "+strings.Join(allowedHosts, ", "), http.StatusBadRequest)
	})
	// The address {ip} in the public URL stands for now, which DHCP may
	// change: the list can't name it.
	var dispatcher *webhook.Dispatcher
	publicIP := func(hostname string) bool { return dispatcher != nil && dispatcher.PublicIP() == hostname }
	router.Use(hostAuthorizationMiddleware(allowedHosts, publicIP, badHostHandler))

	router.Use(requestLogMiddleware(logger))
	router.Use(Compress())

	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	})
	router.Use(corsMiddleware.Handler)

	// After CORS, so that a refused request's 403 has the CORS headers.
	router.Use(CrossOriginProtection(allowedOrigins, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Warn("Refused a request a page of another origin sent", "method", r.Method, "path", r.URL.Path, "origin", r.Header.Get("Origin"))
		http.Error(w, "A page of another origin sent this request. To let it, add its origin to --allowed-origins.", http.StatusForbidden)
	})))

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

	dispatcher, err = webhook.New(config.Webhook, logger, config.AgentType, config.Transport, config.Clock)
	if err != nil {
		return nil, fmt.Errorf("configure webhook: %w", err)
	}
	var serverBytes [16]byte
	if _, err := rand.Read(serverBytes[:]); err != nil {
		return nil, fmt.Errorf("generate server ID: %w", err)
	}
	// The status and the webhook name this run the same.
	serverID := hex.EncodeToString(serverBytes[:])
	dispatcher.SetServerID(serverID)
	emitterOptions := []convstate.EventEmitterOption{
		convstate.WithAgentType(config.AgentType),
		convstate.WithTransport(config.Transport),
		convstate.WithServerID(serverID),
		convstate.WithTerminalColumns(config.PTY.TerminalColumns),
		convstate.WithClock(config.Clock),
		convstate.WithStatusChangeHandler(dispatcher.StatusChanged),
		convstate.WithInstance(dispatcher.Instance),
	}
	stateDB := config.StateDB
	if stateDB != nil {
		emitterOptions = append(emitterOptions, convstate.WithPersister(stateDB))
	}
	var terminal *shellterm.Terminal
	if config.Native.Terminal && config.Transport == TransportNative {
		terminal = shellterm.New(shellterm.Config{Dir: config.CWD, Env: terminalEnv(), Logger: logger.With("component", "terminal")})
		emitterOptions = append(emitterOptions, convstate.WithTerminal())
	}
	emitter := convstate.NewEventEmitter(emitterOptions...)
	restored, err := restoreState(config, stateDB, emitter, logger)
	if err != nil {
		return nil, err
	}

	// Format initial prompt into message parts if provided
	var initialPrompt []conv.MessagePart
	if config.InitialPrompt != "" {
		initialPrompt = FormatMessage(config.AgentType, config.InitialPrompt)
	}

	// The directory for uploads: a temporary one unless the conversation
	// is kept, whose messages refer to uploads by path.
	uploads, err := newUploadStore(config.UploadDir)
	if err != nil {
		return nil, err
	}
	logger.Info("Using upload directory", "uploadDir", uploads.dir)

	var conversation conv.Conversation
	var nativeConversation *native.Conversation
	var observer *nativeObserver
	if config.Transport == TransportNative {
		if config.Native.Factory == nil {
			return nil, fmt.Errorf("native transport requires an adapter factory")
		}
		observer = &nativeObserver{emitter: emitter}
		if stateDB != nil {
			observer.persister = stateDB
		}
		nativeConversation = native.NewConversation(native.Config{
			Factory:         config.Native.Factory,
			ResumeRef:       restored.resumeRef,
			Settings:        restored.settings,
			LimitStop:       restored.limitStop,
			AutoContinue:    config.Native.AutoContinue,
			InitialMessages: restored.messages,
			InitialPrompt:   config.InitialPrompt,
			Emitter:         emitter,
			Observer:        observer,
			Logger:          logger,
			Clock:           config.Clock,
			RestartDelays:   config.Native.RestartDelays,
			PrepareRichMessage: func(msg *transcript.RichMessage) {
				uploads.storeRichImages(msg, logger)
			},
		})
		conversation = nativeConversation
	} else {
		conversation = st.NewPTY(ctx, st.PTYConversationConfig{
			AgentType:             config.AgentType,
			AgentIO:               config.PTY.AgentIO,
			Clock:                 config.Clock,
			SnapshotInterval:      snapshotInterval,
			ScreenStabilityLength: 2 * time.Second,
			FormatMessage:         formatMessage,
			ReadyForInitialPrompt: isAgentReadyForInitialPrompt,
			ReadyWhenStable:       mf.ReadyWhenStable(config.AgentType),
			StabilityScreen:       stabilityScreen,
			FormatToolCall:        formatToolCall,
			InitialPrompt:         ptyInitialPrompt(initialPrompt, config, stateDB, restored),
			Logger:                logger,
			InitialMessages:       restored.messages,
		}, emitter)
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	s := &Server{
		router:         router,
		api:            api,
		port:           config.Port,
		bind:           bind,
		auth:           auth,
		conversation:   conversation,
		native:         nativeConversation,
		logger:         logger,
		agentio:        config.agentIO(),
		agentType:      config.AgentType,
		emitter:        emitter,
		chatBasePath:   strings.TrimSuffix(config.ChatBasePath, "/"),
		uploads:        uploads,
		cwd:            config.CWD,
		clock:          config.Clock,
		shutdownCtx:    shutdownCtx,
		shutdown:       shutdownCancel,
		transport:      config.Transport,
		webhook:        dispatcher,
		restartAgent:   config.PTY.RestartAgent,
		updateAgent:    config.Native.UpdateAgent,
		terminal:       terminal,
		jsonlParentCtx: ctx,
		stateDB:        stateDB,
	}
	// Clients follow the queue through queue_update events, and the state
	// database has every change: a message that went out isn't sent again
	// after a crash.
	s.queue.onChange = func(queue []QueuedMessage, lastID int, version uint64) {
		emitter.SetQueue(queue, version)
		s.storeQueue(queue, lastID, version)
	}
	// The summary and the registry entry name the new address.
	dispatcher.OnURLChange(s.instanceChanged)
	if stateDB != nil {
		s.restoreQueue(restored.snapshot)
		if stored := restored.snapshot.Meta[statedb.MetaWebhook]; stored != "" {
			switch ok, err := dispatcher.Restore(stored); {
			case err != nil:
				logger.Warn("Ignoring the stored webhook settings", "error", err)
			case ok:
				logger.Info("Restored the webhook settings changed through the API")
			default:
				logger.Info("The webhook flags changed since its settings were changed through the API; using the flags")
			}
		}
	}
	if mcpconfig.SupportedAgent(config.AgentType) {
		store, err := mcpconfig.NewStore(config.AgentType, config.CWD)
		if err != nil {
			return nil, fmt.Errorf("create MCP config store: %w", err)
		}
		s.mcpStore = store
	}

	if config.Transport == TransportNative && sessions.Supported(string(config.AgentType)) {
		s.sessions, err = sessions.New(string(config.AgentType), config.CWD, sessions.Config{
			Prices:       config.Native.Prices,
			PiSessionDir: config.Native.PiSessionDir,
			Logger:       logger,
		})
		if err != nil {
			return nil, err
		}
	}
	s.startWebhook(config)
	s.registry = s.newInstanceRegistry(config.RegistryDir, bind, config.RestartsInPlace)
	s.startSpend()
	s.updater = newUpdater(config.Update, logger, emitter, stateDB != nil)
	s.updater.watchBlocked(s)

	// Register API routes
	s.registerRoutes()
	// Created here rather than in Start, so Stop can shut it down whichever
	// runs first.
	s.srv = &http.Server{Addr: s.ListenAddr(), Handler: s.router}

	// Start the conversation polling loop if we have an agent IO.
	// AgentIO is nil only when --print-openapi is used (no agent runs).
	// For PTY transport, the process is already running at this point -
	// termexec.StartProcess() blocks until the PTY is created and the process
	// is active. Agent readiness (waiting for the prompt) is handled
	// asynchronously inside conversation.Start() via ReadyForInitialPrompt.
	if s.agentio != nil || s.native != nil {
		s.conversation.Start(ctx)
		s.startMessageQueue()
		s.startUploadPruning()
	}

	// Start the JSONL watcher to capture rich structured messages.
	// This runs alongside the PTY conversation as a sidecar, providing
	// structured content blocks, tool calls, thinking, and usage data.
	s.startJSONLWatcher(config.PTY.AgentPID)

	return s, nil
}

// Handler returns the underlying chi.Router for testing purposes.
func (s *Server) Handler() http.Handler {
	return s.router
}

// MarkAgentExited records the terminal lifecycle state of a PTY agent that
// exited, before the HTTP server shuts down. A nil error represents a clean
// process exit. A native agent that exits is started again (see
// native.Config.RestartDelays).
func (s *Server) MarkAgentExited(err error) {
	if err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return
	}
	s.emitter.SetLifecycle(LifecycleExited)
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// ListenAddr returns the address Start listens on, e.g. ":3284" (all
// interfaces), "127.0.0.1:3284" or "[::1]:3284".
func (s *Server) ListenAddr() string {
	return net.JoinHostPort(s.bind, strconv.Itoa(s.port))
}

// Start starts the HTTP server
// Start serves HTTP until Stop. After Stop it returns http.ErrServerClosed
// at once, even when Stop came first.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.listening(listener.Addr())
	}
	return s.srv.Serve(listener)
}

// instanceChanged tells the summary's subscribers and the registry that
// the instance's ID or URL may have changed.
func (s *Server) instanceChanged() {
	s.emitter.SummaryChanged()
	if s.registry != nil {
		s.registry.changed()
	}
}

// Stop gracefully stops the HTTP server and the agent. It is safe to call
// multiple times.
func (s *Server) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		// Tells the webhook's receiver, while it can still be sent.
		s.webhook.Stop(ctx)
		if s.registry != nil {
			s.registry.stop()
		}
		// Ends the queue loop and the event streams, which the HTTP
		// shutdown would otherwise wait for.
		s.shutdown()
		// No request can change the queue once the HTTP server, the queue
		// loop and the agent have stopped, so the queue is stored after
		// them.
		if s.srv != nil {
			if err = s.srv.Shutdown(ctx); errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
		}
		if s.queueLoop != nil {
			// A tick under way may still send a message.
			_ = s.queueLoop.Wait("messageQueueDispatch")
		}
		// A restart under way was cancelled with shutdownCtx, and lets go
		// of mu while it waits for its agent; from now on none can start
		// another one, or another session-log watcher.
		s.mu.Lock()
		s.stopping = true
		s.stopJSONLWatcher()
		s.mu.Unlock()
		if closeErr := s.backend().close(); closeErr != nil {
			s.logger.Error("Failed to stop the agent", "error", closeErr)
		}
		if s.terminal != nil {
			_ = s.terminal.Close()
		}
		s.persistQueue()
		if s.uploadPruneDone != nil {
			<-s.uploadPruneDone
		}
		s.cleanupUploadDir()
	})
	return err
}

// SaveState writes what the state database hasn't written yet, the queue
// included. It keeps every change as it happens, so this only narrows what
// a crash could lose; source says who asked (SIGUSR1). Without a state
// database there is nothing to save.
func (s *Server) SaveState(source string) error {
	if s.stateDB == nil {
		return nil
	}
	s.persistQueue()
	s.stateDB.Flush()
	s.logger.Info("Saved the state database", "source", source)
	return nil
}
