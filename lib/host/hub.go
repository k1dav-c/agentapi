package host

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/jsonapi"
)

// heartbeatInterval is how often the launcher tells the hub of the
// machine.
const heartbeatInterval = time.Minute

// hubBase is the hub's address, from its webhook URL (…/hooks).
func hubBase(webhookURL string) string {
	return strings.TrimSuffix(strings.TrimSuffix(webhookURL, "/"), "/hooks")
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// signedPost posts body to url signed with secret, as AgentAPI signs its
// webhook events.
func signedPost(ctx context.Context, client *http.Client, url, secret, eventType, instanceID string, body []byte, bearer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(instanceapi.HeaderDelivery, randomID())
	req.Header.Set(instanceapi.HeaderEvent, eventType)
	req.Header.Set(instanceapi.HeaderInstance, instanceID)
	req.Header.Set(instanceapi.HeaderTimestamp, timestamp)
	req.Header.Set(instanceapi.HeaderSignature, instanceapi.Sign(secret, timestamp, body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return client.Do(req)
}

// reportsToHub is whether the config file names a hub to report to.
func (c Config) reportsToHub() bool { return c.HubURL != "" && c.HubSecret != "" }

// report sends the hub a host.* event about the machine.
func (l *Launcher) report(ctx context.Context, eventType string) error {
	data := HostEventData{HostInfo: l.Info(ctx), Token: l.hostToken, HeartbeatSeconds: int(heartbeatInterval / time.Second)}
	instance := instanceapi.WebhookInstance{ID: "host~" + l.hostname, ServerID: l.runID, Hostname: l.hostname, Version: version.Version, URL: data.URL}
	body, err := json.Marshal(instanceapi.WebhookEvent{ID: randomID(), Type: eventType, CreatedAt: time.Now(), Instance: &instance, Data: data})
	if err != nil {
		return err
	}
	resp, err := signedPost(ctx, l.client, l.config.HubURL, l.config.HubSecret, eventType, instance.ID, body, "")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("the hub answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// reportLoop tells the hub of the machine until ctx ends, then that the
// launcher stops.
func (l *Launcher) reportLoop(ctx context.Context) {
	if !l.config.reportsToHub() {
		return
	}
	failing := false
	send := func(eventType string) {
		if err := l.report(ctx, eventType); err != nil {
			if !failing {
				l.logger.Warn("Couldn't report to the hub; trying again with the next heartbeat", "hub", l.config.HubURL, "error", err)
			}
			failing = true
			return
		}
		if failing {
			l.logger.Info("Reporting to the hub again", "hub", l.config.HubURL)
		}
		failing = false
	}
	send(EventHostStarted)
	tick := time.NewTicker(heartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = l.report(stop, EventHostStopped)
			cancel()
			return
		case <-tick.C:
			send(EventHostHeartbeat)
		}
	}
}

// HubCredentials gets each job's GitHub token from the hub, which gives
// out a token for the job's repository alone, kept here until five
// minutes before it expires.
type HubCredentials struct {
	URL      string
	Secret   string
	Hostname string
	// HostToken is the launcher's token, which the hub has from its
	// events: another machine that has the secret can't ask for this one's
	// jobs.
	HostToken string
	Client    *http.Client

	mu     sync.Mutex
	tokens map[string]Token
}

func (h *HubCredentials) Available() bool { return h.URL != "" && h.Secret != "" }

func (h *HubCredentials) Token(ctx context.Context, key Key) (Token, error) {
	h.mu.Lock()
	if t, ok := h.tokens[key.Slug()]; ok && time.Until(t.ExpiresAt) > 5*time.Minute {
		h.mu.Unlock()
		return t, nil
	}
	h.mu.Unlock()
	body, _ := json.Marshal(HubTokenRequest{Hostname: h.Hostname, Job: key.Slug()})
	resp, err := signedPost(ctx, h.Client, hubBase(h.URL)+HubTokenPath, h.Secret, "github.token", "host~"+h.Hostname, body, h.HostToken)
	if err != nil {
		return Token{}, fmt.Errorf("ask the hub for %s's token: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		if _, message := jsonapi.ReadError(data); message != "" {
			return Token{}, errors.New("the hub: " + message)
		}
		return Token{}, fmt.Errorf("the hub answered %s", resp.Status)
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return Token{}, err
	}
	if t.Value == "" {
		return Token{}, errors.New("the hub gave no token")
	}
	h.mu.Lock()
	if h.tokens == nil {
		h.tokens = map[string]Token{}
	}
	h.tokens[key.Slug()] = t
	h.mu.Unlock()
	return t, nil
}
