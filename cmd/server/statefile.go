package server

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/coder/agentapi/lib/statedb"
)

// importStateFile imports the conversation of the deprecated --state-file
// into the state database, unless it holds one already (it was imported
// before), and says to use --state-db from now on.
func importStateFile(db *statedb.DB, opts serverOptions, cwd string, logger *slog.Logger) {
	notice := fmt.Sprintf("--%s is deprecated: the conversation is kept in the state database %s; start with --%s %s instead", FlagStateFile, opts.stateDB, FlagStateDB, opts.stateDB)
	if !opts.importState {
		logger.Warn(notice)
		return
	}
	n, err := db.ImportStateFile(opts.stateFile, map[string]string{
		statedb.MetaAgentType: string(opts.agentType),
		statedb.MetaTransport: string(opts.transport),
		statedb.MetaCWD:       cwd,
	})
	switch {
	case errors.Is(err, statedb.ErrNotEmpty):
		logger.Warn(notice)
	case err != nil:
		// The conversation starts empty, as it did with a state file that
		// couldn't be read.
		logger.Error("Failed to import the state file", "path", opts.stateFile, "error", err)
		logger.Warn(notice)
	default:
		logger.Warn(notice, "imported_messages", n, "state_file", opts.stateFile)
	}
}
