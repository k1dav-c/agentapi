package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/jsonapi"
)

// Token is a GitHub token a job's git and gh use.
type Token struct {
	Value     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// Credentials hands out the GitHub token of a job.
type Credentials interface {
	// Available reports whether there are credentials to hand out: without
	// them the jobs use the machine's own (an SSH key, gh's login).
	Available() bool
	Token(ctx context.Context, key Key) (Token, error)
}

// FileCredentials is one token for every job, read from a file each time:
// a stand-in until the hub hands out tokens for each repository.
type FileCredentials struct {
	Path string
}

func (f FileCredentials) Available() bool {
	data, err := os.ReadFile(f.Path)
	return err == nil && strings.TrimSpace(string(data)) != ""
}

func (f FileCredentials) Token(context.Context, Key) (Token, error) {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return Token{}, fmt.Errorf("read the GitHub token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return Token{}, fmt.Errorf("the GitHub token file %s is empty", f.Path)
	}
	return Token{Value: token}, nil
}

// EnvJob names the job of a process the launcher started: its git and gh
// ask the launcher for that job's token.
const EnvJob = "AGENTAPI_HOST_JOB"

// tokenPath is the socket API's one route.
const tokenPath = "/v1/token"

// RequestToken asks the launcher listening on socket for key's token.
func RequestToken(ctx context.Context, socket string, key Key) (Token, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		}},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://launcher"+tokenPath+"?job="+url.QueryEscape(key.Slug()), nil)
	if err != nil {
		return Token{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("ask the launcher (%s) for a token: %w", socket, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		if _, message := jsonapi.ReadError(body); message != "" {
			return Token{}, errors.New(message)
		}
		return Token{}, fmt.Errorf("the launcher answered %s", resp.Status)
	}
	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return Token{}, err
	}
	return token, nil
}

// GitCredential answers git's credential helper protocol on in and out:
// for get, the job's token for https://github.com, nothing else.
func GitCredential(ctx context.Context, operation string, in io.Reader, out io.Writer, socket string, key Key) error {
	if operation != "get" {
		return nil
	}
	attrs := map[string]string{}
	data, err := io.ReadAll(io.LimitReader(in, 1<<16))
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if name, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			attrs[name] = value
		}
	}
	if attrs["protocol"] != "https" || attrs["host"] != "github.com" {
		return nil
	}
	if path := strings.TrimSuffix(strings.Trim(attrs["path"], "/"), ".git"); path != "" && !strings.EqualFold(path, key.Repo()) {
		return fmt.Errorf("job %s has no credentials for %s", key, path)
	}
	token, err := RequestToken(ctx, socket, key)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "username=x-access-token\npassword=%s\n", token.Value)
	return err
}
