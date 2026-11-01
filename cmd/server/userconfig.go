package server

import (
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/coder/agentapi/lib/userconfig"
)

// readUserConfig adds the settings of the user's config file (--config,
// by default ~/.agentapi/config.yaml) to v, below the flags and the
// AGENTAPI_* variables. flags are the server's: a setting that names none
// is reported. A missing file is fine; one AgentAPI can't read is reported
// and left out, so a broken file doesn't keep the agent from starting.
func readUserConfig(v *viper.Viper, flags *pflag.FlagSet, logger *slog.Logger) {
	path := v.GetString(FlagConfig)
	if path == "" {
		var err error
		if path, err = userconfig.DefaultPath(); err != nil {
			return
		}
	}
	config, err := userconfig.Load(path)
	if err != nil {
		logger.Warn("Ignoring the AgentAPI config file", "path", path, "error", err)
		return
	}
	if len(config) == 0 {
		return
	}
	var unknown []string
	for key := range config {
		if flags.Lookup(key) == nil || key == FlagConfig {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		logger.Warn("The AgentAPI config file names settings agentapi server doesn't have", "path", path, "settings", unknown)
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 && config["webhook-secret"] != nil {
		logger.Warn("The AgentAPI config file holds a webhook secret others can read; chmod 600 it", "path", path)
	}
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		logger.Warn("Ignoring the AgentAPI config file", "path", path, "error", err)
		return
	}
	if shared := sharedStateDB(config, flags); shared != "" {
		logger.Warn("Ignoring state-db in the AgentAPI config file: every agentapi server reads the file, and only one can use a state database; put {port} in its path, e.g. ~/.agentapi/state/{port}.db",
			"path", path, "state-db", shared)
		v.Set(FlagStateDB, "")
	}
	logger.Info("Read the AgentAPI config file", "path", path)
}

// sharedStateDB returns the state-db of config if every server would use
// that file: its path has no {port}, and neither --state-db nor
// AGENTAPI_STATE_DB names another. The first server locks the file, so the
// others couldn't start.
func sharedStateDB(config userconfig.Config, flags *pflag.FlagSet) string {
	path, ok := config[FlagStateDB].(string)
	if !ok || path == "" || strings.Contains(path, "{port}") {
		return ""
	}
	if flag := flags.Lookup(FlagStateDB); flag != nil && flag.Changed {
		return ""
	}
	if os.Getenv("AGENTAPI_STATE_DB") != "" {
		return ""
	}
	return path
}
