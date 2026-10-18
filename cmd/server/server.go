package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/statedb"
)

type AgentType = msgfmt.AgentType

const (
	AgentTypeClaude   AgentType = msgfmt.AgentTypeClaude
	AgentTypeGoose    AgentType = msgfmt.AgentTypeGoose
	AgentTypeAider    AgentType = msgfmt.AgentTypeAider
	AgentTypeCodex    AgentType = msgfmt.AgentTypeCodex
	AgentTypeGemini   AgentType = msgfmt.AgentTypeGemini
	AgentTypeCopilot  AgentType = msgfmt.AgentTypeCopilot
	AgentTypeAmp      AgentType = msgfmt.AgentTypeAmp
	AgentTypeCursor   AgentType = msgfmt.AgentTypeCursor
	AgentTypeAuggie   AgentType = msgfmt.AgentTypeAuggie
	AgentTypeAmazonQ  AgentType = msgfmt.AgentTypeAmazonQ
	AgentTypeOpencode AgentType = msgfmt.AgentTypeOpencode
	AgentTypeKimi     AgentType = msgfmt.AgentTypeKimi
	AgentTypePi       AgentType = msgfmt.AgentTypePi
	AgentTypeCustom   AgentType = msgfmt.AgentTypeCustom
)

// agentTypeAliases contains the mapping of possible input agent type strings to their canonical AgentType values
var agentTypeAliases = map[string]AgentType{
	"claude":       AgentTypeClaude,
	"goose":        AgentTypeGoose,
	"aider":        AgentTypeAider,
	"codex":        AgentTypeCodex,
	"gemini":       AgentTypeGemini,
	"copilot":      AgentTypeCopilot,
	"amp":          AgentTypeAmp,
	"auggie":       AgentTypeAuggie,
	"cursor":       AgentTypeCursor,
	"cursor-agent": AgentTypeCursor,
	"q":            AgentTypeAmazonQ,
	"amazonq":      AgentTypeAmazonQ,
	"opencode":     AgentTypeOpencode,
	"kimi":         AgentTypeKimi,
	"pi":           AgentTypePi,
	"custom":       AgentTypeCustom,
}

func parseAgentType(firstArg string, agentTypeVar string) (AgentType, error) {
	// if the agent type is provided, use it
	if castedAgentType, ok := agentTypeAliases[agentTypeVar]; ok {
		return castedAgentType, nil
	}
	if agentTypeVar != "" {
		return AgentTypeCustom, fmt.Errorf("invalid agent type: %s", agentTypeVar)
	}
	// if the agent type is not provided, guess it from the first argument
	if castedFirstArg, ok := agentTypeAliases[firstArg]; ok {
		return castedFirstArg, nil
	}
	return AgentTypeCustom, nil
}

func runServer(ctx context.Context, logger *slog.Logger, args []string) error {
	opts, err := loadServerOptions(viper.GetViper(), args)
	if err != nil {
		return err
	}
	if opts.initialPrompt == "" {
		if opts.initialPrompt, err = readPipedPrompt(logger); err != nil {
			return err
		}
	}
	if opts.pidFile != "" {
		if err := writePIDFile(opts.pidFile, logger); err != nil {
			return fmt.Errorf("failed to write PID file: %w", err)
		}
		defer cleanupPIDFile(opts.pidFile, logger)
	}

	cwd, _ := os.Getwd()
	agent, err := startAgent(ctx, opts, cwd, logger)
	if err != nil {
		return err
	}
	var stateDB *statedb.DB
	if opts.stateDB != "" && !opts.printOpenAPI {
		if stateDB, err = statedb.Open(opts.stateDB, logger); err != nil {
			return err
		}
		defer func() {
			if err := stateDB.Close(); err != nil {
				logger.Error("Failed to close the state database", "error", err)
			}
		}()
	}
	apiToken, err := resolveAPIToken(opts, logger)
	if err != nil {
		return err
	}
	srv, err := httpapi.NewServer(ctx, serverConfig(opts, agent, stateDB, apiToken, cwd))
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}
	if opts.printOpenAPI {
		fmt.Println(srv.GetOpenAPI())
		return nil
	}
	return serve(ctx, logger, srv, agent)
}

// resolveAPIToken returns the token the API asks for: none, the given one,
// or a random one, which it prints with the chat's sign-in URL.
func resolveAPIToken(opts serverOptions, logger *slog.Logger) (string, error) {
	switch opts.apiToken {
	case "":
		return "", nil
	case "generate":
		var tokenBytes [32]byte
		if _, err := rand.Read(tokenBytes[:]); err != nil {
			return "", fmt.Errorf("generate API token: %w", err)
		}
		token := hex.EncodeToString(tokenBytes[:])
		fmt.Fprintf(os.Stderr, "API token: %s\n", token)
		// The token goes in the URL fragment, which the browser never sends
		// to a server or proxy; the chat UI trades it for a session cookie.
		fmt.Fprintf(os.Stderr, "Chat UI: %s\n", chatSignInURL(opts.bind, opts.port, token))
		return token, nil
	default:
		logger.Info("API token authentication enabled")
		return opts.apiToken, nil
	}
}

// serverConfig is the configuration of the server for opts and agent.
func serverConfig(opts serverOptions, agent *runningAgent, stateDB *statedb.DB, apiToken, cwd string) httpapi.ServerConfig {
	config := httpapi.ServerConfig{
		AgentType:              opts.agentType,
		AgentIO:                agent.io,
		Transport:              opts.transport,
		Port:                   opts.port,
		ChatBasePath:           opts.chatBasePath,
		AllowedHosts:           opts.allowedHosts,
		AllowedOrigins:         opts.allowedOrigins,
		InitialPrompt:          opts.initialPrompt,
		AgentPID:               agent.pid(),
		CWD:                    cwd,
		RestartAgent:           agent.restart(),
		APIToken:               apiToken,
		Bind:                   opts.bind,
		NativeFactory:          agent.factory,
		UpdateAgent:            agent.updateNative,
		Terminal:               opts.terminal,
		StateDB:                stateDB,
		Webhook:                opts.webhook,
		StatePersistenceConfig: opts.state,
	}
	if opts.transport == httpapi.TransportPTY {
		config.TerminalColumns = opts.termWidth
	}
	if stateDB != nil {
		// Messages refer to uploads by path, so they are kept with the
		// state.
		config.UploadDir = opts.stateDB + ".uploads"
	}
	return config
}

// serve runs the server until a signal, the agent's exit or a failed
// listener stops it, then stops the server and the agent.
func serve(ctx context.Context, logger *slog.Logger, srv *httpapi.Server, agent *runningAgent) error {
	gracefulCtx, gracefulCancel := context.WithCancel(ctx)
	defer gracefulCancel()
	// Signals end gracefulCtx.
	handleSignals(gracefulCtx, gracefulCancel, logger, srv)
	logger.Info("Starting server", "address", srv.ListenAddr())

	// One monitor waits for the agent: its exit stops the server.
	exitCh := make(chan error, 1)
	go func() {
		defer close(exitCh)
		exited, err := agent.waitForExit(gracefulCtx, srv)
		if !exited {
			return
		}
		srv.MarkAgentExited(err)
		if err != nil {
			exitCh <- err
		}
		gracefulCancel()
	}()

	serverErrCh := make(chan error, 1)
	go func() {
		defer close(serverErrCh)
		if err := srv.Start(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	// stopServer stops the HTTP server and a native agent.
	stopServer := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Stop(shutdownCtx); err != nil {
			logger.Error("Failed to stop HTTP server", "error", err)
		}
	}
	select {
	case err := <-serverErrCh:
		if err != nil {
			// The agent started before the listener failed.
			stopServer()
			agent.close(logger)
			return fmt.Errorf("failed to start server: %w", err)
		}
	case <-gracefulCtx.Done():
	}

	if err := srv.SaveState("shutdown"); err != nil {
		logger.Error("Failed to save state during shutdown", "error", err)
	}
	stopServer()
	select {
	case err := <-exitCh:
		if err != nil {
			return fmt.Errorf("agent exited with error: %w", err)
		}
	default:
		agent.close(logger)
	}
	return nil
}

// chatSignInURL returns the chat UI URL that signs the browser in with
// token. A server listening on all interfaces is opened through localhost.
func chatSignInURL(bind string, port int, token string) string {
	host := bind
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s/chat/embed#token=%s", net.JoinHostPort(host, strconv.Itoa(port)), url.QueryEscape(token))
}

var agentNames = (func() []string {
	names := make([]string, 0, len(agentTypeAliases))
	for agentType := range agentTypeAliases {
		names = append(names, agentType)
	}
	sort.Strings(names)
	return names
})()

// writePIDFile writes the current process ID to the specified file
func writePIDFile(pidFile string, logger *slog.Logger) error {
	pid := os.Getpid()
	pidContent := fmt.Sprintf("%d\n", pid)

	// Create directory if it doesn't exist
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create PID file directory: %w", err)
	}

	// Create the PID file exclusively so two servers cannot both pass a
	// check-then-write sequence. If the file belongs to a dead process (or is
	// malformed), remove it and retry the exclusive create.
	for {
		file, err := os.OpenFile(pidFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, writeErr := file.WriteString(pidContent); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(pidFile)
				return fmt.Errorf("failed to write PID file: %w", writeErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(pidFile)
				return fmt.Errorf("failed to close PID file: %w", closeErr)
			}
			break
		}
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create PID file: %w", err)
		}

		existingPIDData, readErr := os.ReadFile(pidFile)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue // The owner removed it between OpenFile and ReadFile.
			}
			return fmt.Errorf("failed to read existing PID file: %w", readErr)
		}

		existingPIDStr := strings.TrimSpace(string(existingPIDData))
		existingPID, parseErr := strconv.Atoi(existingPIDStr)
		if parseErr == nil && isProcessRunning(existingPID) {
			return fmt.Errorf("another instance is already running with PID %d (PID file: %s)", existingPID, pidFile)
		}

		logger.Warn("Removing stale PID file before retrying", "pidFile", pidFile, "stalePID", existingPIDStr)
		if removeErr := os.Remove(pidFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("failed to remove stale PID file: %w", removeErr)
		}
	}

	logger.Info("Wrote PID file", "pidFile", pidFile, "pid", pid)
	return nil
}

// cleanupPIDFile removes the PID file if it was written by this process.
func cleanupPIDFile(pidFile string, logger *slog.Logger) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Error("Failed to read PID file for cleanup", "pidFile", pidFile, "error", err)
		}
		return
	}
	pidStr := strings.TrimSpace(string(data))
	filePID, err := strconv.Atoi(pidStr)
	if err != nil || filePID != os.Getpid() {
		logger.Info("PID file belongs to another process, skipping cleanup", "pidFile", pidFile, "filePID", pidStr)
		return
	}
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to remove PID file", "pidFile", pidFile, "error", err)
	} else if err == nil {
		logger.Info("Removed PID file", "pidFile", pidFile)
	}
}

type flagSpec struct {
	name         string
	shorthand    string
	defaultValue any
	usage        string
	flagType     string
}

const (
	FlagType                   = "type"
	FlagPort                   = "port"
	FlagPrintOpenAPI           = "print-openapi"
	FlagChatBasePath           = "chat-base-path"
	FlagTermWidth              = "term-width"
	FlagTermHeight             = "term-height"
	FlagAllowedHosts           = "allowed-hosts"
	FlagAllowedOrigins         = "allowed-origins"
	FlagExit                   = "exit"
	FlagInitialPrompt          = "initial-prompt"
	FlagStateFile              = "state-file"
	FlagLoadState              = "load-state"
	FlagSaveState              = "save-state"
	FlagPidFile                = "pid-file"
	FlagExperimentalACP        = "experimental-acp"
	FlagTransport              = "transport"
	FlagStateDB                = "state-db"
	FlagTerminal               = "terminal"
	FlagWebhookURL             = "webhook-url"
	FlagWebhookTimeout         = "webhook-timeout"
	FlagWebhookMaxAttempts     = "webhook-max-attempts"
	FlagWebhookPayloadTemplate = "webhook-payload-template"
	FlagAPIToken               = "api-token"
	FlagBind                   = "bind"
)

func CreateServerCmd() *cobra.Command {
	serverCmd := &cobra.Command{
		Use:   "server [agent]",
		Short: "Run the server",
		Long:  fmt.Sprintf("Run the server with the specified agent (one of: %s)", strings.Join(agentNames, ", ")),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The arguments are fine: an error from here on isn't a usage
			// error.
			cmd.SilenceUsage = true
			// The --exit flag is used for testing validation of flags in the test suite
			if viper.GetBool(FlagExit) {
				return nil
			}
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			if viper.GetBool(FlagPrintOpenAPI) {
				// We don't want log output here.
				logger = slog.New(logctx.DiscardHandler)
			}
			ctx := logctx.WithLogger(context.Background(), logger)
			return runServer(ctx, logger, cmd.Flags().Args())
		},
	}

	flagSpecs := []flagSpec{
		{FlagType, "t", "", fmt.Sprintf("Override the agent type (one of: %s, custom)", strings.Join(agentNames, ", ")), "string"},
		{FlagPort, "p", 3284, "Port to run the server on", "int"},
		{FlagPrintOpenAPI, "P", false, "Print the OpenAPI schema to stdout and exit", "bool"},
		{FlagChatBasePath, "c", "/chat", "Base path for assets and routes used in the static files of the chat interface", "string"},
		{FlagTermWidth, "W", uint16(80), "Width of the emulated terminal", "uint16"},
		{FlagTermHeight, "H", uint16(1000), "Height of the emulated terminal", "uint16"},
		// localhost is the default host for the server. Port is ignored during matching.
		{FlagAllowedHosts, "a", []string{"localhost", "127.0.0.1", "[::1]"}, "HTTP allowed hosts (hostnames only, no ports). Use '*' for all, comma-separated list via flag, space-separated list via AGENTAPI_ALLOWED_HOSTS env var", "stringSlice"},
		// localhost:3284 is the default origin when you open the chat interface in your browser. localhost:3000 and 3001 are used during development.
		{FlagAllowedOrigins, "o", []string{"http://localhost:3284", "http://localhost:3000", "http://localhost:3001"}, "HTTP allowed origins. Use '*' for all, comma-separated list via flag, space-separated list via AGENTAPI_ALLOWED_ORIGINS env var", "stringSlice"},
		{FlagInitialPrompt, "I", "", "Initial prompt for the agent. Recommended only if the agent doesn't support initial prompt in interaction mode. Will be read from stdin if piped (e.g., echo 'prompt' | agentapi server -- my-agent)", "string"},
		{FlagStateFile, "s", "", "Path to file for saving/loading server state", "string"},
		{FlagLoadState, "", false, "Load state from state-file on startup (defaults to true when state-file is set)", "bool"},
		{FlagSaveState, "", false, "Save state to state-file on shutdown (defaults to true when state-file is set)", "bool"},
		{FlagPidFile, "", "", "Path to file where the server process ID will be written for shutdown scripts", "string"},
		{FlagExperimentalACP, "", false, "Use experimental ACP transport instead of PTY", "bool"},
		{FlagStateDB, "", "", "Path to a SQLite file that keeps the conversation (messages, tool calls, requests, queue) and resumes the agent's session when AgentAPI restarts. Native transport only", "string"},
		{FlagTransport, "", "pty", "How to drive the agent: pty (its terminal UI), acp (same as --experimental-acp) or native (its own structured protocol; claude, codex and pi)", "string"},
		{FlagTerminal, "", true, "Offer a shell in the agent's working directory to the chat (its Terminal button) and at GET /terminal. Native transport only, where the agent has no terminal of its own", "bool"},
		{FlagWebhookURL, "", "", "URL notified when the agent run status changes", "string"},
		{FlagWebhookTimeout, "", 10 * time.Second, "Timeout for each webhook delivery attempt", "duration"},
		{FlagWebhookMaxAttempts, "", 3, "Maximum webhook delivery attempts", "int"},
		{FlagWebhookPayloadTemplate, "", "", "Go text/template for custom webhook POST body. Available fields: .ID, .Type, .CreatedAt, .RunID, .Status, .PreviousStatus, .AgentType, .Transport", "string"},
		{FlagAPIToken, "", "", "API token for Bearer authentication. If set without a value, a random token is generated. Empty (default) disables authentication.", "string"},
		{FlagBind, "", "", "Address to listen on: an IP address or hostname without a port (e.g. 127.0.0.1 or ::1). Empty (default) listens on all interfaces", "string"},
	}

	for _, spec := range flagSpecs {
		switch spec.flagType {
		case "string":
			serverCmd.Flags().StringP(spec.name, spec.shorthand, spec.defaultValue.(string), spec.usage)
		case "int":
			serverCmd.Flags().IntP(spec.name, spec.shorthand, spec.defaultValue.(int), spec.usage)
		case "bool":
			serverCmd.Flags().BoolP(spec.name, spec.shorthand, spec.defaultValue.(bool), spec.usage)
		case "uint16":
			serverCmd.Flags().Uint16P(spec.name, spec.shorthand, spec.defaultValue.(uint16), spec.usage)
		case "stringSlice":
			serverCmd.Flags().StringSliceP(spec.name, spec.shorthand, spec.defaultValue.([]string), spec.usage)
		case "duration":
			serverCmd.Flags().DurationP(spec.name, spec.shorthand, spec.defaultValue.(time.Duration), spec.usage)
		default:
			panic(fmt.Sprintf("unknown flag type: %s", spec.flagType))
		}
		if err := viper.BindPFlag(spec.name, serverCmd.Flags().Lookup(spec.name)); err != nil {
			panic(fmt.Sprintf("failed to bind flag %s: %v", spec.name, err))
		}
	}

	// --api-token without a value auto-generates a token; --api-token=VALUE
	// uses the given value; omitting the flag entirely disables authentication.
	serverCmd.Flags().Lookup(FlagAPIToken).NoOptDefVal = "generate"

	serverCmd.Flags().Bool(FlagExit, false, "Exit immediately after parsing arguments")
	if err := serverCmd.Flags().MarkHidden(FlagExit); err != nil {
		panic(fmt.Sprintf("failed to mark flag %s as hidden: %v", FlagExit, err))
	}
	if err := viper.BindPFlag(FlagExit, serverCmd.Flags().Lookup(FlagExit)); err != nil {
		panic(fmt.Sprintf("failed to bind flag %s: %v", FlagExit, err))
	}

	viper.SetEnvPrefix("AGENTAPI")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	return serverCmd
}
