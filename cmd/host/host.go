// Package host implements agentapi host: the launcher that starts an
// AgentAPI for each GitHub issue a hub dispatches to this machine (see
// lib/host), the systemd user service that runs it, and the git credential
// helper and gh token its jobs use.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/httpmw"
	"github.com/coder/agentapi/lib/logctx"
)

// HostCmd is agentapi host.
func HostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Run the launcher that starts an AgentAPI for each issue a hub dispatches to this machine",
		Long: "The launcher starts an AgentAPI for each GitHub issue a hub dispatches to this machine, in a git " +
			"worktree on the issue's branch, and stops it again. It reads the host section of " +
			"~/.agentapi/config.yaml. agentapi host install runs it as a systemd user service.",
	}
	cmd.AddCommand(serveCmd(), installCmd(), uninstallCmd(), statusCmd(), gitCredentialCmd(), ghTokenCmd())
	return cmd
}

func serveCmd() *cobra.Command {
	var configPath, logLevel string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the launcher",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			level, err := logctx.ParseLevel(logLevel)
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level}))
			config, err := host.LoadConfig(configPath)
			if err != nil {
				return err
			}
			launcher, err := host.New(config, logger)
			if err != nil {
				return err
			}
			bind, err := httpmw.ParseBindAddress(config.Bind)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(config.Port)))
			if err != nil {
				return err
			}
			srv := &http.Server{Handler: launcher.Handler(), ReadHeaderTimeout: 10 * time.Second}
			errs := make(chan error, 2)
			go func() { errs <- srv.Serve(ln) }()
			go func() { errs <- launcher.ServeSocket(ctx) }()
			go launcher.Run(ctx)
			if len(config.Agents) == 0 {
				logger.Warn("No agent is installed: install claude, codex or pi, or list them in agents")
			}
			logger.Info("Serving the launcher", "address", ln.Addr().String(), "url", launcher.PublicURL(ctx),
				"token_file", config.TokenFile, "agents", config.Agents, "workdir", config.Workdir)
			select {
			case <-ctx.Done():
			case err := <-errs:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
			}
			logger.Info("Stopping the launcher; the jobs' AgentAPIs keep running")
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdown)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "Config file (default ~/.agentapi/config.yaml)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Least important level to log: debug, info, warn, error")
	return cmd
}

func statusCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the launcher, the repositories it has and its jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := host.LoadConfig(configPath)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			token, err := os.ReadFile(config.TokenFile)
			if err != nil {
				return fmt.Errorf("the launcher hasn't run yet (no %s)", config.TokenFile)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(config.Port)+"/host", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("the launcher doesn't answer on port %d: %w", config.Port, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				return fmt.Errorf("the launcher answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
			}
			var info host.HostInfo
			if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
				return err
			}
			hub := config.HubURL
			if hub == "" {
				hub = "none (run agentapi hub join)"
			}
			_, _ = fmt.Fprintf(out, "Launcher: %s (agentapi %s)\nHub: %s\nAgents: %s (default %s)\nGitHub credentials: %v\nRepositories (%d):\n",
				info.URL, info.AgentAPIVersion, hub, strings.Join(info.Agents, ", "), info.DefaultAgent, info.CredentialsAvailable, len(info.Repos))
			for _, repo := range info.Repos {
				_, _ = fmt.Fprintf(out, "  %s\n", repo)
			}
			_, _ = fmt.Fprintf(out, "Jobs (%d of %d):\n", len(info.Jobs), info.MaxJobs)
			for _, job := range info.Jobs {
				line := fmt.Sprintf("  %s  %s  %s  port %d  %s", job.Key, job.Agent, job.State, job.Port, job.Worktree)
				if job.Error != "" {
					line += "  (" + job.Error + ")"
				}
				_, _ = fmt.Fprintln(out, line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "Config file (default ~/.agentapi/config.yaml)")
	return cmd
}

// jobFromEnv is the job of the process git or gh runs in; outside a job
// there is none.
func jobFromEnv() (host.Key, bool) {
	slug := os.Getenv(host.EnvJob)
	if slug == "" {
		return host.Key{}, false
	}
	key, err := host.ParseKey(slug)
	return key, err == nil
}

func socketFlag(cmd *cobra.Command, socket *string) {
	cmd.Flags().StringVar(socket, "socket", "", "The launcher's socket (default ~/.agentapi/host.sock)")
}

func resolveSocket(socket string) (string, error) {
	if socket != "" {
		return socket, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentapi", "host.sock"), nil
}

func gitCredentialCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:    "git-credential <get|store|erase>",
		Short:  "git's credential helper in a job: the job's GitHub token",
		Hidden: true,
		// git and the gh shim show the error; usage would bury it.
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, ok := jobFromEnv()
			if !ok {
				return nil
			}
			path, err := resolveSocket(socket)
			if err != nil {
				return err
			}
			return host.GitCredential(cmd.Context(), args[0], cmd.InOrStdin(), cmd.OutOrStdout(), path, key)
		},
	}
	socketFlag(cmd, &socket)
	return cmd
}

func ghTokenCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:    "gh-token",
		Short:  "Print the GitHub token of the job this runs in, for gh",
		Hidden: true,
		// git and the gh shim show the error; usage would bury it.
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, ok := jobFromEnv()
			if !ok {
				return fmt.Errorf("not in a job: %s isn't set", host.EnvJob)
			}
			path, err := resolveSocket(socket)
			if err != nil {
				return err
			}
			token, err := host.RequestToken(cmd.Context(), path, key)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token.Value)
			return err
		},
	}
	socketFlag(cmd, &socket)
	return cmd
}

// run runs a command, returning its output and an error naming it. Tests
// replace it, so they don't reach the machine's systemd.
var run = func(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
