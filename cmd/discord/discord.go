package discord

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coder/agentapi/lib/discordbridge"
	"github.com/coder/agentapi/lib/temporalconfig"
	"github.com/spf13/cobra"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func CreateCommand() *cobra.Command {
	var config discordbridge.Config
	var configPath string
	command := &cobra.Command{
		Use: "discord", Short: "Run the Temporal worker and Discord human-reply bridge",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveConfig(cmd, config, configPath)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return discordbridge.Run(ctx, resolved, slog.Default())
		},
	}
	f := command.Flags()
	f.StringVar(&configPath, "config", env("AGENTAPI_TEMPORAL_CONFIG", ".agentapi/temporal.json"), "Temporal settings exported or saved by Session Explorer (loaded at startup)")
	f.StringVar(&config.AgentURL, "agent-url", env("AGENTAPI_DISCORD_AGENT_URL", "http://localhost:3284"), "AgentAPI base URL reachable from this worker")
	f.StringVar(&config.AgentToken, "agent-token", "", "AgentAPI Bearer token (normally loaded from the JSON config)")
	f.StringVar(&config.TemporalAddress, "temporal-address", env("TEMPORAL_ADDRESS", "localhost:7233"), "Temporal host:port")
	f.StringVar(&config.Namespace, "temporal-namespace", env("TEMPORAL_NAMESPACE", "default"), "Temporal namespace (defaults to the AgentAPI session ID)")
	f.StringVar(&config.TaskQueue, "task-queue", env("AGENTAPI_DISCORD_TASK_QUEUE", ""), "Dedicated task queue for this AgentAPI endpoint (default: derived from URL and channel)")
	f.BoolVar(&config.TemporalTLS, "temporal-tls", env("TEMPORAL_TLS", "false") == "true", "Use TLS for Temporal")
	f.StringVar(&config.TemporalAPIKey, "temporal-api-key", "", "Temporal API key; enables TLS (normally loaded from the JSON config)")
	f.StringVar(&config.BotToken, "bot-token", "", "Discord bot token (normally loaded from the JSON config)")
	f.StringVar(&config.ChannelID, "channel-id", env("DISCORD_CHANNEL_ID", ""), "Discord channel for notifications and replies")
	f.StringSliceVar(&config.AllowedUsers, "allowed-users", strings.Split(env("DISCORD_ALLOWED_USER_IDS", ""), ","), "Comma-separated Discord user IDs allowed to reply (required)")
	// Cobra prints flag defaults in --help. Environment-provided credentials
	// must remain usable without appearing in help output.
	for _, name := range []string{"agent-token", "temporal-api-key", "bot-token"} {
		f.Lookup(name).DefValue = ""
	}
	return command
}

// Explicit flags override the saved profile. Credentials are loaded from the
// JSON document so the Worker has one portable source of truth.
func resolveConfig(cmd *cobra.Command, current discordbridge.Config, path string) (discordbridge.Config, error) {
	saved, err := temporalconfig.Load(path)
	if errors.Is(err, os.ErrNotExist) && !cmd.Flags().Changed("config") && os.Getenv("AGENTAPI_TEMPORAL_CONFIG") == "" {
		return current, nil
	}
	if err != nil {
		return current, fmt.Errorf("load Temporal configuration: %w", err)
	}
	useSaved := func(flag, environment string) bool { return !cmd.Flags().Changed(flag) && os.Getenv(environment) == "" }
	if useSaved("agent-url", "AGENTAPI_DISCORD_AGENT_URL") {
		current.AgentURL = saved.AgentURL
	}
	if useSaved("temporal-address", "TEMPORAL_ADDRESS") {
		current.TemporalAddress = saved.TemporalAddress
	}
	if useSaved("temporal-namespace", "TEMPORAL_NAMESPACE") {
		current.Namespace = saved.Namespace
	}
	if useSaved("task-queue", "AGENTAPI_DISCORD_TASK_QUEUE") {
		current.TaskQueue = saved.TaskQueue
	}
	if useSaved("temporal-tls", "TEMPORAL_TLS") {
		current.TemporalTLS = saved.TemporalTLS
	}
	if useSaved("channel-id", "DISCORD_CHANNEL_ID") {
		current.ChannelID = saved.ChannelID
	}
	if useSaved("allowed-users", "DISCORD_ALLOWED_USER_IDS") {
		current.AllowedUsers = saved.AllowedUsers
	}
	if !cmd.Flags().Changed("agent-token") {
		current.AgentToken = saved.AgentToken
	}
	if !cmd.Flags().Changed("temporal-api-key") {
		current.TemporalAPIKey = saved.TemporalAPIKey
	}
	if !cmd.Flags().Changed("bot-token") {
		current.BotToken = saved.BotToken
	}
	return current, nil
}
