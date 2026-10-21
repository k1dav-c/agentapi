package server

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/viper"

	"github.com/coder/agentapi/lib/httpapi"
	st "github.com/coder/agentapi/lib/screentracker"
)

// serverOptions are the settings of `agentapi server`, from its flags and
// AGENTAPI_* variables.
type serverOptions struct {
	// agent is the agent's program, and agentArgs the arguments after it.
	agent     string
	agentArgs []string
	agentType AgentType
	transport httpapi.Transport
	// printOpenAPI prints the API's schema instead of running an agent.
	printOpenAPI bool

	port           int
	bind           string
	chatBasePath   string
	allowedHosts   []string
	allowedOrigins []string
	// apiToken is the token the API asks for: empty for none, "generate"
	// for a random one.
	apiToken string
	pidFile  string

	termWidth     uint16
	termHeight    uint16
	initialPrompt string
	// state keeps a PTY conversation in a file (--state-file).
	state st.StatePersistenceConfig
	// stateDB keeps a native conversation in SQLite (--state-db).
	stateDB  string
	terminal bool
	// autoContinue continues a turn a usage limit stopped once it resets.
	autoContinue bool
	// prices is a JSON file of model prices that adds to the built-in ones.
	prices  string
	webhook httpapi.WebhookConfig
}

// flagSource is where the options are read from: the viper the flags are
// bound to.
type flagSource interface {
	GetString(key string) string
	GetBool(key string) bool
	GetInt(key string) int
	GetUint16(key string) uint16
	GetStringSlice(key string) []string
	GetDuration(key string) time.Duration
	IsSet(key string) bool
}

var _ flagSource = (*viper.Viper)(nil)

// loadServerOptions reads the options of the agent command args (the
// program and its arguments) from flags, and checks them.
func loadServerOptions(flags flagSource, args []string) (serverOptions, error) {
	opts := serverOptions{
		agent:          args[0],
		agentArgs:      args[1:],
		printOpenAPI:   flags.GetBool(FlagPrintOpenAPI),
		port:           flags.GetInt(FlagPort),
		chatBasePath:   flags.GetString(FlagChatBasePath),
		allowedHosts:   flags.GetStringSlice(FlagAllowedHosts),
		allowedOrigins: flags.GetStringSlice(FlagAllowedOrigins),
		apiToken:       flags.GetString(FlagAPIToken),
		pidFile:        flags.GetString(FlagPidFile),
		termWidth:      flags.GetUint16(FlagTermWidth),
		termHeight:     flags.GetUint16(FlagTermHeight),
		initialPrompt:  flags.GetString(FlagInitialPrompt),
		stateDB:        flags.GetString(FlagStateDB),
		terminal:       flags.GetBool(FlagTerminal),
		autoContinue:   flags.GetBool(FlagAutoContinue),
		prices:         flags.GetString(FlagPrices),
		webhook: httpapi.WebhookConfig{
			URL:             flags.GetString(FlagWebhookURL),
			Timeout:         flags.GetDuration(FlagWebhookTimeout),
			MaxAttempts:     flags.GetInt(FlagWebhookMaxAttempts),
			PayloadTemplate: flags.GetString(FlagWebhookPayloadTemplate),
		},
	}
	var err error
	opts.agentType, err = parseAgentType(opts.agent, flags.GetString(FlagType))
	if err != nil {
		return serverOptions{}, fmt.Errorf("failed to parse agent type: %w", err)
	}
	if opts.termWidth < 10 {
		return serverOptions{}, fmt.Errorf("term width must be at least 10")
	}
	if opts.termHeight < 10 {
		return serverOptions{}, fmt.Errorf("term height must be at least 10")
	}
	if opts.state, err = stateFileOptions(flags); err != nil {
		return serverOptions{}, err
	}
	persists := opts.state.LoadState || opts.state.SaveState
	if opts.transport, err = transportOption(flags, opts.agentType, opts.agent, persists); err != nil {
		return serverOptions{}, err
	}
	switch {
	case opts.transport == httpapi.TransportACP && persists:
		return serverOptions{}, fmt.Errorf("ACP mode doesn't support state persistence")
	case opts.transport == httpapi.TransportNative && persists:
		return serverOptions{}, fmt.Errorf("the native transport doesn't support --%s; use --%s", FlagStateFile, FlagStateDB)
	case opts.stateDB != "" && opts.transport != httpapi.TransportNative:
		return serverOptions{}, fmt.Errorf("--%s needs --%s native (with the PTY transport the agent can't resume its session; use --%s)", FlagStateDB, FlagTransport, FlagStateFile)
	case opts.printOpenAPI && opts.transport == httpapi.TransportACP:
		return serverOptions{}, fmt.Errorf("flags --%s and --%s are mutually exclusive", FlagPrintOpenAPI, FlagExperimentalACP)
	}
	if opts.bind, err = httpapi.ParseBindAddress(flags.GetString(FlagBind)); err != nil {
		return serverOptions{}, fmt.Errorf("invalid --%s: %w", FlagBind, err)
	}
	return opts, nil
}

// stateFileOptions reads --state-file, with --load-state and --save-state
// on unless they are turned off; they need a state file.
func stateFileOptions(flags flagSource) (st.StatePersistenceConfig, error) {
	file := flags.GetString(FlagStateFile)
	if file == "" {
		if flags.IsSet(FlagLoadState) && flags.GetBool(FlagLoadState) {
			return st.StatePersistenceConfig{}, fmt.Errorf("--load-state requires --state-file to be set")
		}
		if flags.IsSet(FlagSaveState) && flags.GetBool(FlagSaveState) {
			return st.StatePersistenceConfig{}, fmt.Errorf("--save-state requires --state-file to be set")
		}
		return st.StatePersistenceConfig{}, nil
	}
	return st.StatePersistenceConfig{
		StateFile: file,
		LoadState: !flags.IsSet(FlagLoadState) || flags.GetBool(FlagLoadState),
		SaveState: !flags.IsSet(FlagSaveState) || flags.GetBool(FlagSaveState),
	}, nil
}

// transportOption reads --transport, and --experimental-acp, the older
// name of --transport acp. Without either, an agent the native transport can
// drive uses it, unless a state file asks for the PTY transport's
// persistence, and every other agent uses the PTY transport.
func transportOption(flags flagSource, agentType AgentType, program string, stateFile bool) (httpapi.Transport, error) {
	acp := flags.GetBool(FlagExperimentalACP)
	if !flags.IsSet(FlagTransport) {
		switch {
		case acp:
			return httpapi.TransportACP, nil
		case hasNativeAdapter(agentType, program) && !stateFile:
			return httpapi.TransportNative, nil
		default:
			return httpapi.TransportPTY, nil
		}
	}
	switch value := flags.GetString(FlagTransport); value {
	case "", "pty":
		if acp {
			return httpapi.TransportACP, nil
		}
		return httpapi.TransportPTY, nil
	case "acp":
		return httpapi.TransportACP, nil
	case "native":
		if acp {
			return "", fmt.Errorf("--%s and --%s=native are mutually exclusive", FlagExperimentalACP, FlagTransport)
		}
		return httpapi.TransportNative, nil
	default:
		return "", fmt.Errorf("unknown --%s %q (one of: pty, acp, native)", FlagTransport, value)
	}
}

// readPipedPrompt returns what was piped to AgentAPI's stdin, the initial
// prompt when --initial-prompt isn't given.
func readPipedPrompt(logger *slog.Logger) (string, error) {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return "", nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read stdin: %w", err)
	}
	if len(data) > 0 {
		logger.Info("Read initial prompt from stdin", "bytes", len(data))
	}
	return string(data), nil
}
