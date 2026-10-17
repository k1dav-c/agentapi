//go:build unix

package httpapi_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native/nativetest"
)

// terminalServer runs a native server with a terminal in a temporary
// directory.
func terminalServer(t *testing.T, terminal bool) *nativeServer {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	factory := &nativetest.Factory{}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		NativeFactory:  factory.New,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		CWD:            t.TempDir(),
		Terminal:       terminal,
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Stop(context.Background())
		cancel()
	})
	return &nativeServer{t: t, url: ts.URL, factory: factory}
}

// terminalEvents reads the /terminal stream: decoded output, and the
// reason of an exit event.
func terminalEvents(t *testing.T, url string) (<-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/terminal", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	events := make(chan string, 64)
	go func() {
		defer close(events)
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		event := ""
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var body struct {
					Data   string `json:"data"`
					Reason string `json:"reason"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &body) != nil {
					continue
				}
				if event == "exit" {
					events <- "exit: " + body.Reason
					continue
				}
				data, _ := base64.StdEncoding.DecodeString(body.Data)
				events <- string(data)
			}
		}
	}()
	return events, cancel
}

func waitFor(t *testing.T, events <-chan string, want string) string {
	t.Helper()
	var got strings.Builder
	deadline := time.After(10 * time.Second)
	for !strings.Contains(got.String(), want) {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("the stream ended before %q: %q", want, got.String())
			}
			got.WriteString(e)
		case <-deadline:
			t.Fatalf("no %q in %q", want, got.String())
		}
	}
	return got.String()
}

func TestTerminal(t *testing.T) {
	n := terminalServer(t, true)
	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	assert.True(t, status.Terminal)
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/terminal/input", map[string]any{"data": "x"}, nil), "no shell before someone watches")

	events, stop := terminalEvents(t, n.url)
	defer stop()
	require.Eventually(t, func() bool {
		return n.do(http.MethodPost, "/terminal/resize", map[string]any{"cols": 120, "rows": 40}, nil) == http.StatusOK &&
			n.do(http.MethodPost, "/terminal/input", map[string]any{"data": "echo \"size $(stty size)\"\r"}, nil) == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond)
	waitFor(t, events, "size 40 120")

	// Another client gets what the terminal showed.
	again, stopAgain := terminalEvents(t, n.url)
	defer stopAgain()
	waitFor(t, again, "size 40 120")

	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/terminal/input", map[string]any{"data": "exit\r"}, nil))
	waitFor(t, events, "exit: The shell exited.")
}

func TestNoTerminal(t *testing.T) {
	n := terminalServer(t, false)
	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	assert.False(t, status.Terminal)
	assert.Equal(t, http.StatusNotFound, n.do(http.MethodPost, "/terminal/input", map[string]any{"data": "x"}, nil))
	assert.Equal(t, http.StatusNotFound, n.do(http.MethodPost, "/terminal/resize", map[string]any{"cols": 80, "rows": 24}, nil))
	events, stop := terminalEvents(t, n.url)
	defer stop()
	waitFor(t, events, "exit: There is no terminal")
}
