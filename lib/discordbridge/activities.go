package discordbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/coder/agentapi/lib/handoff"
	"go.temporal.io/sdk/temporal"
)

type Activities struct {
	AgentURL        string
	AgentToken      string
	ChannelID       string
	WorkspaceURL    string
	Discord         *discordgo.Session
	HTTP            *http.Client
	mu              sync.Mutex
	threadBySession map[string]string
	messageChannel  map[string]string
}

func (a *Activities) agentRequest(ctx context.Context, method, path string, body, output any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.AgentURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.AgentToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.AgentToken)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("AgentAPI request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not include response bodies or URLs: they may contain credentials.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			return temporal.NewNonRetryableApplicationError(fmt.Sprintf("AgentAPI returned HTTP %d", resp.StatusCode), "AgentAPIRejected", nil)
		}
		return fmt.Errorf("AgentAPI returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(output)
}

func (a *Activities) Pending(ctx context.Context) (*handoff.Request, error) {
	var pending handoff.Pending
	err := a.agentRequest(ctx, http.MethodGet, "/handoff", nil, &pending)
	return pending.Request, err
}

func (a *Activities) AgentPending(ctx context.Context, id string) (bool, error) {
	pending, err := a.Pending(ctx)
	return pending != nil && pending.ID == id, err
}

func (a *Activities) AgentReply(ctx context.Context, reply handoff.Reply) (handoff.Result, error) {
	var result handoff.Result
	err := a.agentRequest(ctx, http.MethodPost, "/handoff/reply", reply, &result)
	return result, err
}

func truncate(text string, size int) string {
	runes := []rune(text)
	if len(runes) <= size {
		return text
	}
	return string(runes[:size-1]) + "…"
}

// Keep the end of long output so the current question/options remain visible.
// 1700 Unicode runes also fit Discord's limit when every rune is a surrogate pair.
func notificationExcerpt(text string) string {
	text = stripANSI(text)
	runes := []rune(text)
	if len(runes) <= 1700 {
		return text
	}
	return "…" + string(runes[len(runes)-1699:])
}

var ansiSequence = regexp.MustCompile(`\x1b(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)
var terminalOptionLine = regexp.MustCompile(`(?m)^\s*[❯›>]?\s*(\d+)\.\s+(.+)$`)

func stripANSI(text string) string {
	return strings.TrimSpace(ansiSequence.ReplaceAllString(text, ""))
}

func threadTitle(text string) string {
	text = stripANSI(text)
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "AgentAPI handoff"
	}
	return text
}

func terminalOptions(text string) []string {
	text = stripANSI(text)
	matches := terminalOptionLine.FindAllStringSubmatch(text, -1)
	options := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(options) == 5 {
			break
		}
		options = append(options, match[1])
	}
	return options
}

func (a *Activities) DiscordNotify(ctx context.Context, request handoff.Request) (string, error) {
	if len(request.ID) != 64 {
		return "", temporal.NewNonRetryableApplicationError("invalid request ID", "InvalidRequest", nil)
	}
	active, err := a.AgentPending(ctx, request.ID)
	if err != nil {
		return "", err
	}
	if !active {
		return "", temporal.NewNonRetryableApplicationError("request already handled", "Superseded", nil)
	}
	instructions := "Reply to this message to continue the agent. You can also continue in the AgentAPI web UI."
	if request.Kind == "terminal" {
		instructions = "Reply to this message with a displayed option number, `enter`, or `esc`. Other terminal prompts must be handled in the web UI."
	}
	// A stable nonce suppresses duplicate creates on short activity retries.
	// Discord only deduplicates recent nonces; request IDs also protect replies
	// if a much later retry produces another notification.
	payload := struct {
		*discordgo.MessageSend
		Nonce        string `json:"nonce"`
		EnforceNonce bool   `json:"enforce_nonce"`
	}{
		MessageSend: &discordgo.MessageSend{
			Content:         instructions,
			AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
			Embeds: []*discordgo.MessageEmbed{{
				Title:       truncate(request.AgentType+" · waiting for your reply", 200),
				Description: notificationExcerpt(request.Content),
				URL:         a.WorkspaceURL,
				Footer:      &discordgo.MessageEmbedFooter{Text: workflowPrefix + request.ID},
			}},
		},
		Nonce: request.ID[:24], EnforceNonce: true,
	}
	if a.WorkspaceURL != "" {
		payload.MessageSend.Components = []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Open Coder workspace", Style: discordgo.LinkButton, URL: a.WorkspaceURL},
			}},
		}
	}
	if request.Kind == "terminal" {
		buttons := make([]discordgo.MessageComponent, 0, len(terminalOptions(request.Content)))
		for _, option := range terminalOptions(request.Content) {
			buttons = append(buttons, discordgo.Button{Label: option, Style: discordgo.PrimaryButton, CustomID: "agentapi-option:" + request.ID + ":" + option})
		}
		if len(buttons) > 0 {
			payload.MessageSend.Components = append(payload.MessageSend.Components, discordgo.ActionsRow{Components: buttons})
		}
	}
	a.mu.Lock()
	threadID := a.threadBySession[request.SessionID]
	a.mu.Unlock()
	channelID := a.ChannelID
	if threadID != "" {
		channelID = threadID
	}
	endpoint := discordgo.EndpointChannelMessages(channelID)
	data, err := a.Discord.RequestWithBucketID(http.MethodPost, endpoint, payload, endpoint, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	var message discordgo.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return "", err
	}
	if threadID == "" {
		// Keep each session isolated in its own Discord thread.
		threadName := truncate(threadTitle(request.Question), 100)
		if request.Question == "" {
			threadName = truncate(threadTitle(request.Content), 100)
		}
		thread, err := a.Discord.MessageThreadStart(a.ChannelID, message.ID, threadName, 1440, discordgo.WithContext(ctx))
		if err != nil {
			return "", err
		}
		threadID = thread.ID
	}
	a.mu.Lock()
	if a.threadBySession == nil {
		a.threadBySession = make(map[string]string)
	}
	if a.messageChannel == nil {
		a.messageChannel = make(map[string]string)
	}
	a.threadBySession[request.SessionID] = threadID
	a.messageChannel[message.ID] = channelID
	a.mu.Unlock()
	return message.ID, nil
}

func (a *Activities) DiscordFeedback(ctx context.Context, input FeedbackInput) error {
	text := map[string]string{
		"applied":    "Reply delivered to AgentAPI. Execution can continue.",
		"superseded": "This request is no longer active (handled in the web UI, or the agent moved on/restarted).",
		"invalid":    "Reply with a displayed option number, `enter`, or `esc`. This request is still waiting.",
		"uncertain":  "Delivery could not be confirmed. Automatic resend was stopped; check the AgentAPI terminal before continuing.",
		"expired":    "This request expired after seven days. Continue in the AgentAPI web UI.",
	}[input.Outcome]
	if text == "" {
		return fmt.Errorf("unknown reply outcome %q", input.Outcome)
	}
	a.mu.Lock()
	channelID := a.messageChannel[input.MessageID]
	a.mu.Unlock()
	if channelID == "" {
		channelID = a.ChannelID
	}
	_, err := a.Discord.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID: input.MessageID, Channel: channelID, Content: &text,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	}, discordgo.WithContext(ctx))
	return err
}

func workflowIDFromMessage(message *discordgo.Message, botID string) string {
	if message == nil || message.Author == nil || message.Author.ID != botID {
		return ""
	}
	for _, embed := range message.Embeds {
		if embed.Footer == nil {
			continue
		}
		id := strings.TrimPrefix(embed.Footer.Text, workflowPrefix)
		if id == embed.Footer.Text || len(id) != 64 {
			continue
		}
		valid := true
		for _, c := range id {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				valid = false
				break
			}
		}
		if valid {
			return workflowPrefix + id
		}
	}
	return ""
}
