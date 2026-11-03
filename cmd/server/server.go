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
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/native/demo"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/statedb"
)

type AgentType = httpapi.AgentType

const (
	AgentTypeClaude = httpapi.AgentTypeClaude
	AgentTypeCodex  = httpapi.AgentTypeCodex
	AgentTypePi     = httpapi.AgentTypePi
	AgentTypeCustom = httpapi.AgentTypeCustom
)

// agentTypeAliases are the agent types --type takes, and the programs
// whose type is known without it. custom is native-demo's type
// (lib/native/demo), which it reports.
var agentTypeAliases = map[string]AgentType{
	"claude":      AgentTypeClaude,
	"codex":       AgentTypeCodex,
	"pi":          AgentTypePi,
	"native-demo": AgentTypeCustom,
	"custom":      AgentTypeCustom,
}

// agentNames are the agent types --type takes, as help names them.
const agentNames = "claude, codex, pi, native-demo"

// lastPTYRelease is the last release that drives other agents (through
// their terminal UI).
const lastPTYRelease = "v2.5.1"

// unsupportedAgent is the error for an agent AgentAPI can't run.
func unsupportedAgent(name string) error {
	return fmt.Errorf("AgentAPI 3 runs Claude Code, Codex and Pi natively, not %s (--type %s); for other agents, use AgentAPI %s, the last release with the PTY transport", name, agentNames, lastPTYRelease)
}

// parseAgentType returns the agent type --type (typeFlag) names, or
// without it the type of program, the agent's command. The custom type
// runs native-demo only.
func parseAgentType(program, typeFlag string) (AgentType, error) {
	name := typeFlag
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(program), ".exe")
	}
	agentType, ok := agentTypeAliases[name]
	switch {
	case !ok && typeFlag == "":
		return "", fmt.Errorf("unknown agent %s: set --type (%s); AgentAPI 3 runs Claude Code, Codex and Pi natively, and for other agents AgentAPI %s is the last release with the PTY transport", program, agentNames, lastPTYRelease)
	case !ok:
		return "", unsupportedAgent(name)
	case agentType == AgentTypeCustom && program != demo.Program:
		return "", unsupportedAgent("a custom agent")
	}
	return agentType, nil
}

// runServer runs the server for args until it stops. It returns errRestart
// once SIGUSR2 stopped it to run self again.
func runServer(ctx context.Context, logger *slog.Logger, args []string, self *selfExec) (err error) {
	opts, err := loadServerOptions(viper.GetViper(), args)
	if err != nil {
		return err
	}
	if !opts.printOpenAPI {
		logger.Info("Driving the agent", "agentType", opts.agentType)
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
		defer func() {
			// After a restart in place, the process goes on with the same
			// ID.
			if !errors.Is(err, errRestart) {
				cleanupPIDFile(opts.pidFile, logger)
			}
		}()
	}

	prices := pricing.Builtin()
	if opts.prices != "" {
		if prices, err = prices.WithFile(opts.prices); err != nil {
			return fmt.Errorf("--prices: %w", err)
		}
	}
	cwd, _ := os.Getwd()
	agent, err := startAgent(opts, cwd, logger)
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
		if opts.stateFile != "" {
			importStateFile(stateDB, opts, cwd, logger)
		}
		if self != nil {
			self.stateDB = stateDB
		}
	}
	apiToken, err := resolveAPIToken(opts, logger, self)
	if err != nil {
		return err
	}
	srv, err := httpapi.NewServer(ctx, serverConfig(opts, agent, stateDB, apiToken, cwd, prices, self))
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}
	if opts.printOpenAPI {
		fmt.Println(srv.GetOpenAPI())
		return nil
	}
	if warning := exposureWarning(opts.bind, apiToken, opts.terminal); warning != "" {
		logger.Warn(warning)
	}
	return serve(ctx, logger, srv, self)
}

// generatedAPITokenEnv carries a generated API token to the program a
// restart in place runs: it gets the same arguments, and --api-token
// without a value would generate another token, signing every client out.
// procenv keeps it from the agent, as every AGENTAPI_*_TOKEN.
const generatedAPITokenEnv = "AGENTAPI_GENERATED_API_TOKEN"

// resolveAPIToken returns the token the API asks for: none, the given one,
// or a random one, which it prints with the chat's sign-in URL. A random
// one is kept for self to run again with (generatedAPITokenEnv); after a
// restart in place, it is the one generated before.
func resolveAPIToken(opts serverOptions, logger *slog.Logger, self *selfExec) (string, error) {
	kept := os.Getenv(generatedAPITokenEnv)
	_ = os.Unsetenv(generatedAPITokenEnv)
	self.setEnv(generatedAPITokenEnv, "")
	switch opts.apiToken {
	case "":
		return "", nil
	case "generate":
		token := kept
		if isGeneratedAPIToken(token) {
			logger.Info("Kept the generated API token across the restart in place")
		} else {
			var tokenBytes [32]byte
			if _, err := rand.Read(tokenBytes[:]); err != nil {
				return "", fmt.Errorf("generate API token: %w", err)
			}
			token = hex.EncodeToString(tokenBytes[:])
		}
		self.setEnv(generatedAPITokenEnv, token)
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

// isGeneratedAPIToken reports whether token is one resolveAPIToken
// generates: 32 random bytes in hex.
func isGeneratedAPIToken(token string) bool {
	decoded, err := hex.DecodeString(token)
	return err == nil && len(decoded) == 32
}

// serverConfig is the configuration of the server for opts and agent;
// self, if set, restarts it in place.
func serverConfig(opts serverOptions, agent *runningAgent, stateDB *statedb.DB, apiToken, cwd string, prices *pricing.Table, self *selfExec) httpapi.ServerConfig {
	config := httpapi.ServerConfig{
		AgentType:      opts.agentType,
		Port:           opts.port,
		ChatBasePath:   opts.chatBasePath,
		AllowedHosts:   opts.allowedHosts,
		AllowedOrigins: opts.allowedOrigins,
		InitialPrompt:  opts.initialPrompt,
		CWD:            cwd,
		APIToken:       apiToken,
		Bind:           opts.bind,
		Webhook:        opts.webhook,
		Name:           opts.name,
		StateDB:        stateDB,
		RegistryDir:    opts.registryDir,
		// SIGUSR2 (handleSignals).
		RestartsInPlace: runtime.GOOS != "windows",
		Update:          httpapi.UpdateConfig{Allowed: opts.allowUpdate},
	}
	if self != nil {
		config.Update.Program = self.path
		if config.RestartsInPlace {
			config.Update.Restart = func(ctx context.Context) error { return self.restart(ctx, "update") }
		}
	}
	if !opts.printOpenAPI {
		config.Native = httpapi.NativeConfig{
			Factory:      agent.factory,
			UpdateAgent:  agent.updateNative,
			Terminal:     opts.terminal,
			AutoContinue: opts.autoContinue,
			Prices:       prices,
		}
		if opts.agentType == AgentTypePi {
			config.Native.PiSessionDir = piSessionDir(opts.agentArgs)
		}
	}
	if stateDB != nil {
		// Messages refer to uploads by path, so they are kept with the
		// state.
		config.UploadDir = opts.stateDB + ".uploads"
	}
	return config
}

// serve runs the server until a signal or a failed listener stops it, then
// stops the server and the agent. An agent that exits is started again,
// and the server goes on serving. It returns errRestart once SIGUSR2
// stopped it to run self again; self may be nil.
func serve(ctx context.Context, logger *slog.Logger, srv *httpapi.Server, self *selfExec) error {
	gracefulCtx, gracefulCancel := context.WithCancel(ctx)
	defer gracefulCancel()
	// Signals and restarts in place end gracefulCtx.
	self.arm(gracefulCtx, gracefulCancel, logger)
	handleSignals(gracefulCtx, gracefulCancel, logger, srv, self)
	logger.Info("Starting server", "address", srv.ListenAddr())

	serverErrCh := make(chan error, 1)
	go func() {
		defer close(serverErrCh)
		if err := srv.Start(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	var err error
	select {
	case err = <-serverErrCh:
	case <-gracefulCtx.Done():
	}
	// Stops the HTTP server and the agent, which started before a
	// listener that failed.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stopErr := srv.Stop(shutdownCtx); stopErr != nil {
		logger.Error("Failed to stop HTTP server", "error", stopErr)
	}
	if err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	if self.restarting() {
		return errRestart
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

// exposureWarning returns the warning for a server that anyone who can
// connect to it may use: it asks for no API token and listens on an address
// other machines can reach. It returns "" otherwise. terminal says whether
// the server offers its shell terminal.
func exposureWarning(bind, apiToken string, terminal bool) string {
	if apiToken != "" || isLoopbackHost(bind) {
		return ""
	}
	what := "the agent"
	if terminal {
		what = "the agent and the shell terminal"
	}
	return "No API token is set and the server listens on an address other machines can reach, " +
		"so anyone who can connect to it can use " + what +
		"; set --api-token, or --bind 127.0.0.1 to accept only local connections"
}

// isLoopbackHost reports whether listening on host (an IP address, a
// hostname, or "" for every interface) accepts only local connections.
func isLoopbackHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(strings.TrimSuffix(host, "."), "localhost")
}

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
		if parseErr == nil && existingPID == pid {
			// This process wrote it before it restarted in place.
			logger.Info("The PID file holds this process already", "pidFile", pidFile, "pid", pid)
			return nil
		}
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
	FlagAutoContinue           = "auto-continue"
	FlagPrices                 = "prices"
	FlagWebhookURL             = "webhook-url"
	FlagWebhookTimeout         = "webhook-timeout"
	FlagWebhookMaxAttempts     = "webhook-max-attempts"
	FlagWebhookPayloadTemplate = "webhook-payload-template"
	FlagWebhookEvents          = "webhook-events"
	FlagWebhookSecret          = "webhook-secret"
	FlagWebhookHeartbeat       = "webhook-heartbeat"
	FlagWebhookInstanceID      = "webhook-instance-id"
	FlagInstanceID             = "instance-id"
	FlagName                   = "name"
	FlagRegistry               = "registry"
	FlagConfig                 = "config"
	FlagRegistryDir            = "registry-dir"
	FlagPublicURL              = "public-url"
	FlagAPIToken               = "api-token"
	FlagBind                   = "bind"
	FlagLogLevel               = "log-level"
	FlagAllowUpdate            = "allow-update"
)

func CreateServerCmd() *cobra.Command {
	serverCmd := &cobra.Command{
		Use:   "server [agent]",
		Short: "Run the server",
		Long:  "Run the server with the specified agent (one of: " + agentNames + ")",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runServerCmd,
	}

	for _, spec := range serverFlagSpecs() {
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

	// --experimental-acp says that ACP was removed rather than that the
	// flag is unknown.
	markHidden(serverCmd, FlagExperimentalACP)

	serverCmd.Flags().Bool(FlagValidate, false, "Check the settings as a start would, without starting anything, print ok or why it wouldn't start, and exit")
	markHidden(serverCmd, FlagValidate)

	serverCmd.Flags().Bool(FlagExit, false, "Exit immediately after parsing arguments")
	markHidden(serverCmd, FlagExit)
	if err := viper.BindPFlag(FlagExit, serverCmd.Flags().Lookup(FlagExit)); err != nil {
		panic(fmt.Sprintf("failed to bind flag %s: %v", FlagExit, err))
	}

	viper.SetEnvPrefix("AGENTAPI")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	return serverCmd
}

// markHidden hides the flag name of cmd from its help.
func markHidden(cmd *cobra.Command, name string) {
	if err := cmd.Flags().MarkHidden(name); err != nil {
		panic(fmt.Sprintf("failed to mark flag %s as hidden: %v", name, err))
	}
}

// runServerCmd runs agentapi server: it checks the settings (--validate),
// or runs the server and, once SIGUSR2 stopped it, the program again.
func runServerCmd(cmd *cobra.Command, _ []string) error {
	// The arguments are fine: an error from here on isn't a usage
	// error.
	cmd.SilenceUsage = true
	// First, so that a SIGUSR2 while the server starts doesn't end
	// it (listenForRestart).
	self := newSelfExec()
	// The --exit flag is used for testing validation of flags in the test suite
	if viper.GetBool(FlagExit) {
		return nil
	}
	// Not bound to viper: a variable or the config file doesn't
	// turn the server into a check.
	validate, _ := cmd.Flags().GetBool(FlagValidate)
	level := new(slog.LevelVar)
	logger := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{Level: level}))
	if viper.GetBool(FlagPrintOpenAPI) || validate {
		// We don't want log output here: --validate prints one
		// line.
		logger = slog.New(logctx.DiscardHandler)
	}
	ctx := logctx.WithLogger(context.Background(), logger)
	// The schema doesn't depend on the user's settings, and a
	// webhook in them (agentapi hub join) would have this short
	// run report to the hub (make gen runs it).
	if !viper.GetBool(FlagPrintOpenAPI) {
		readUserConfig(viper.GetViper(), cmd.Flags(), logger)
	}
	// Once the config file, which can set it too, was read; what
	// reading it logged went out at info.
	logLevel, err := logctx.ParseLevel(viper.GetString(FlagLogLevel))
	if err != nil {
		return fmt.Errorf("invalid --%s: %w", FlagLogLevel, err)
	}
	level.Set(logLevel)
	if validate {
		return validateServer(cmd.OutOrStdout(), cmd.Flags().Args())
	}
	logger.Debug("Restarting in place runs this program", "program", self.path)
	err = runServer(ctx, logger, cmd.Flags().Args(), self)
	if errors.Is(err, errRestart) {
		logger.Info("Running the program again", "program", self.path)
		if err := self.exec(); err != nil {
			return fmt.Errorf("failed to restart in place: %w", err)
		}
	}
	return err
}

// serverFlagSpecs are the flags of agentapi server that viper binds.
func serverFlagSpecs() []flagSpec {
	return []flagSpec{
		{FlagType, "t", "", "The agent type (one of: " + agentNames + "); by default the agent command's name", "string"},
		{FlagPort, "p", 3284, "Port to run the server on", "int"},
		{FlagPrintOpenAPI, "P", false, "Print the OpenAPI schema to stdout and exit", "bool"},
		{FlagChatBasePath, "c", "/chat", "Base path for assets and routes used in the static files of the chat interface", "string"},
		{FlagTermWidth, "W", uint16(80), "Deprecated and without effect: the PTY transport's terminal width", "uint16"},
		{FlagTermHeight, "H", uint16(1000), "Deprecated and without effect: the PTY transport's terminal height", "uint16"},
		// localhost is the default host for the server. Port is ignored during matching.
		{FlagAllowedHosts, "a", []string{"localhost", "127.0.0.1", "[::1]"}, "HTTP allowed hosts (hostnames only, no ports). Use '*' for all, comma-separated list via flag, space-separated list via AGENTAPI_ALLOWED_HOSTS env var", "stringSlice"},
		// localhost:3284 is the default origin when you open the chat interface in your browser. localhost:3000 and 3001 are used during development.
		{FlagAllowedOrigins, "o", []string{"http://localhost:3284", "http://localhost:3000", "http://localhost:3001"}, "HTTP allowed origins. Use '*' for all, comma-separated list via flag, space-separated list via AGENTAPI_ALLOWED_ORIGINS env var", "stringSlice"},
		{FlagInitialPrompt, "I", "", "Initial prompt, sent as the first message once the agent is ready. Read from stdin if piped (e.g., echo 'prompt' | agentapi server -- claude)", "string"},
		{FlagStateFile, "s", "", "Deprecated: use --state-db. A state file written by an earlier AgentAPI, whose conversation is imported into a state database next to it (the same name with .db, or --state-db) the first time; that database keeps the conversation from then on", "string"},
		{FlagLoadState, "", false, "Deprecated: with --load-state=false the state file isn't imported", "bool"},
		{FlagSaveState, "", false, "Deprecated and without effect: the state database keeps every change", "bool"},
		{FlagPidFile, "", "", "Path to file where the server process ID will be written for shutdown scripts; {port} and {hostname} are filled in", "string"},
		{FlagExperimentalACP, "", false, "Removed: the ACP transport is no longer available", "bool"},
		{FlagStateDB, "", "", "Path to a SQLite file that keeps the conversation (messages, tool calls, requests, queue) when AgentAPI restarts, and the agent resumes its session. {port} and {hostname} are filled in", "string"},
		{FlagTransport, "", "native", "Deprecated: native, the only transport, which drives the agent through its own structured protocol. AgentAPI 3 has no pty", "string"},
		{FlagTerminal, "", true, "Offer a shell in the agent's working directory to the chat (its Terminal button) and at GET /terminal", "bool"},
		{FlagAutoContinue, "", false, "When a usage limit stops the agent's turn, continue it once the limit resets, if the agent says when", "bool"},
		{FlagPrices, "", "", "JSON file of model prices per million tokens, which add to or replace the built-in ones for the sessions' API-equivalent cost (GET /sessions, GET /cost)", "string"},
		{FlagWebhookURL, "", "", "URL the webhook events are POSTed to (by default only run.status_changed; see --webhook-events)", "string"},
		{FlagWebhookTimeout, "", 10 * time.Second, "Timeout for each webhook delivery attempt", "duration"},
		{FlagWebhookMaxAttempts, "", 3, "Maximum webhook delivery attempts", "int"},
		{FlagWebhookPayloadTemplate, "", "", "Go text/template for custom webhook POST body. Available fields: .ID, .Type, .CreatedAt, .Instance, .Data (the event's data), and for run.status_changed .ServerID, .Status, .PreviousStatus, .AgentType, .Transport", "string"},
		{FlagWebhookEvents, "", []string{}, "Webhook events to send: event types (task.completed), groups (task.*) or * for all. Default: run.status_changed. Comma-separated via flag, space-separated via AGENTAPI_WEBHOOK_EVENTS", "stringSlice"},
		{FlagWebhookSecret, "", "", "Secret that signs every webhook delivery: X-AgentAPI-Signature-256 is sha256= and the hex HMAC-SHA256 of <X-AgentAPI-Timestamp>.<body>", "string"},
		{FlagWebhookHeartbeat, "", 60 * time.Second, "How often the instance.heartbeat webhook event is sent; 0 turns it off", "duration"},
		{FlagInstanceID, "", "", "Identifies this AgentAPI to clients that watch many (GET /summary) and in every webhook event (default <hostname>:<port>)", "string"},
		{FlagName, "", "", "What clients that watch many AgentAPIs call this one, in GET /summary and webhook events (default the working directory's name)", "string"},
		{FlagWebhookInstanceID, "", "", "Deprecated: use --instance-id", "string"},
		{FlagRegistry, "", true, "While serving, keep an entry in the registry directory, where agentapi list and clients that watch many AgentAPIs find the ones running on this machine", "bool"},
		{FlagRegistryDir, "", "", "Registry directory (default ~/.agentapi/instances)", "string"},
		{FlagConfig, "", "", "Settings file: agentapi server flags by name (YAML), below flags and AGENTAPI_* variables; agentapi hub join writes it (default ~/.agentapi/config.yaml)", "string"},
		{FlagPublicURL, "", "", "URL this AgentAPI is reached at, in GET /summary and every webhook event so clients and receivers can link to its chat or call its API; {port} and {hostname} are filled in, and {ip} with this machine's IPv4 address on the route to the webhook URL, looked up again while it runs (after DHCP gives the machine another address)", "string"},
		{FlagAPIToken, "", "", "API token for Bearer authentication. If set without a value, a random token is generated. Empty (default) disables authentication.", "string"},
		{FlagBind, "", "", "Address to listen on: an IP address or hostname without a port (e.g. 127.0.0.1 or ::1). Empty (default) listens on all interfaces", "string"},
		{FlagLogLevel, "", "info", "Least important level to log: " + logctx.LevelNames, "string"},
		{FlagAllowUpdate, "", false, "Let API clients, such as a hub, update AgentAPI to the latest release (POST /update), which then restarts in place; the program's directory must be writable by the user AgentAPI runs as", "bool"},
	}
}
