package hub

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/httpmw"
	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/logctx"
)

// defaultHubPort is where agentapi hub serve listens by default.
const defaultHubPort = 3290

// stopTimeout is how long the hub waits for its watchers when it stops.
const stopTimeout = 10 * time.Second

func serveCmd() *cobra.Command {
	var (
		port          int
		bind          string
		token         string
		noToken       bool
		secret        string
		instanceToken string
		statePath     string
		allowedHosts  []string
		logLevel      string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a hub: watch many AgentAPIs and open any of them in the chat",
		Long: "Run a hub. AgentAPIs join it with agentapi hub join <this hub>/hooks: their signed instance.* events " +
			"add them, and the hub follows each one's summary. Sign in at the hub's address with --token to see them " +
			"all and open any of them in the chat; the hub sends the instances' API token (--instance-token) for you. " +
			"With --no-token everyone who reaches the hub is in: only for a hub behind a sign-in of its own, such as a " +
			"Coder workspace app or a private VPN.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			token = firstSet(token, os.Getenv("AGENTAPI_HUB_TOKEN"))
			secret = firstSet(secret, os.Getenv("AGENTAPI_HUB_SECRET"))
			instanceToken = firstSet(instanceToken, os.Getenv("AGENTAPI_HUB_INSTANCE_TOKEN"), os.Getenv("AGENTAPI_API_TOKEN"))
			if statePath == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("find the home directory: %w (set --state)", err)
				}
				statePath = filepath.Join(home, ".agentapi", "hub", "instances.json")
			}
			if len(allowedHosts) > 0 {
				var err error
				if allowedHosts, err = httpmw.ParseAllowedHosts(allowedHosts); err != nil {
					return fmt.Errorf("--allowed-hosts: %w", err)
				}
			}
			level, err := logctx.ParseLevel(firstSet(logLevel, os.Getenv("AGENTAPI_HUB_LOG_LEVEL")))
			if err != nil {
				return fmt.Errorf("invalid --log-level: %w", err)
			}
			logger := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{Level: level}))
			// Listening first, the join URLs name the port it got (--port 0).
			listener, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
			if err != nil {
				return err
			}
			port = listener.Addr().(*net.TCPAddr).Port
			h, err := hub.New(hub.Config{
				Token: token, NoToken: noToken, Secret: secret, InstanceToken: instanceToken, Bind: bind, Port: port,
				StatePath: statePath, AllowedHosts: allowedHosts, Logger: logger,
			})
			if err != nil {
				_ = listener.Close()
				return err
			}
			if noToken {
				logger.Warn("The hub has no token: everyone who reaches it sees the instances and opens them, with their API token")
				if len(allowedHosts) == 0 && !loopbackHost(bind) {
					logger.Warn("The hub answers to any name: a site's page can reach it through a name of the site's that points at the hub's address (DNS rebinding); set --allowed-hosts to the names you open the hub by")
				}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			srv := &http.Server{
				Addr:              net.JoinHostPort(bind, strconv.Itoa(port)),
				Handler:           h.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
			}
			done := make(chan struct{})
			go func() {
				h.Run(ctx)
				close(done)
			}()
			serveErr := make(chan error, 1)
			go func() { serveErr <- srv.Serve(listener) }()
			logger.Info("Hub listening", "address", srv.Addr, "webhook", "/hooks", "state", statePath)
			logJoin(logger, h.JoinURLs(), port)
			// The instances' last changes, on any way out.
			defer func() {
				if err := h.Close(); err != nil {
					logger.Error("Failed to keep the hub's instances", "file", statePath, "error", err)
				}
			}()
			select {
			case err := <-serveErr:
				stop()
				<-done
				return err
			case <-ctx.Done():
			}
			// Close, not Shutdown: the browsers' event streams don't end by
			// themselves.
			_ = srv.Close()
			select {
			case <-done:
			case <-time.After(stopTimeout):
				// The instances' watchers should end at once; one that
				// doesn't must not keep the hub from stopping (a restart,
				// an update). Its stack says where it waits.
				logger.Error("The hub's watchers didn't stop; stopping anyway", "after", stopTimeout)
				_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
			}
			if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", defaultHubPort, "Port to listen on")
	cmd.Flags().StringVar(&bind, "bind", "", "Address to listen on, such as this machine's VPN address (default all interfaces)")
	cmd.Flags().StringVar(&token, "token", "", "Token browsers sign in with (default $AGENTAPI_HUB_TOKEN); required, unless --no-token")
	cmd.Flags().BoolVar(&noToken, "no-token", false, "Let in everyone who reaches the hub, without signing in: only behind a sign-in of its own (a Coder workspace app, a private VPN)")
	cmd.Flags().StringVar(&secret, "secret", "", "Secret the AgentAPIs sign their events with, as given to agentapi hub join (default $AGENTAPI_HUB_SECRET); required")
	cmd.Flags().StringVar(&instanceToken, "instance-token", "", "API token sent to the AgentAPIs (default $AGENTAPI_HUB_INSTANCE_TOKEN, else $AGENTAPI_API_TOKEN)")
	cmd.Flags().StringVar(&statePath, "state", "", "File that keeps the instances (default ~/.agentapi/hub/instances.json)")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "Least important level to log: "+logctx.LevelNames+" (default $AGENTAPI_HUB_LOG_LEVEL, else info)")
	cmd.Flags().StringSliceVar(&allowedHosts, "allowed-hosts", nil, "Names browsers open the hub by, besides IP addresses and localhost, which always work (hostnames only, comma-separated; '*' for any); without it the hub answers to any name")
	return cmd
}

// logJoin logs the command that joins a machine to the hub, for each URL
// machines may reach it by. It leaves the secret out, since others may read
// the log: join takes it from AGENTAPI_HUB_SECRET.
func logJoin(logger *slog.Logger, urls []hub.JoinURL, port int) {
	if len(urls) == 0 {
		urls = []hub.JoinURL{{URL: fmt.Sprintf("http://<this machine>:%d/hooks", port)}}
	}
	for _, u := range urls {
		var args []any
		if u.Interface != "" {
			args = append(args, "interface", u.Interface)
		}
		args = append(args, "command", "agentapi hub join "+u.URL+" --public-url 'http://{ip}:{port}'")
		logger.Info("To join a machine, run this on it with AGENTAPI_HUB_SECRET set to the hub's secret", args...)
	}
	logger.Info("The Add dialog (+) of the hub's page has the command, and a prompt for an agent on the machine")
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
