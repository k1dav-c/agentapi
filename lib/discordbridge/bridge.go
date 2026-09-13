package discordbridge

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/coder/agentapi/lib/handoff"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

type Config struct {
	AgentURL        string
	AgentToken      string
	TemporalAddress string
	Namespace       string
	TaskQueue       string
	TemporalTLS     bool
	TemporalAPIKey  string
	BotToken        string
	ChannelID       string
	AllowedUsers    []string
}

func coderWorkspaceURL() string {
	base := strings.TrimRight(os.Getenv("CODER_URL"), "/")
	owner := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_OWNER_NAME"))
	name := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_NAME"))
	if base == "" || owner == "" || name == "" {
		return ""
	}
	return base + "/@" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (c *Config) validate() error {
	c.AgentURL = strings.TrimRight(c.AgentURL, "/")
	u, err := url.Parse(c.AgentURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("agent URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	if c.BotToken == "" || c.ChannelID == "" {
		return fmt.Errorf("Discord bot token and channel ID are required")
	}
	users := c.AllowedUsers[:0]
	for _, id := range c.AllowedUsers {
		if id = strings.TrimSpace(id); id != "" {
			users = append(users, id)
		}
	}
	c.AllowedUsers = users
	if len(users) == 0 {
		return fmt.Errorf("at least one allowed Discord user ID is required")
	}
	if c.TemporalAddress == "" || c.Namespace == "" {
		return fmt.Errorf("Temporal address and namespace are required")
	}
	if c.TaskQueue == "" {
		sum := sha256.Sum256([]byte(c.AgentURL + "\n" + c.ChannelID))
		c.TaskQueue = fmt.Sprintf("agentapi-discord-%x", sum[:8])
	}
	return nil
}

// Run starts a worker, Discord Gateway connection and pending-request scanner.
// It never depends on browser presence and requires no inbound public endpoint.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.validate(); err != nil {
		return err
	}
	options := client.Options{HostPort: config.TemporalAddress, Namespace: config.Namespace}
	if config.TemporalTLS || config.TemporalAPIKey != "" {
		options.ConnectionOptions.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if config.TemporalAPIKey != "" {
		options.Credentials = client.NewAPIKeyStaticCredentials(config.TemporalAPIKey)
	}
	temporalClient, err := client.DialContext(ctx, options)
	if err != nil {
		return fmt.Errorf("connect to Temporal: %w", err)
	}
	defer temporalClient.Close()
	discord, err := discordgo.New("Bot " + config.BotToken)
	if err != nil {
		return fmt.Errorf("configure Discord: %w", err)
	}
	discord.Client.Timeout = 20 * time.Second
	discord.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentMessageContent
	activities := &Activities{
		AgentURL: config.AgentURL, AgentToken: config.AgentToken, ChannelID: config.ChannelID,
		WorkspaceURL: coderWorkspaceURL(),
		Discord:      discord, HTTP: &http.Client{Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	// Resolve identity before registering handlers; never trust a user-supplied
	// quoted message or an embed posted by a different bot.
	bot, err := discord.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("authenticate Discord bot: %w", err)
	}
	allowed := make(map[string]bool)
	for _, id := range config.AllowedUsers {
		allowed[id] = true
	}
	discord.AddHandler(func(session *discordgo.Session, event *discordgo.MessageCreate) {
		if event.Message == nil || event.Author == nil || event.Author.Bot || !allowed[event.Author.ID] || strings.TrimSpace(event.Content) == "" {
			return
		}
		ref := event.MessageReference
		if event.ChannelID == config.ChannelID && ref == nil {
			return
		}
		requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		var original *discordgo.Message
		var err error
		if ref != nil && ref.MessageID != "" {
			referenceChannel := ref.ChannelID
			if referenceChannel == "" {
				referenceChannel = event.ChannelID
			}
			original, err = session.ChannelMessage(referenceChannel, ref.MessageID, discordgo.WithContext(requestCtx))
			if err != nil && referenceChannel != config.ChannelID {
				// Discord may report the parent channel for a thread reference.
				original, err = session.ChannelMessage(config.ChannelID, ref.MessageID, discordgo.WithContext(requestCtx))
			}
		}
		if original == nil && event.ChannelID != config.ChannelID {
			// A normal message typed inside a thread has no MessageReference.
			// Message-thread IDs are also the starter-message IDs, so resolve the
			// workflow from that starter in the configured parent channel.
			original, err = session.ChannelMessage(config.ChannelID, event.ChannelID, discordgo.WithContext(requestCtx))
		}
		if err != nil {
			logger.Warn("Could not read referenced Discord message")
			return
		}
		workflowID := workflowIDFromMessage(original, bot.ID)
		if workflowID == "" {
			return
		}
		reply := handoff.Reply{RequestID: strings.TrimPrefix(workflowID, workflowPrefix), ID: event.ID, Content: event.Content}
		if err := temporalClient.SignalWorkflow(requestCtx, workflowID, "", ReplySignal, reply); err != nil {
			logger.Warn("Discord reply was not accepted by Temporal", "message_id", event.ID)
			_ = session.MessageReactionAdd(event.ChannelID, event.ID, "❌", discordgo.WithContext(requestCtx))
			return
		}
		// This acknowledges durable signal acceptance, not PTY execution.
		_ = session.MessageReactionAdd(event.ChannelID, event.ID, "📨", discordgo.WithContext(requestCtx))
	})
	w := worker.New(temporalClient, config.TaskQueue, worker.Options{})
	w.RegisterWorkflow(HumanReplyWorkflow)
	w.RegisterActivity(activities.DiscordNotify)
	w.RegisterActivity(activities.DiscordFeedback)
	w.RegisterActivity(activities.AgentReply)
	w.RegisterActivity(activities.AgentPending)
	if err := w.Start(); err != nil {
		return fmt.Errorf("start Temporal worker: %w", err)
	}
	defer w.Stop()
	if err := discord.Open(); err != nil {
		return fmt.Errorf("connect Discord Gateway: %w", err)
	}
	defer func() {
		if err := discord.Close(); err != nil {
			logger.Warn("Could not close Discord Gateway cleanly")
		}
	}()
	logger.Info("Discord handoff bridge started", "namespace", config.Namespace, "task_queue", config.TaskQueue)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	lastID := ""
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		pending, err := activities.Pending(requestCtx)
		if err != nil {
			logger.Warn("Could not read AgentAPI handoff", "error", err)
		} else if pending != nil && pending.ID != lastID {
			_, err = temporalClient.ExecuteWorkflow(requestCtx, client.StartWorkflowOptions{
				ID: workflowPrefix + pending.ID, TaskQueue: config.TaskQueue,
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			}, HumanReplyWorkflow, WorkflowInput{Request: *pending})
			var exists *serviceerror.WorkflowExecutionAlreadyStarted
			if err == nil || errors.As(err, &exists) {
				lastID = pending.ID
			} else {
				logger.Warn("Could not start Discord handoff workflow", "error", err)
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
