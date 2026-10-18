package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/quartz"
	"github.com/spf13/cobra"
	sse "github.com/tmaxmax/go-sse"
	"golang.org/x/term"
)

type ChannelWriter struct {
	ch chan []byte
}

func (c *ChannelWriter) Write(p []byte) (n int, err error) {
	// A Writer must not keep p: the reader reuses its buffer for the next
	// keys, while these may still wait to be sent.
	c.ch <- bytes.Clone(p)
	return len(p), nil
}

func (c *ChannelWriter) Receive() ([]byte, bool) {
	data, ok := <-c.ch
	return data, ok
}

type model struct {
	screen string
}

func (m model) Init() tea.Cmd {
	// Just return `nil`, which means "no I/O right now, please."
	return nil
}

type screenMsg struct {
	screen string
}

type finishMsg struct{}

//lint:ignore U1000 The Update function is used by the Bubble Tea framework
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case screenMsg:
		m.screen = msg.screen
		if m.screen != "" && m.screen[len(m.screen)-1] != '\n' {
			m.screen += "\n"
		}
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case finishMsg:
		return m, tea.Quit
	}

	return m, nil
}

func (m model) View() string {
	return m.screen
}

// client sends requests to the server, with the API token if there is one.
type client struct {
	url   string
	token string
}

var (
	errTokenNeeded  = errors.New("the server needs an API token: pass --api-token or set AGENTAPI_API_TOKEN")
	errTokenRefused = errors.New("the server refused the API token")
)

// do sends req, with the token. A 401 is errTokenNeeded or errTokenRefused.
func (c *client) do(req *http.Request) (*http.Response, error) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do request: %w", err)
	}
	if res.StatusCode == http.StatusUnauthorized {
		_ = res.Body.Close()
		if c.token == "" {
			return nil, errTokenNeeded
		}
		return nil, errTokenRefused
	}
	return res, nil
}

func (c *client) readScreen(ctx context.Context, ch chan<- httpapi.ScreenUpdateBody) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/internal/screen", nil)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	for ev, err := range sse.Read(res.Body, &sse.ReadConfig{
		// 256KB: screen can be big. The default terminal size is 80x1000,
		// which can be over 80000 bytes.
		MaxEventSize: 256 * 1024,
	}) {
		if err != nil {
			return fmt.Errorf("failed to read sse: %w", err)
		}
		var screen httpapi.ScreenUpdateBody
		if err := json.Unmarshal([]byte(ev.Data), &screen); err != nil {
			return fmt.Errorf("failed to unmarshal screen: %w", err)
		}
		ch <- screen
	}
	return nil
}

func (c *client) writeRawInput(ctx context.Context, msg string) error {
	messageRequest := httpapi.MessageRequestBody{
		Type:    httpapi.MessageTypeRaw,
		Content: msg,
	}
	messageRequestBytes, err := json.Marshal(messageRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal message request: %w", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/message", bytes.NewReader(messageRequestBytes))
	req.Header.Set("Content-Type", "application/json")

	res, err := c.do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = res.Body.Close()
	}()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to write raw input: %w", errors.New(res.Status))
	}

	return nil
}

// transport returns the server's transport.
func (c *client) transport(ctx context.Context) (httpapi.Transport, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/status", nil)
	resp, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("failed to check server status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected %d response from server: %s", resp.StatusCode, resp.Status)
	}

	var status httpapi.StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status.Body); err != nil {
		return "", fmt.Errorf("failed to decode server status: %w", err)
	}
	return status.Body.Transport, nil
}

// errNoTerminal says why a transport has no terminal to attach to, or is
// nil.
func errNoTerminal(transport httpapi.Transport) error {
	switch transport {
	case httpapi.TransportACP:
		return errors.New("attach is not yet supported in ACP mode")
	case httpapi.TransportNative:
		return errors.New("the native transport runs the agent without a terminal, so there is none to attach to; use the chat, whose Terminal button opens a shell")
	case httpapi.TransportPTY:
	}
	return nil
}

func runAttach(c *client) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport, err := c.transport(ctx)
	switch {
	case errors.Is(err, errTokenNeeded), errors.Is(err, errTokenRefused):
		return err
	case err != nil:
		_, _ = fmt.Fprintf(os.Stderr, "WARN: Unable to check server: %s\n", err.Error())
	default:
		if err := errNoTerminal(transport); err != nil {
			return err
		}
	}

	stdin := int(os.Stdin.Fd())

	oldState, err := term.MakeRaw(stdin)
	if err != nil {
		return fmt.Errorf("failed to make raw: %w", err)
	}
	defer func() {
		_ = term.Restore(stdin, oldState)
	}()

	stdinWriter := &ChannelWriter{
		ch: make(chan []byte, 4096),
	}
	tee := io.TeeReader(os.Stdin, stdinWriter)
	p := tea.NewProgram(model{}, tea.WithInput(tee), tea.WithAltScreen())
	screenCh := make(chan httpapi.ScreenUpdateBody, 64)

	readScreenErrCh := make(chan error, 1)
	go func() {
		defer close(readScreenErrCh)
		if err := c.readScreen(ctx, screenCh); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			readScreenErrCh <- fmt.Errorf("failed to read screen: %w", err)
		}
	}()
	writeRawInputErrCh := make(chan error, 1)
	go func() {
		defer close(writeRawInputErrCh)
		for {
			select {
			case <-ctx.Done():
				return
			case buf, ok := <-stdinWriter.ch:
				if !ok {
					return
				}
				input := string(buf)
				// Don't send Ctrl+C to the agent
				if input == "\x03" {
					continue
				}
				if err := c.writeRawInput(ctx, input); err != nil {
					writeRawInputErrCh <- fmt.Errorf("failed to write raw input: %w", err)
					return
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case screenUpdate, ok := <-screenCh:
				if !ok {
					return
				}
				p.Send(screenMsg{
					screen: screenUpdate.Screen,
				})
			}
		}
	}()
	pErrCh := make(chan error, 1)
	go func() {
		_, err := p.Run()
		pErrCh <- err
		close(pErrCh)
	}()

	select {
	case err = <-readScreenErrCh:
	case err = <-writeRawInputErrCh:
	case err = <-pErrCh:
	case <-ctx.Done():
		err = nil
	}

	p.Send(finishMsg{})
	graceTimer := quartz.NewReal().NewTimer(1 * time.Second)
	defer graceTimer.Stop()
	select {
	case <-pErrCh:
	case <-graceTimer.C:
	}

	return err
}

var (
	remoteURLArg string
	apiTokenArg  string
)

var AttachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Attach to a running agent",
	Long:  `Attach to the terminal of an agent that a server runs with the PTY transport`,
	RunE: func(cmd *cobra.Command, args []string) error {
		remoteURL := remoteURLArg
		if remoteURL == "" {
			return errors.New("URL is required")
		}
		cmd.SilenceUsage = true
		if !strings.HasPrefix(remoteURL, "http") {
			remoteURL = "http://" + remoteURL
		}
		token := apiTokenArg
		if token == "" {
			token = os.Getenv("AGENTAPI_API_TOKEN")
		}
		if err := runAttach(&client{url: strings.TrimRight(remoteURL, "/"), token: token}); err != nil {
			return fmt.Errorf("attach failed: %w", err)
		}
		return nil
	},
}

func init() {
	AttachCmd.Flags().StringVarP(&remoteURLArg, "url", "u", "localhost:3284", "URL of the agentapi server to attach to. May optionally include a protocol and a path.")
	AttachCmd.Flags().StringVar(&apiTokenArg, "api-token", "", "The server's API token, if it has one (default $AGENTAPI_API_TOKEN)")
}
