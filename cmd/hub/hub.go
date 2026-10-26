// Package hub implements agentapi hub: connecting this machine's AgentAPIs
// to a hub, the receiver that watches many. Joining writes the hub's
// webhook into the user's config file, which every agentapi server reads
// when it starts, and applies it to the AgentAPIs running now.
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/registry"
	"github.com/coder/agentapi/lib/userconfig"
)

// The config file's settings the hub uses: the server flags of the same
// name.
const (
	keyURL    = "webhook-url"
	keySecret = "webhook-secret"
	keyEvents = "webhook-events"
	keyPublic = "public-url"
)

// defaultEvents are the events a hub gets: the instances, their tasks and
// what they wait on.
var defaultEvents = []string{"instance.*", "task.*", "request.*", "login.*", "limit.*", "agent.error"}

// requestTimeout is how long a running AgentAPI has to answer.
const requestTimeout = 5 * time.Second

// options are the flags every hub subcommand shares.
type options struct {
	configPath  string
	registryDir string
	apiToken    string
}

func (o *options) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.configPath, "config", "", "Config file (default ~/.agentapi/config.yaml)")
	cmd.Flags().StringVar(&o.registryDir, "registry-dir", "", "Registry directory of the running AgentAPIs (default ~/.agentapi/instances)")
	cmd.Flags().StringVar(&o.apiToken, "api-token", "", "Token for the running AgentAPIs that require one (default $AGENTAPI_API_TOKEN)")
}

func (o *options) resolve() error {
	var err error
	if o.configPath == "" {
		if o.configPath, err = userconfig.DefaultPath(); err != nil {
			return err
		}
	}
	if o.registryDir == "" {
		if o.registryDir, err = registry.DefaultDir(); err != nil {
			return err
		}
	}
	if o.apiToken == "" {
		o.apiToken = os.Getenv("AGENTAPI_API_TOKEN")
	}
	return nil
}

// HubCmd is agentapi hub.
func HubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "Run a hub that watches many AgentAPIs, or connect this machine's to one",
		Long: "A hub watches many AgentAPIs: it receives their webhook events. agentapi hub join writes the hub's " +
			"webhook into ~/.agentapi/config.yaml, which agentapi server reads when it starts, so the AgentAPIs this " +
			"user starts, also after a restart, report to the hub; it also applies it to the ones running now.",
	}
	cmd.AddCommand(joinCmd(), leaveCmd(), statusCmd(), serveCmd())
	return cmd
}

func joinCmd() *cobra.Command {
	var (
		opts      options
		secret    string
		events    []string
		publicURL string
		noCheck   bool
	)
	cmd := &cobra.Command{
		Use:   "join <hub webhook URL>",
		Short: "Report to a hub: now and whenever an AgentAPI starts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.resolve(); err != nil {
				return err
			}
			hubURL := args[0]
			if err := checkURL(hubURL); err != nil {
				return err
			}
			if secret == "" {
				secret = os.Getenv("AGENTAPI_HUB_SECRET")
			}
			out := cmd.OutOrStdout()
			if secret == "" {
				_, _ = fmt.Fprintln(out, "Warning: without --secret the hub can't tell the events are this machine's.")
			}
			if publicURL != "" {
				if err := checkURL(httpapi.ExpandPublicURL(publicURL, 1)); err != nil {
					return fmt.Errorf("--public-url: %w", err)
				}
			}
			if !noCheck {
				if err := checkReach(out, hubURL, publicURL); err != nil {
					return err
				}
				if err := checkHub(cmd.Context(), hubURL, secret); err != nil {
					return fmt.Errorf("%w\n(--no-check joins without checking, for a hub that doesn't run yet)", err)
				}
				_, _ = fmt.Fprintf(out, "The hub at %s took a signed test event.\n", hubURL)
			}
			config, err := userconfig.Load(opts.configPath)
			if err != nil {
				return err
			}
			config[keyURL] = hubURL
			config[keyEvents] = events
			if publicURL != "" {
				config[keyPublic] = publicURL
			}
			if secret != "" {
				config[keySecret] = secret
			} else {
				delete(config, keySecret)
			}
			if err := userconfig.Save(opts.configPath, config); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Wrote the hub to %s: AgentAPIs that start from now on report to %s.\n", opts.configPath, hubURL)
			return applyToRunning(cmd.Context(), out, opts, webhookChange{URL: hubURL, Events: &events, Secret: &secret, PublicURL: publicURL})
		},
	}
	opts.addFlags(cmd)
	cmd.Flags().StringVar(&secret, "secret", "", "Secret that signs the events, which the hub checks (default $AGENTAPI_HUB_SECRET)")
	cmd.Flags().StringSliceVar(&events, "events", defaultEvents, "Events to send to the hub")
	cmd.Flags().BoolVar(&noCheck, "no-check", false, "Join without checking that the hub takes this machine's events (for a hub that doesn't run yet)")
	cmd.Flags().StringVar(&publicURL, "public-url", "", "Where the hub reaches this machine's AgentAPIs, such as http://dev-1.vpn:{port} ({port} and {hostname} are filled in for each)")
	return cmd
}

func leaveCmd() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "leave",
		Short: "Stop reporting to the hub",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.resolve(); err != nil {
				return err
			}
			config, err := userconfig.Load(opts.configPath)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if config[keyURL] == nil {
				_, _ = fmt.Fprintf(out, "%s names no hub.\n", opts.configPath)
			} else {
				delete(config, keyURL)
				delete(config, keySecret)
				delete(config, keyEvents)
				delete(config, keyPublic)
				if err := userconfig.Save(opts.configPath, config); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "Removed the hub from %s.\n", opts.configPath)
			}
			empty, none := "", []string{}
			return applyToRunning(cmd.Context(), out, opts, webhookChange{URL: "", Events: &none, Secret: &empty})
		},
	}
	opts.addFlags(cmd)
	return cmd
}

func statusCmd() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the hub in the config file and where the running AgentAPIs report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.resolve(); err != nil {
				return err
			}
			config, err := userconfig.Load(opts.configPath)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if hubURL, ok := config[keyURL].(string); ok && hubURL != "" {
				_, _ = fmt.Fprintf(out, "Hub: %s (%s; signed: %v)\n", hubURL, opts.configPath, config[keySecret] != nil)
			} else {
				_, _ = fmt.Fprintf(out, "Hub: none in %s\n", opts.configPath)
			}
			entries, err := localEntries(opts.registryDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				var current webhookConfig
				status := ""
				if err := call(cmd.Context(), entry, opts.apiToken, http.MethodGet, nil, &current); err != nil {
					status = err.Error()
				} else if current.URL == "" {
					status = "no webhook"
				} else {
					status = "reports to " + current.URL
				}
				_, _ = fmt.Fprintf(out, "  %s (%s, %s): %s\n", displayName(entry), entry.AgentType, entry.URL, status)
			}
			return nil
		},
	}
	opts.addFlags(cmd)
	return cmd
}

// checkURL refuses what isn't an http or https URL.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("the hub's webhook URL must be an http or https URL, not %q", raw)
	}
	return nil
}

// webhookConfig is what GET /webhook returns, as far as the hub cares.
type webhookConfig struct {
	URL            string   `json:"url"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxAttempts    int      `json:"max_attempts"`
	Events         []string `json:"events"`
}

// webhookChange is the change of a running AgentAPI's webhook.
type webhookChange struct {
	URL    string
	Events *[]string
	Secret *string
	// PublicURL, if set, may hold {port} and {hostname}.
	PublicURL string
}

// applyToRunning changes the webhook of the AgentAPIs running on this
// machine, keeping their timeout and attempts.
func applyToRunning(ctx context.Context, out io.Writer, opts options, change webhookChange) error {
	entries, err := localEntries(opts.registryDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "No running AgentAPI is registered here. Ones that started before AgentAPI 1.2 aren't: restart them to apply.")
		return nil
	}
	var failed []string
	for _, entry := range entries {
		err := applyTo(ctx, entry, opts.apiToken, change)
		if err != nil {
			failed = append(failed, displayName(entry))
			_, _ = fmt.Fprintf(out, "  %s (%s): %v\n", displayName(entry), entry.URL, err)
			continue
		}
		_, _ = fmt.Fprintf(out, "  %s (%s): applied\n", displayName(entry), entry.URL)
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't change %s now; they apply the config file when they restart", strings.Join(failed, ", "))
	}
	return nil
}

func applyTo(ctx context.Context, entry registry.Entry, token string, change webhookChange) error {
	var current webhookConfig
	if err := call(ctx, entry, token, http.MethodGet, nil, &current); err != nil {
		return err
	}
	body := map[string]any{
		"url":             change.URL,
		"timeout_seconds": max(current.TimeoutSeconds, 1),
		"max_attempts":    max(current.MaxAttempts, 1),
	}
	if change.Events != nil {
		body["events"] = *change.Events
	}
	if change.Secret != nil {
		body["secret"] = *change.Secret
	}
	if change.PublicURL != "" {
		body["public_url"] = httpapi.ExpandPublicURL(change.PublicURL, entry.Port)
	}
	return call(ctx, entry, token, http.MethodPut, body, nil)
}

// call sends a request to /webhook of the AgentAPI entry names.
func call(ctx context.Context, entry registry.Entry, token, method string, body, out any) error {
	if entry.AuthRequired && token == "" {
		return errors.New("needs an API token (--api-token)")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(entry.URL, "/")+"/webhook", reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("%s /webhook: %s %s", method, resp.Status, strings.TrimSpace(string(detail)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// localEntries are the registered AgentAPIs of this machine: a registry
// directory shared between machines holds others' too.
func localEntries(dir string) ([]registry.Entry, error) {
	entries, err := registry.List(dir)
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	return slices.DeleteFunc(entries, func(e registry.Entry) bool { return e.Hostname != "" && e.Hostname != hostname }), nil
}

func displayName(entry registry.Entry) string {
	if entry.Name != "" {
		return entry.Name
	}
	return entry.ID
}
