package server

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/viper"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/registry"
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
	// stateFile is the deprecated --state-file: its conversation is
	// imported into the state database, which keeps it from then on.
	stateFile string
	// importState: import stateFile when the state database holds no
	// conversation yet (--load-state isn't turned off).
	importState bool
	// stateDB keeps the conversation in SQLite (--state-db, or next to
	// the --state-file).
	stateDB  string
	terminal bool
	// autoContinue continues a turn a usage limit stopped once it resets.
	autoContinue bool
	// prices is a JSON file of model prices that adds to the built-in ones.
	prices  string
	webhook httpapi.WebhookConfig
	// name is what clients that watch many AgentAPIs call this one.
	name string
	// registryDir is where the server keeps its entry while it serves;
	// empty without --registry.
	registryDir string
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
		name:           flags.GetString(FlagName),
		registryDir:    flags.GetString(FlagRegistryDir),
		webhook: httpapi.WebhookConfig{
			URL:             flags.GetString(FlagWebhookURL),
			Timeout:         flags.GetDuration(FlagWebhookTimeout),
			MaxAttempts:     flags.GetInt(FlagWebhookMaxAttempts),
			PayloadTemplate: flags.GetString(FlagWebhookPayloadTemplate),
			Events:          flags.GetStringSlice(FlagWebhookEvents),
			Secret:          flags.GetString(FlagWebhookSecret),
			Heartbeat:       heartbeatOption(flags.GetDuration(FlagWebhookHeartbeat)),
			InstanceID:      cmp.Or(flags.GetString(FlagInstanceID), flags.GetString(FlagWebhookInstanceID)),
			PublicURL:       flags.GetString(FlagPublicURL),
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
	if opts.stateFile, opts.importState, err = stateFileOptions(flags); err != nil {
		return serverOptions{}, err
	}
	if opts.stateFile != "" && opts.stateDB == "" {
		opts.stateDB = stateDBForFile(opts.stateFile)
	}
	switch {
	case !flags.GetBool(FlagRegistry):
		opts.registryDir = ""
	case opts.registryDir == "":
		if opts.registryDir, err = registry.DefaultDir(); err != nil {
			return serverOptions{}, fmt.Errorf("%w; set --registry-dir or --registry=false", err)
		}
	}
	if opts.transport, err = transportOption(flags, opts.agentType, opts.agent, opts.stateFile != ""); err != nil {
		return serverOptions{}, err
	}
	switch {
	case opts.transport == httpapi.TransportACP && opts.stateDB != "":
		return serverOptions{}, fmt.Errorf("ACP mode doesn't support state persistence")
	case opts.printOpenAPI && opts.transport == httpapi.TransportACP:
		return serverOptions{}, fmt.Errorf("flags --%s and --%s are mutually exclusive", FlagPrintOpenAPI, FlagExperimentalACP)
	}
	if opts.bind, err = httpapi.ParseBindAddress(flags.GetString(FlagBind)); err != nil {
		return serverOptions{}, fmt.Errorf("invalid --%s: %w", FlagBind, err)
	}
	return opts, nil
}

// stateFileOptions reads the deprecated --state-file, and whether to import
// it: unless --load-state is turned off. --load-state and --save-state need
// a state file; --save-state has no effect anymore, since the state
// database keeps every change.
func stateFileOptions(flags flagSource) (file string, importState bool, err error) {
	file = flags.GetString(FlagStateFile)
	if file == "" {
		if flags.IsSet(FlagLoadState) && flags.GetBool(FlagLoadState) {
			return "", false, fmt.Errorf("--load-state requires --state-file to be set")
		}
		if flags.IsSet(FlagSaveState) && flags.GetBool(FlagSaveState) {
			return "", false, fmt.Errorf("--save-state requires --state-file to be set")
		}
		return "", false, nil
	}
	return file, !flags.IsSet(FlagLoadState) || flags.GetBool(FlagLoadState), nil
}

// stateDBForFile is the state database that takes over from the state
// file at path when --state-db isn't given: the same name with .db.
func stateDBForFile(path string) string {
	db := strings.TrimSuffix(path, filepath.Ext(path)) + ".db"
	if db == path {
		db = path + ".sqlite"
	}
	return db
}

// transportOption reads --transport, and --experimental-acp, the older
// name of --transport acp. Without either, an agent the native transport can
// drive uses it, unless a state file (written by the PTY transport, whose
// command lines keep working) is given, and every other agent uses the PTY
// transport.
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

// heartbeatOption is the webhook heartbeat interval of
// --webhook-heartbeat, where 0 turns it off.
func heartbeatOption(interval time.Duration) time.Duration {
	if interval <= 0 {
		return -1
	}
	return interval
}
