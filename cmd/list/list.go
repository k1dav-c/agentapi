// Package list implements `agentapi list`: the AgentAPIs running on this
// machine, from the registry directory, with their summaries.
package list

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/registry"
)

// summaryTimeout is how long an AgentAPI has to answer GET /summary.
const summaryTimeout = 2 * time.Second

var (
	registryDir string
	jsonOutput  bool
	apiToken    string
)

var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the AgentAPIs running on this machine",
	Long: "List the AgentAPIs that registered in the registry directory (see agentapi server --registry-dir), " +
		"with what each one's GET /summary says: whether the agent works, why it waits for you, and its unread task.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir := registryDir
		if dir == "" {
			var err error
			if dir, err = registry.DefaultDir(); err != nil {
				return err
			}
		}
		token := apiToken
		if token == "" {
			token = os.Getenv("AGENTAPI_API_TOKEN")
		}
		instances, err := listInstances(cmd.Context(), dir, token, http.DefaultClient)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), instances)
		}
		return writeTable(cmd.OutOrStdout(), instances)
	},
}

func init() {
	ListCmd.Flags().StringVar(&registryDir, "registry-dir", "", "Registry directory (default ~/.agentapi/instances)")
	ListCmd.Flags().BoolVar(&jsonOutput, "json", false, "Print the entries and summaries as JSON")
	ListCmd.Flags().StringVar(&apiToken, "api-token", "", "Token for the AgentAPIs that require one (default $AGENTAPI_API_TOKEN)")
}

// instance is a registered AgentAPI and what its GET /summary said.
type instance struct {
	registry.Entry
	Summary *instanceapi.SummaryBody `json:"summary,omitempty"`
	Error   string                   `json:"error,omitempty"`
}

// listInstances reads the registry in dir and asks each AgentAPI for its
// summary, all at once.
func listInstances(ctx context.Context, dir, token string, client *http.Client) ([]instance, error) {
	entries, err := registry.List(dir)
	if err != nil {
		return nil, err
	}
	instances := make([]instance, len(entries))
	var wg sync.WaitGroup
	for i, entry := range entries {
		instances[i].Entry = entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			summary, err := fetchSummary(ctx, client, entry, token)
			if err != nil {
				instances[i].Error = err.Error()
				return
			}
			instances[i].Summary = summary
		}()
	}
	wg.Wait()
	return instances, nil
}

func fetchSummary(ctx context.Context, client *http.Client, entry registry.Entry, token string) (*instanceapi.SummaryBody, error) {
	if entry.AuthRequired && token == "" {
		return nil, fmt.Errorf("needs an API token")
	}
	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(entry.URL, "/")+"/summary", nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /summary: %s", resp.Status)
	}
	var summary instanceapi.SummaryBody
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("GET /summary: %w", err)
	}
	if summary.Instance.ServerID != entry.ServerID {
		// Another AgentAPI took the port of one that crashed.
		return nil, fmt.Errorf("another AgentAPI answers at %s", entry.URL)
	}
	return &summary, nil
}

func writeJSON(w io.Writer, instances []instance) error {
	if instances == nil {
		instances = []instance{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(instances)
}

func writeTable(w io.Writer, instances []instance) error {
	if len(instances) == 0 {
		_, err := fmt.Fprintln(w, "No AgentAPI is running.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tAGENT\tSTATE\tURL\tTITLE")
	for _, in := range instances {
		name := in.Name
		if name == "" {
			name = in.ID
		}
		state, title := in.Error, ""
		if s := in.Summary; s != nil {
			state, title = stateOf(*s), s.Title
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, in.AgentType, state, in.URL, title)
	}
	return tw.Flush()
}

// stateOf is what an AgentAPI's summary says in one word or two: why it
// waits for the user, else whether it works, and an unread task.
func stateOf(s instanceapi.SummaryBody) string {
	state := "idle"
	switch {
	case s.Attention != instanceapi.AttentionNone:
		state = "needs " + string(s.Attention)
		if s.Attention == instanceapi.AttentionExited {
			state = "exited"
		}
	case s.Status == instanceapi.AgentStatusRunning:
		state = "working"
	}
	if s.Unread {
		state += ", unread"
	}
	return state
}
