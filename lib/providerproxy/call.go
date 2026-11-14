package providerproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/agentapi/lib/agentstate"
)

// call is one of the agent's calls the proxy sends on: what its health
// counts once it ends.
type call struct {
	proxy    *Proxy
	provider string
	name     string
	style    string
	request  *http.Request
	start    time.Time
	// firstByte is how long the provider took to answer with headers.
	firstByte time.Duration
}

// errorPeek is how much of a failed answer's body the proxy reads its
// error message from, as the body goes by.
const errorPeek = 4 << 10

// messageLimit is how many characters of an error message health keeps.
const messageLimit = 200

// redirectError is a redirect the proxy didn't follow: one with a request
// body, which it doesn't keep to send again.
type redirectError struct {
	status   int
	location string
}

func (e *redirectError) Error() string {
	return fmt.Sprintf("the provider redirected the call (%d) to %s, which AgentAPI doesn't follow", e.status, e.location)
}

// answered sees the provider's answer, which streams on to the agent.
func (c *call) answered(resp *http.Response) error {
	c.firstByte = c.proxy.clock.Since(c.start)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
		location := resp.Header.Get("Location")
		if u, err := resp.Request.URL.Parse(location); err == nil {
			// Its origin and path, without a query that may carry anything.
			location = u.Scheme + "://" + u.Host + u.Path
		}
		return &redirectError{status: resp.StatusCode, location: location}
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		// The proxy hands the connection over as it is (a WebSocket).
		c.record(outcomeSuccess, nil)
		return nil
	}
	resp.Body = &watchedBody{ReadCloser: resp.Body, call: c, status: resp.StatusCode, peek: failureStatus(resp.StatusCode)}
	return nil
}

// failed answers a call the provider didn't answer, or answered with a
// redirect the proxy doesn't follow.
func (c *call) failed(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		// The agent cancelled the call.
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	failure := agentstate.ProviderFailure{Kind: agentstate.ProviderFailureNetwork, Message: shortMessage(networkMessage(err))}
	status := http.StatusBadGateway
	var redirect *redirectError
	var netErr net.Error
	switch {
	case errors.As(err, &redirect):
		failure.Kind, failure.Status = agentstate.ProviderFailureStatus, redirect.status
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		failure.Kind = agentstate.ProviderFailureTimeout
		status = http.StatusGatewayTimeout
	}
	c.record(outcomeFailure, &failure)
	writeError(w, c.style, status, fmt.Sprintf("AgentAPI's provider proxy couldn't get an answer from %s: %s", c.name, failure.Message))
}

// networkMessage is err without the URL net/http adds to it.
func networkMessage(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return err.Error()
}

// failureStatus reports whether an answer with status is the provider's
// failure: 5xx, or 429 (overloaded, out of capacity or quota).
func failureStatus(status int) bool {
	return status >= 500 || status == http.StatusTooManyRequests
}

// outcome is how a call ended, as health counts it.
type outcome int

const (
	outcomeSuccess outcome = iota
	outcomeClientError
	outcomeFailure
)

// record counts the call in its provider's health.
func (c *call) record(o outcome, failure *agentstate.ProviderFailure) {
	now := c.proxy.clock.Now()
	if failure != nil {
		failure.At = now.UTC()
		c.proxy.logger.Debug("A model provider failed a call", "provider", c.provider, "kind", failure.Kind, "status", failure.Status, "message", failure.Message)
	}
	c.proxy.health.record(c.provider, now, o, c.firstByte, failure)
}

// watchedBody is the provider's answer as it streams to the agent: when
// it ends, the call counts in health.
type watchedBody struct {
	io.ReadCloser
	call   *call
	status int
	// peek: the body of a failed answer is kept, up to errorPeek, for its
	// message.
	peek    bool
	peeked  []byte
	readErr error
	once    sync.Once
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.peek && len(b.peeked) < errorPeek {
		b.peeked = append(b.peeked, p[:min(n, errorPeek-len(b.peeked))]...)
	}
	if err != nil && !errors.Is(err, io.EOF) && b.readErr == nil {
		b.readErr = err
	}
	return n, err
}

func (b *watchedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.ended)
	return err
}

// ended counts the call: a failure for a failed status or an answer that
// broke off (not one the agent stopped reading), a client error for
// another 4xx, else a success.
func (b *watchedBody) ended() {
	c := b.call
	cancelled := c.request.Context().Err() != nil
	switch {
	case failureStatus(b.status):
		c.record(outcomeFailure, &agentstate.ProviderFailure{Kind: agentstate.ProviderFailureStatus, Status: b.status, Message: shortMessage(errorMessage(b.status, b.peeked))})
	case b.readErr != nil && !cancelled:
		c.record(outcomeFailure, &agentstate.ProviderFailure{Kind: agentstate.ProviderFailureStream, Status: b.status, Message: shortMessage("the answer broke off: " + networkMessage(b.readErr))})
	case b.status >= 400:
		c.record(outcomeClientError, nil)
	default:
		c.record(outcomeSuccess, nil)
	}
}

// errorMessage is the message of a failed answer: the provider's own, in
// the Anthropic or OpenAI format, else its status.
func errorMessage(status int, body []byte) string {
	var parsed struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		var plain string
		switch {
		case json.Unmarshal(parsed.Error, &nested) == nil && nested.Message != "":
			return nested.Message
		case json.Unmarshal(parsed.Error, &plain) == nil && plain != "":
			return plain
		case parsed.Message != "":
			return parsed.Message
		}
	}
	return fmt.Sprintf("%d %s", status, http.StatusText(status))
}

// shortMessage is s on one line, cut at messageLimit characters.
func shortMessage(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= messageLimit {
		return s
	}
	runes := []rune(s)
	return string(runes[:messageLimit-1]) + "…"
}
