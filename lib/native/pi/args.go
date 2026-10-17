package pi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// flagKind is how filterArgs handles one of Pi's built-in options.
type flagKind int

const (
	// valueFlag takes the next argument as its value. Pi reads the
	// "--flag=value" form only for extension flags, so the adapter splits
	// it.
	valueFlag flagKind = iota + 1
	// switchFlag takes no value; an argument after it is a message.
	switchFlag
	// tuiFlag only affects the terminal UI, which RPC mode doesn't draw. It
	// is dropped with its value.
	tuiFlag
	// reservedFlag picks the output mode or the session, which the adapter
	// does itself, or makes Pi do something other than serve RPC and exit.
	// It is refused.
	reservedFlag
)

type piFlag struct {
	kind flagKind
	// reason says why a reserved flag is refused.
	reason string
}

const (
	reasonMode    = "the adapter runs Pi with --mode rpc"
	reasonSession = "the adapter picks the session with --session-id"
)

// piFlags are Pi's built-in options (cli/args.js of Pi 1.0).
var piFlags = map[string]piFlag{
	"--mode":        {reservedFlag, reasonMode},
	"--print":       {reservedFlag, reasonMode},
	"-p":            {reservedFlag, reasonMode},
	"--session":     {reservedFlag, reasonSession},
	"--session-id":  {reservedFlag, reasonSession},
	"--continue":    {reservedFlag, reasonSession},
	"-c":            {reservedFlag, reasonSession},
	"--resume":      {reservedFlag, reasonSession},
	"-r":            {reservedFlag, reasonSession},
	"--fork":        {reservedFlag, reasonSession},
	"--no-session":  {reservedFlag, "the adapter needs a saved session to resume it"},
	"--export":      {reservedFlag, "it exports a session and exits"},
	"--list-models": {reservedFlag, "it lists models and exits"},
	"--help":        {reservedFlag, "it prints help and exits"},
	"-h":            {reservedFlag, "it prints help and exits"},
	"--version":     {reservedFlag, "it prints the version and exits"},
	"-v":            {reservedFlag, "it prints the version and exits"},

	"--tui-mode":  {kind: tuiFlag},
	"--use-theme": {kind: tuiFlag},

	"--provider":             {kind: valueFlag},
	"--model":                {kind: valueFlag},
	"--api-key":              {kind: valueFlag},
	"--system-prompt":        {kind: valueFlag},
	"--append-system-prompt": {kind: valueFlag},
	"--session-dir":          {kind: valueFlag},
	"--name":                 {kind: valueFlag},
	"-n":                     {kind: valueFlag},
	"--models":               {kind: valueFlag},
	"--tools":                {kind: valueFlag},
	"-t":                     {kind: valueFlag},
	"--exclude-tools":        {kind: valueFlag},
	"-xt":                    {kind: valueFlag},
	"--thinking":             {kind: valueFlag},
	"--extension":            {kind: valueFlag},
	"-e":                     {kind: valueFlag},
	"--skill":                {kind: valueFlag},
	"--prompt-template":      {kind: valueFlag},
	"--theme":                {kind: valueFlag},

	"--no-tools":            {kind: switchFlag},
	"-nt":                   {kind: switchFlag},
	"--no-builtin-tools":    {kind: switchFlag},
	"-nbt":                  {kind: switchFlag},
	"--no-extensions":       {kind: switchFlag},
	"-ne":                   {kind: switchFlag},
	"--no-skills":           {kind: switchFlag},
	"-ns":                   {kind: switchFlag},
	"--no-prompt-templates": {kind: switchFlag},
	"-np":                   {kind: switchFlag},
	"--no-themes":           {kind: switchFlag},
	"--no-context-files":    {kind: switchFlag},
	"-nc":                   {kind: switchFlag},
	"--verbose":             {kind: switchFlag},
	"--approve":             {kind: switchFlag},
	"-a":                    {kind: switchFlag},
	"--no-approve":          {kind: switchFlag},
	"-na":                   {kind: switchFlag},
	"--offline":             {kind: switchFlag},
}

// subcommands are Pi's management commands, which don't start an agent.
var subcommands = []string{"install", "remove", "uninstall", "update", "list", "config", "auth", "mcp"}

// filterArgs returns the user's arguments to pass to Pi and the TUI-only
// ones it dropped, or an error for arguments that conflict with RPC mode.
// It reads them the way Pi does, so it finds the messages, which RPC mode
// would ignore.
func filterArgs(args []string) (kept, dropped []string, err error) {
	if len(args) > 0 && slices.Contains(subcommands, args[0]) {
		return nil, nil, fmt.Errorf("pi %s is a management command, not an agent; the native transport needs the agent", args[0])
	}
	// takesNext reports whether there is an argument after args[i] that
	// isn't an option.
	takesNext := func(i int) bool { return i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") }
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return nil, nil, fmt.Errorf("pi messages after \"--\" are not supported by the native transport; use --initial-prompt or POST /message")
		case strings.HasPrefix(arg, "@"):
			return nil, nil, fmt.Errorf("pi argument %q: Pi's RPC mode doesn't accept @file arguments; mention the file in a message instead", arg)
		case !strings.HasPrefix(arg, "-"):
			return nil, nil, fmt.Errorf("pi argument %q is a message, which Pi's RPC mode ignores; use --initial-prompt or POST /message", arg)
		}
		name, value, hasValue := strings.Cut(arg, "=")
		flag, known := piFlags[name]
		switch {
		case flag.kind == reservedFlag:
			return nil, nil, fmt.Errorf("pi option %s can't be used with the native transport: %s", name, flag.reason)
		case flag.kind == tuiFlag:
			dropped = append(dropped, arg)
			if !hasValue && takesNext(i) {
				i++
				dropped = append(dropped, args[i])
			}
		case flag.kind == valueFlag && hasValue:
			kept = append(kept, name, value)
		case flag.kind == valueFlag:
			kept = append(kept, arg)
			if i+1 < len(args) {
				i++
				kept = append(kept, args[i])
			}
		case !known && strings.HasPrefix(arg, "--") && !hasValue:
			// An extension flag. Like Pi, it takes the next argument as its
			// value unless that is an option or a file.
			kept = append(kept, arg)
			if takesNext(i) && !strings.HasPrefix(args[i+1], "@") {
				i++
				kept = append(kept, args[i])
			}
		default:
			// A switch, an extension flag in the "--flag=value" form, or a
			// short option Pi doesn't know (Pi refuses those itself).
			kept = append(kept, arg)
		}
	}
	return kept, dropped, nil
}

// rpcArgs returns Pi's arguments: RPC mode, the session, then the user's
// arguments (filtered by filterArgs).
func rpcArgs(args []string, sessionID string) []string {
	return append([]string{"--mode", "rpc", "--session-id", sessionID}, args...)
}

// NewSessionID returns a new Pi session ID. AgentAPI uses the same format
// for Pi in the terminal transport.
func NewSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "agentapi-" + hex.EncodeToString(b[:])
}
