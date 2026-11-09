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
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/httpmw"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/pricing"
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
	mu         sync.Mutex
	stopOnce   sync.Once
	stopping   bool        // set under mu once Stop closed the agent: no more restarts
	restarting atomic.Bool // set under mu while a restart runs without it (runRestartLocked)
	updating   atomic.Bool // set under mu once an update restarts AgentAPI: messages wait in the queue, and no agent restart begins
	logger     *slog.Logger
	native     *native.Conversation
	sessions   *sessions.Store // the agent's sessions; nil unless it keeps logs
	stateDB    *statedb.DB     // nil unless the conversation is kept
	// queueStoreMu guards what storeQueue last stored in stateDB: the
	// queue, its last ID and the version of the queue they are.
	queueStoreMu      sync.Mutex
	savedQueue        []convstate.QueuedMessage
	savedNextQueueID  int
	savedQueueVersion uint64
	agentType         instanceapi.AgentType
	emitter           *convstate.EventEmitter
	chatBasePath      string
	uploads           *uploadStore
	uploadPruneNow    chan struct{} // nil until startUploadPruning
	uploadPruneDone   chan struct{} // closed once pruning stopped; nil until it starts
	cwd               string
	clock             quartz.Clock
	shutdownCtx       context.Context
	shutdown          context.CancelFunc
	queue             messageQueue  // guarded by mu
	queueLoop         quartz.Waiter // the queue's loop, nil until it starts
	webhook           *webhook.Dispatcher
	registry          *instanceRegistry // nil without a registry directory
	updater           *updater
	handoff           handoffState
	updateAgent       func(context.Context)
	terminal          *shellterm.Terminal // nil without a terminal
}

type ServerConfig struct {
	AgentType      instanceapi.AgentType
	Port           int
	ChatBasePath   string
	AllowedHosts   []string
	AllowedOrigins []string
	InitialPrompt  string
	// InitialPromptSent, if set, is called once the agent got the initial
	// prompt.
	InitialPromptSent func()
	Clock             quartz.Clock
	CWD               string // Working directory (used by Codex resolver)
	// Webhook sends an event whenever the agent status changes.
	Webhook webhook.Config
	// Name is what clients call this AgentAPI (GET /summary, webhook
	// events); empty is the working directory's name.
	Name string
	// Native configures the agent; without a Factory none runs
	// (--print-openapi).
	Native NativeConfig
	// StateDB, if set, stores the conversation and restores it on start.
	StateDB *statedb.DB
	// ResumeSession, if set and there is no StateDB, is the agent's session
	// the server resumes on start: the one it had open before a restart in
	// place. The conversation is read from the session's log, as POST
	// /sessions/{id}/resume reads it; a session whose log can't be read
	// isn't resumed, and the agent starts a new one.
	ResumeSession string
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

// NativeConfig is how the server runs the agent.
type NativeConfig struct {
	// Factory creates the agent's adapter; nil runs no agent.
	Factory native.Factory
	// Terminal offers a shell in the agent's working directory (GET
	// /terminal).
	Terminal bool
	// UpdateAgent, if set, updates the agent's CLI before a restart starts
	// a new one (Codex, Pi).
	UpdateAgent func(context.Context)
	// Prices price the model calls of the agent's sessions; nil is the
	// built-in prices.
	Prices *pricing.Table
	// PiSessionDir is the --session-dir Pi runs with, where its session
	// logs are; empty without one.
	PiSessionDir string
	// Policy is the conversation's: whether it continues a turn a usage
	// limit stopped, and how long it waits before it tries a failed turn
	// again or starts an agent that exited again.
	native.Policy
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
	if hosts, err = httpmw.ParseAllowedHosts(config.AllowedHosts); err != nil {
		return nil, nil, "", fmt.Errorf("failed to parse allowed hosts: %w", err)
	}
	if origins, err = httpmw.ParseAllowedOrigins(config.AllowedOrigins); err != nil {
		return nil, nil, "", fmt.Errorf("failed to parse allowed origins: %w", err)
	}
	if bind, err = httpmw.ParseBindAddress(config.Bind); err != nil {
		return nil, nil, "", fmt.Errorf("invalid bind address: %w", err)
	}
	return hosts, origins, bind, nil
}

// NewServer creates a new server instance
func NewServer(ctx context.Context, config ServerConfig) (*Server, error) {
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

	// The address {ip} in the public URL stands for now, which DHCP may
	// change: the list can't name it.
	var dispatcher *webhook.Dispatcher
	publicIP := func(hostname string) bool { return dispatcher != nil && dispatcher.PublicIP() == hostname }
	router, auth := newRouter(config, logger, allowedHosts, allowedOrigins, publicIP)

	humaConfig := huma.DefaultConfig("AgentAPI", version.Version)
	humaConfig.Info.Description = "HTTP API for Claude Code, Codex and Pi.\n\nhttps://github.com/coder/agentapi"
	api := humachi.New(router, humaConfig)

	dispatcher, err = webhook.New(config.Webhook, logger, config.AgentType, config.Clock)
	if err != nil {
		return nil, fmt.Errorf("configure webhook: %w", err)
	}
	serverID, err := newServerID()
	if err != nil {
		return nil, err
	}
	// The status and the webhook name this run the same.
	dispatcher.SetServerID(serverID)
	stateDB := config.StateDB
	var terminal *shellterm.Terminal
	if config.Native.Terminal {
		terminal = shellterm.New(shellterm.Config{Dir: config.CWD, Env: terminalEnv(), Logger: logger.With("component", "terminal")})
	}
	emitter := newEventEmitter(config, serverID, dispatcher, terminal != nil)
	restored, err := restoreState(config, stateDB, emitter, logger)
	if err != nil {
		return nil, err
	}

	// The directory for uploads: a temporary one unless the conversation
	// is kept, whose messages refer to uploads by path.
	uploads, err := newUploadStore(config.UploadDir)
	if err != nil {
		return nil, err
	}
	logger.Info("Using upload directory", "uploadDir", uploads.dir)

	var history *sessions.Store
	if sessions.Supported(string(config.AgentType)) {
		history, err = sessions.New(string(config.AgentType), config.CWD, sessions.Config{
			Prices:       config.Native.Prices,
			PiSessionDir: config.Native.PiSessionDir,
			Logger:       logger,
		})
		if err != nil {
			return nil, err
		}
	}
	if stateDB == nil && config.ResumeSession != "" {
		restored = resumeCarriedSession(config.ResumeSession, history, emitter, uploads, logger)
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	s := &Server{
		router:       router,
		api:          api,
		port:         config.Port,
		bind:         bind,
		auth:         auth,
		native:       newNativeConversation(config, restored, emitter, uploads, logger),
		logger:       logger,
		agentType:    config.AgentType,
		emitter:      emitter,
		chatBasePath: strings.TrimSuffix(config.ChatBasePath, "/"),
		uploads:      uploads,
		cwd:          config.CWD,
		clock:        config.Clock,
		shutdownCtx:  shutdownCtx,
		shutdown:     shutdownCancel,
		webhook:      dispatcher,
		updateAgent:  config.Native.UpdateAgent,
		terminal:     terminal,
		stateDB:      stateDB,
		sessions:     history,
	}
	// Clients follow the queue through queue_update events, and the state
	// database has every change: a message that went out isn't sent again
	// after a crash.
	s.queue.onChange = func(queue []convstate.QueuedMessage, lastID int, version uint64) {
		emitter.SetQueue(queue, version)
		s.storeQueue(queue, lastID, version)
	}
	// The summary and the registry entry name the new address.
	dispatcher.OnURLChange(s.instanceChanged)
	if stateDB != nil {
		s.restoreQueue(restored.snapshot)
		restoreWebhook(dispatcher, restored.snapshot, logger)
	}

	s.startWebhook(config)
	s.registry = s.newInstanceRegistry(config.RegistryDir, bind, config.RestartsInPlace)
	s.startSpend()
	// Without a state database, a restart in place carries the agent's
	// session, which the next run resumes if it can read its log.
	s.updater = newUpdater(config.Update, logger, emitter, stateDB != nil, stateDB != nil || s.sessions != nil)
	s.updater.watchBlocked(s)

	// Register API routes
	s.registerRoutes()
	// Created here rather than in Start, so Stop can shut it down whichever
	// runs first. A client gets 10 s to send a request's headers and an idle
	// keep-alive connection is closed after 2 minutes; there is no write
	// timeout, as the event streams stay open.
	s.srv = &http.Server{
		Addr:              s.ListenAddr(),
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// Without a factory (--print-openapi) no agent runs.
	if config.Native.Factory != nil {
		s.native.Start(ctx)
		s.startMessageQueue()
		s.startUploadPruning()
	}
	return s, nil
}

// newServerID returns a random ID for this run of the server.
func newServerID() (string, error) {
	var serverBytes [16]byte
	if _, err := rand.Read(serverBytes[:]); err != nil {
		return "", fmt.Errorf("generate server ID: %w", err)
	}
	return hex.EncodeToString(serverBytes[:]), nil
}

// newEventEmitter returns the emitter of the conversation's events, which
// tells dispatcher of status changes, keeps the conversation in the state
// database if there is one and, with withTerminal, says a terminal is
// offered.
func newEventEmitter(config ServerConfig, serverID string, dispatcher *webhook.Dispatcher, withTerminal bool) *convstate.EventEmitter {
	options := []convstate.EventEmitterOption{
		convstate.WithAgentType(config.AgentType),
		convstate.WithServerID(serverID),
		convstate.WithClock(config.Clock),
		convstate.WithStatusChangeHandler(dispatcher.StatusChanged),
		convstate.WithInstance(dispatcher.Instance),
	}
	if config.StateDB != nil {
		options = append(options, convstate.WithPersister(config.StateDB))
	}
	if withTerminal {
		options = append(options, convstate.WithTerminal())
	}
	return convstate.NewEventEmitter(options...)
}

// newRouter returns the router with the middleware every request goes
// through: the host check (publicIP says whether a hostname is the
// webhook's current public address), request logs, compression, CORS, the
// cross-origin check and, with an API token, its authentication.
func newRouter(config ServerConfig, logger *slog.Logger, allowedHosts, allowedOrigins []string, publicIP func(string) bool) (chi.Router, *tokenAuth) {
	router := chi.NewMux()

	// Enforce allowed hosts in a custom middleware that ignores the port during matching.
	badHostHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid host header. Allowed hosts: "+strings.Join(allowedHosts, ", "), http.StatusBadRequest)
	})
	router.Use(httpmw.HostAuthorization(allowedHosts, publicIP, badHostHandler))

	router.Use(requestLogMiddleware(logger))
	router.Use(httpmw.Compress())

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
	router.Use(httpmw.CrossOriginProtection(allowedOrigins, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	return router, auth
}

// newNativeConversation returns the conversation of a native agent, which
// continues the restored one.
func newNativeConversation(config ServerConfig, restored restoredState, emitter *convstate.EventEmitter, uploads *uploadStore, logger *slog.Logger) *native.Conversation {
	return native.NewConversation(native.Config{
		Factory:           config.Native.Factory,
		ResumeRef:         restored.resumeRef,
		State:             restored.state,
		Policy:            config.Native.Policy,
		InitialMessages:   restored.messages,
		InitialPrompt:     config.InitialPrompt,
		InitialPromptSent: config.InitialPromptSent,
		Emitter:           emitter,
		Observer:          &nativeObserver{emitter: emitter},
		Logger:            logger,
		Clock:             config.Clock,
		PrepareRichMessage: func(msg *transcript.RichMessage) {
			uploads.storeRichImages(msg, logger)
		},
	})
}

// restoreWebhook restores the webhook settings stored in snap, which were
// changed through the API, unless its flags changed since.
func restoreWebhook(dispatcher *webhook.Dispatcher, snap statedb.Snapshot, logger *slog.Logger) {
	stored := snap.Meta[statedb.MetaWebhook]
	if stored == "" {
		return
	}
	switch ok, err := dispatcher.Restore(stored); {
	case err != nil:
		logger.Warn("Ignoring the stored webhook settings", "error", err)
	case ok:
		logger.Info("Restored the webhook settings changed through the API")
	default:
		logger.Info("The webhook flags changed since its settings were changed through the API; using the flags")
	}
}

// Handler returns the server's router. The tests of lib/httpapi and of
// the hub (which runs real servers behind it) serve it with httptest.
func (s *Server) Handler() http.Handler {
	return s.router
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
		// another one.
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		if closeErr := s.native.Close(); closeErr != nil {
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
