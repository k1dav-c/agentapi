package server

import (
	"fmt"
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
		if flags.Lookup(key) == nil || key == FlagConfig || key == FlagValidate {
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
	for _, setting := range perServerFiles {
		if shared := sharedFile(config, flags, setting.name); shared != "" {
			logger.Warn(fmt.Sprintf("Ignoring %s in the AgentAPI config file: every agentapi server reads the file, and only one can %s; put {port} in its path, e.g. %s",
				setting.name, setting.use, setting.example), "path", path, setting.name, shared)
			v.Set(setting.name, "")
		}
	}
	logger.Info("Read the AgentAPI config file", "path", path)
}

// perServerFiles are the settings that name a file only one server can
// use: the first locks a state database, and the others refuse a PID file
// a running server wrote.
var perServerFiles = []struct{ name, use, example string }{
	{FlagStateDB, "use a state database", "~/.agentapi/state/{port}.db"},
	{FlagPidFile, "write a PID file", "~/.agentapi/run/{port}.pid"},
}

// sharedFile returns the file setting name of config if every server would
// use that file: its path has no {port}, and neither the flag nor its
// AGENTAPI_* variable names another.
func sharedFile(config userconfig.Config, flags *pflag.FlagSet, name string) string {
	path, ok := config[name].(string)
	if !ok || path == "" || strings.Contains(path, "{port}") {
		return ""
	}
	if flag := flags.Lookup(name); flag != nil && flag.Changed {
		return ""
	}
	if os.Getenv("AGENTAPI_"+strings.ToUpper(strings.ReplaceAll(name, "-", "_"))) != "" {
		return ""
	}
	return path
}
