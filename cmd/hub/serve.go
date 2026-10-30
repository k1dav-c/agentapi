package hub

import (
	"context"
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

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/hub"
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
				if allowedHosts, err = httpapi.ParseAllowedHosts(allowedHosts); err != nil {
					return fmt.Errorf("--allowed-hosts: %w", err)
				}
			}
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			h, err := hub.New(hub.Config{
				Token: token, NoToken: noToken, Secret: secret, InstanceToken: instanceToken, StatePath: statePath,
				AllowedHosts: allowedHosts, Logger: logger,
			})
			if err != nil {
				return err
			}
			if noToken {
				logger.Warn("The hub has no token: everyone who reaches it sees the instances and opens them, with their API token")
				if len(allowedHosts) == 0 && !loopback(bind) {
					logger.Warn("The hub answers to any name: a site's page can reach it through a name of the site's that points at the hub's address (DNS rebinding); set --allowed-hosts to the names you open the hub by")
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
			go func() { serveErr <- srv.ListenAndServe() }()
			logger.Info("Hub listening", "address", srv.Addr, "webhook", "/hooks", "state", statePath)
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
	cmd.Flags().StringSliceVar(&allowedHosts, "allowed-hosts", nil, "Names browsers open the hub by, besides IP addresses and localhost, which always work (hostnames only, comma-separated; '*' for any); without it the hub answers to any name")
	return cmd
}

// loopback reports whether bind is a loopback address, which only this
// machine reaches.
func loopback(bind string) bool {
	if bind == "localhost" {
		return true
	}
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
