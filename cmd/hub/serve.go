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
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/hub"
)

// defaultHubPort is where agentapi hub serve listens by default.
const defaultHubPort = 3290

func serveCmd() *cobra.Command {
	var (
		port          int
		bind          string
		token         string
		secret        string
		instanceToken string
		statePath     string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a hub: watch many AgentAPIs and open any of them in the chat",
		Long: "Run a hub. AgentAPIs join it with agentapi hub join <this hub>/hooks: their signed instance.* events " +
			"add them, and the hub follows each one's summary. Sign in at the hub's address with --token to see them " +
			"all and open any of them in the chat; the hub sends the instances' API token (--instance-token) for you.",
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
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			h, err := hub.New(hub.Config{
				Token: token, Secret: secret, InstanceToken: instanceToken, StatePath: statePath, Logger: logger,
			})
			if err != nil {
				return err
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
			<-done
			if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", defaultHubPort, "Port to listen on")
	cmd.Flags().StringVar(&bind, "bind", "", "Address to listen on, such as this machine's VPN address (default all interfaces)")
	cmd.Flags().StringVar(&token, "token", "", "Token browsers sign in with (default $AGENTAPI_HUB_TOKEN); required")
	cmd.Flags().StringVar(&secret, "secret", "", "Secret the AgentAPIs sign their events with, as given to agentapi hub join (default $AGENTAPI_HUB_SECRET); required")
	cmd.Flags().StringVar(&instanceToken, "instance-token", "", "API token sent to the AgentAPIs (default $AGENTAPI_HUB_INSTANCE_TOKEN, else $AGENTAPI_API_TOKEN)")
	cmd.Flags().StringVar(&statePath, "state", "", "File that keeps the instances (default ~/.agentapi/hub/instances.json)")
	return cmd
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
