package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"

	"github.com/spf13/viper"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/statedb"
)

// FlagValidate is the hidden --validate: check the settings and exit,
// which a restart in place has the new program do first (selfExec.check).
const FlagValidate = "validate"

// validateOK starts what --validate prints when the server would start,
// followed by the newest state database schema it knows.
const validateOK = "ok state-db-schema="

// validateServer checks what runServer would refuse for the agent command
// args, as far as it can without starting anything: the settings, the
// prices file, the agent's arguments (native) or program (PTY), the state
// database's schema and the server's configuration. It doesn't listen,
// start the agent, or write a file: the PID file, the registry entry and
// the state database stay as they are. A state database another AgentAPI
// has open (the one that asks, before a restart in place) isn't read; that
// one checks it (statedb.DB.CheckWriter). On success it writes validateOK
// and the schema to out.
func validateServer(out io.Writer, args []string) error {
	opts, err := loadServerOptions(viper.GetViper(), args)
	if err != nil {
		return err
	}
	if opts.prices != "" {
		if _, err := pricing.Builtin().WithFile(opts.prices); err != nil {
			return fmt.Errorf("--prices: %w", err)
		}
	}
	cwd, _ := os.Getwd()
	if err := checkAgent(opts, cwd); err != nil {
		return err
	}
	if opts.stateDB != "" {
		if err := statedb.Check(opts.stateDB); err != nil && !errors.Is(err, statedb.ErrInUse) {
			return err
		}
	}
	if err := httpapi.CheckConfig(serverConfig(opts, &runningAgent{}, nil, "", cwd, nil, nil)); err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}
	_, err = fmt.Fprintf(out, "%s%d\n", validateOK, statedb.Schema())
	return err
}

// checkAgent returns what would keep startAgent from starting the agent:
// arguments the native adapter refuses, or a PTY agent's program that
// isn't found.
func checkAgent(opts serverOptions, cwd string) error {
	if opts.transport == httpapi.TransportNative {
		_, err := nativeFactory(opts.agentType, nativeAdapterConfig{
			Program: opts.agent,
			Args:    opts.agentArgs,
			CWD:     cwd,
			Logger:  slog.New(slog.DiscardHandler),
		})
		return err
	}
	if _, err := exec.LookPath(opts.agent); err != nil {
		return fmt.Errorf("failed to setup process: %w", err)
	}
	return nil
}
