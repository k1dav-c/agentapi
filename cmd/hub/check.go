package hub

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

// checkTimeout is how long the hub has to take the test event.
const checkTimeout = 10 * time.Second

// checkReach refuses a public URL the hub can't reach: a hub on another
// machine can't reach this one at localhost. Without a public URL the
// AgentAPIs report this machine's name, which the hub's machine must
// resolve; it says so.
func checkReach(out io.Writer, hubURL, publicURL string) error {
	hub, err := url.Parse(hubURL)
	if err != nil || loopbackHost(hub.Hostname()) {
		return nil
	}
	if publicURL == "" {
		host, _ := os.Hostname()
		_, _ = fmt.Fprintf(out, "Note: without --public-url the hub reaches these AgentAPIs at http://%s:<port>; "+
			"that name must resolve on the hub's machine.\n", host)
		return nil
	}
	public, err := url.Parse(instanceapi.ExpandPublicURL(publicURL, 1))
	if err == nil && loopbackHost(public.Hostname()) {
		return fmt.Errorf("the hub at %s is on another machine, so it can't reach this one at %s: "+
			"give --public-url the address the hub reaches this machine at, such as http://dev-1.vpn:{port}", hubURL, public.Hostname())
	}
	return nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkHub sends the hub a signed webhook.test event, as the AgentAPIs
// will send theirs, and explains why it didn't take it: it isn't reached,
// a sign-in page is in front of it, or the secret differs.
func checkHub(ctx context.Context, hubURL, secret string) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	now := time.Now()
	body, err := json.Marshal(map[string]any{
		"id":         "hub-join-check-" + hex.EncodeToString(id),
		"type":       instanceapi.EventWebhookTest,
		"created_at": now.UTC(),
		"instance":   map[string]any{"id": "agentapi-hub-join-check"},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hubURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AgentAPI-Webhook/1.0")
	req.Header.Set(instanceapi.HeaderEvent, instanceapi.EventWebhookTest)
	req.Header.Set(instanceapi.HeaderTimestamp, timestamp)
	if secret != "" {
		req.Header.Set(instanceapi.HeaderSignature, instanceapi.Sign(secret, timestamp, body))
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
		// A redirect is the answer: the AgentAPIs don't follow one either.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return fmt.Errorf("the hub at %s didn't answer within %s: check the address, and that this machine reaches the hub's", hubURL, checkTimeout)
		}
		return fmt.Errorf("can't reach the hub at %s: %w", hubURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code >= 300 && code < 400:
		return fmt.Errorf("%s redirects to %s: a sign-in page is in front of it, such as a Coder port URL's. "+
			"The AgentAPIs send their events themselves, so give an address of the hub that needs no sign-in, "+
			"such as its address on your network or VPN", hubURL, resp.Header.Get("Location"))
	case code == http.StatusUnauthorized && secret == "":
		return fmt.Errorf("the hub at %s takes only signed events: give --secret (or $AGENTAPI_HUB_SECRET), the hub's --secret", hubURL)
	case code == http.StatusUnauthorized:
		return fmt.Errorf("the hub at %s refused the signature: the secret differs from the hub's --secret, "+
			"or this machine's clock is more than 10 minutes off", hubURL)
	default:
		return fmt.Errorf("%s answered %s, not as a hub: is it the hub's address with /hooks?", hubURL, resp.Status)
	}
}
