package host

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/userconfig"
)

// serviceName is the launcher's systemd user service.
const serviceName = "agentapi-host.service"

// unitFile is the service: KillMode=process stops the launcher alone, so
// restarting it (an update) leaves the jobs' AgentAPIs running; the new
// launcher takes them over.
func unitFile(program, configPath string) string {
	exec := quoteSystemd(program) + " host serve"
	if configPath != "" {
		exec += " --config " + quoteSystemd(configPath)
	}
	return `# Written by agentapi host install.
[Unit]
Description=AgentAPI host launcher: an AgentAPI for each issue a hub dispatches here
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=` + exec + `
Restart=on-failure
RestartSec=5
KillMode=process

[Install]
WantedBy=default.target
`
}

// quoteSystemd quotes an argument of ExecStart.
func quoteSystemd(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\$%") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

func unitPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", serviceName), nil
}

func installCmd() *cobra.Command {
	var (
		configPath string
		now        bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Run the launcher as a systemd user service, from now on at every boot",
		Long: "Writes ~/.config/systemd/user/" + serviceName + " and enables it; --now also (re)starts it. " +
			"Running it again rewrites the service, for instance after the program moved. The Coder templates " +
			"run it at every workspace start.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !host.Supported {
				return errors.New("agentapi host runs on Linux and macOS")
			}
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if _, err := exec.LookPath("systemctl"); err != nil {
				return errors.New("systemctl isn't installed: run agentapi host serve under another supervisor")
			}
			if _, err := run(ctx, "systemctl", "--user", "show-environment"); err != nil {
				return fmt.Errorf("no systemd user instance (%w): run agentapi host serve under another supervisor", err)
			}
			config, err := host.LoadConfig(configPath)
			if err != nil {
				return err
			}
			warnHub(out, configPath)
			if len(config.Agents) == 0 {
				_, _ = fmt.Fprintln(out, "Warning: no agent is installed (claude, codex or pi): jobs can't start until one is")
			}
			program, err := host.Executable()
			if err != nil {
				return err
			}
			path, err := unitPath()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(unitFile(program, configPath)), 0o644); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Wrote %s (%s host serve)\n", path, program)
			if _, err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
				return err
			}
			if _, err := run(ctx, "systemctl", "--user", "enable", serviceName); err != nil {
				return err
			}
			if now {
				if _, err := run(ctx, "systemctl", "--user", "restart", serviceName); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, "Started the launcher: agentapi host status shows it")
			} else {
				_, _ = fmt.Fprintf(out, "Enabled the launcher: it starts at the next boot, or now with systemctl --user start %s\n", serviceName)
			}
			warnLinger(cmd, out)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "Config file the launcher reads (default ~/.agentapi/config.yaml)")
	cmd.Flags().BoolVar(&now, "now", false, "Start the launcher now too (restart it if it runs)")
	return cmd
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the launcher's systemd user service; the jobs and their files stay",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := unitPath()
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The launcher's service isn't installed")
				return nil
			}
			_, _ = run(cmd.Context(), "systemctl", "--user", "disable", "--now", serviceName)
			if err := os.Remove(path); err != nil {
				return err
			}
			_, _ = run(cmd.Context(), "systemctl", "--user", "daemon-reload")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %s; the jobs' AgentAPIs keep running until they're stopped\n", path)
			return nil
		},
	}
}

// warnHub says when the config file names no hub: the launcher then
// reports to none.
func warnHub(out io.Writer, configPath string) {
	path := configPath
	if path == "" {
		var err error
		if path, err = userconfig.DefaultPath(); err != nil {
			return
		}
	}
	config, err := userconfig.Load(path)
	if err != nil {
		return
	}
	if url, _ := config["webhook-url"].(string); url == "" {
		_, _ = fmt.Fprintln(out, "Warning: this machine reports to no hub: run agentapi hub join first")
	}
}

// warnLinger says when the user's services stop at logout.
func warnLinger(cmd *cobra.Command, out io.Writer) {
	if _, err := exec.LookPath("loginctl"); err != nil {
		return
	}
	u, err := user.Current()
	if err != nil {
		return
	}
	value, err := run(cmd.Context(), "loginctl", "show-user", u.Username, "--property=Linger", "--value")
	if err == nil && strings.TrimSpace(value) == "no" {
		_, _ = fmt.Fprintf(out, "Warning: your services stop when you log out; keep them running with: sudo loginctl enable-linger %s\n", u.Username)
	}
}
