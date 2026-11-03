package server

import (
	"cmp"
	"errors"
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
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/registry"
)

// serverOptions are the settings of `agentapi server`, from its flags and
// AGENTAPI_* variables.
type serverOptions struct {
	// agent is the agent's program, and agentArgs the arguments after it.
	agent     string
	agentArgs []string
	agentType AgentType
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
	// allowUpdate lets API clients update AgentAPI (POST /update).
	allowUpdate bool
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
	port := flags.GetInt(FlagPort)
	opts := serverOptions{
		agent:          args[0],
		agentArgs:      args[1:],
		printOpenAPI:   flags.GetBool(FlagPrintOpenAPI),
		port:           port,
		chatBasePath:   flags.GetString(FlagChatBasePath),
		allowedHosts:   flags.GetStringSlice(FlagAllowedHosts),
		allowedOrigins: flags.GetStringSlice(FlagAllowedOrigins),
		apiToken:       flags.GetString(FlagAPIToken),
		pidFile:        expandHome(instanceapi.ExpandPlaceholders(flags.GetString(FlagPidFile), port)),
		initialPrompt:  flags.GetString(FlagInitialPrompt),
		// One setting in the config file serves every server of the user,
		// each with its own file (also pidFile).
		stateDB:      expandHome(instanceapi.ExpandPlaceholders(flags.GetString(FlagStateDB), port)),
		terminal:     flags.GetBool(FlagTerminal),
		autoContinue: flags.GetBool(FlagAutoContinue),
		prices:       expandHome(flags.GetString(FlagPrices)),
		name:         flags.GetString(FlagName),
		registryDir:  expandHome(flags.GetString(FlagRegistryDir)),
		allowUpdate:  flags.GetBool(FlagAllowUpdate),
		webhook: httpapi.WebhookConfig{
			URL:             flags.GetString(FlagWebhookURL),
			Timeout:         flags.GetDuration(FlagWebhookTimeout),
			MaxAttempts:     flags.GetInt(FlagWebhookMaxAttempts),
			PayloadTemplate: flags.GetString(FlagWebhookPayloadTemplate),
			Events:          flags.GetStringSlice(FlagWebhookEvents),
			Secret:          flags.GetString(FlagWebhookSecret),
			Heartbeat:       heartbeatOption(flags.GetDuration(FlagWebhookHeartbeat)),
			InstanceID:      cmp.Or(flags.GetString(FlagInstanceID), flags.GetString(FlagWebhookInstanceID)),
			// {ip} stays: it may change while the server runs, and every
			// event and summary fill it in.
			PublicURL: httpapi.ExpandPublicURL(flags.GetString(FlagPublicURL), port),
		},
	}
	if port < 0 || port > 65535 {
		return serverOptions{}, fmt.Errorf("invalid --%s %d: a port is 0 to 65535", FlagPort, port)
	}
	var err error
	// The schema is the same for every agent; make gen names none.
	if !opts.printOpenAPI {
		if opts.agentType, err = parseAgentType(opts.agent, flags.GetString(FlagType)); err != nil {
			return serverOptions{}, err
		}
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
	if err := checkTransport(flags); err != nil {
		return serverOptions{}, err
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
	file = expandHome(flags.GetString(FlagStateFile))
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

// expandHome replaces a leading ~ of path, a path setting, with the home
// directory. A shell does that for a flag, but not for the config file, a
// variable or --flag=~/….
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
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

// errACPRemoved answers --transport acp and --experimental-acp.
var errACPRemoved = errors.New("the experimental ACP transport was removed: AgentAPI runs Claude Code, Codex and Pi natively")

// errPTYRemoved answers --transport pty.
var errPTYRemoved = fmt.Errorf("AgentAPI 3 has no PTY transport: it runs Claude Code, Codex and Pi natively; drop --transport pty (or AGENTAPI_TRANSPORT=pty), or use AgentAPI %s, the last release with the PTY transport", lastPTYRelease)

// checkTransport checks the deprecated --transport, which only takes
// native, the one transport.
func checkTransport(flags flagSource) error {
	if flags.GetBool(FlagExperimentalACP) {
		return errACPRemoved
	}
	switch value := flags.GetString(FlagTransport); value {
	case "", string(httpapi.TransportNative):
		return nil
	case "pty":
		return errPTYRemoved
	case "acp":
		return errACPRemoved
	default:
		return fmt.Errorf("unknown --%s %q: AgentAPI 3 drives every agent natively", FlagTransport, value)
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
