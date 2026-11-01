package sessions

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/pricing"
)

// testdata/corpus holds a session of each agent, cut from logs the CLIs
// wrote (Claude Code 2.1.299 and 2.1.302, Codex 0.163.0, Pi 1.3.0), with
// every string that isn't a record type, a model, a tag the readers go by
// or another fixed value replaced, and the IDs and times renumbered: the
// shapes of real records, and none of their content. All three ran in
// /work/app.

const corpusID = "00000000-0000-4000-8000-000000000001"

// corpusStore returns a Store of the corpus's sessions of agentType, and
// what it logs.
func corpusStore(t *testing.T, agentType string) (*Store, *strings.Builder) {
	t.Helper()
	var logs strings.Builder
	s, err := New(agentType, "/work/app", Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	return s, &logs
}

func corpusDir(parts ...string) string {
	return filepath.Join(append([]string{"testdata", "corpus"}, parts...)...)
}

func TestCorpusClaude(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", corpusDir("claude"))
	store, logs := corpusStore(t, "claude")

	// The session's prompts leave out the background task's notification,
	// the note of an interrupted turn and a slash command with its output.
	list, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, corpusID, list[0].ID)
	assert.Equal(t, "aiTitle 2", list[0].Title)
	assert.Equal(t, "content 2", list[0].FirstPrompt)
	assert.Equal(t, 2, list[0].Prompts)

	h, err := store.ReadHistory(corpusID)
	require.NoError(t, err)
	assert.Equal(t, "aiTitle 2", h.Title)
	assert.Equal(t, []string{"user: content 2", "agent: text 3\n\ntext 4", "user: text 5"}, roles(h.Messages))
	var rich []string
	for _, m := range h.Rich {
		var types []string
		for _, b := range m.Content {
			types = append(types, b.Type)
		}
		rich = append(rich, m.Role+" "+strings.Join(types, ","))
	}
	assert.Equal(t, []string{"assistant tool_use", "user tool_result", "assistant thinking,text", "assistant text"}, rich)

	subs, err := store.Subagents(corpusID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, "id-000020", subs[0].ID)
	assert.Equal(t, "general-purpose", subs[0].AgentType)
	assert.Equal(t, "toolu_000016", subs[0].ParentToolUseID, "the Agent tool call that started it")
	_, run, err := store.ReadSubagent(corpusID, "id-000020")
	require.NoError(t, err)
	assert.NotEmpty(t, run.Prompt)
	assert.NotEmpty(t, run.Messages)

	// Three calls of the session's, two of its subagent's; Claude Code's
	// own count of them (cost-state) is as much, give or take its title.
	d, err := store.Costs()
	require.NoError(t, err)
	c := d.Cost(corpusID)
	assert.Equal(t, 5, c.Calls)
	assert.Equal(t, pricing.Tokens{Input: 12, CacheRead: 64231, CacheWrite: 9991, CacheWrite1h: 1241, Output: 532}, c.Tokens, "the session writes the cache for an hour, its subagent for five minutes")
	var subCalls int
	for _, call := range d.Sessions[corpusID] {
		assert.False(t, call.Unitemized)
		if call.SubagentID == "id-000020" {
			subCalls++
		}
	}
	assert.Equal(t, 2, subCalls)
	assert.True(t, c.Models[0].Priced)
	assert.Empty(t, logs.String(), "the format knows every record, and reads every call")
}

func TestCorpusCodex(t *testing.T) {
	t.Setenv("CODEX_HOME", corpusDir("codex"))
	store, logs := corpusStore(t, "codex")

	// The developer messages and the environment are Codex's.
	list, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, corpusID, list[0].ID)
	assert.Equal(t, "thread_name 1", list[0].Title)
	assert.Equal(t, "text 2", list[0].FirstPrompt)
	assert.Equal(t, 4, list[0].Prompts)

	h, err := store.ReadHistory(corpusID)
	require.NoError(t, err)
	assert.Equal(t, "thread_name 1", h.Title)
	assert.Equal(t, []string{
		"user: text 2", "agent: text 5", "user: text 6", "agent: text 9",
		"user: text 10", "agent: text 13", "user: text 16", "agent: text 19",
	}, roles(h.Messages))

	// A call per change of the thread's running total; the third turn,
	// interrupted, counted its tokens twice.
	d, err := store.Costs()
	require.NoError(t, err)
	c := d.Cost(corpusID)
	assert.Equal(t, 4, c.Calls)
	assert.Equal(t, pricing.Tokens{Input: 9530, CacheRead: 51968, Output: 68}, c.Tokens)
	assert.Equal(t, "gpt-6.1-sol", c.Models[0].Model)
	assert.Empty(t, logs.String(), "the format knows every record, and reads every call")
}

func TestCorpusPi(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", corpusDir("pi", "agent"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	store, logs := corpusStore(t, "pi")

	list, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, corpusID, list[0].ID)
	assert.Equal(t, "name 5", list[0].Title)
	assert.Equal(t, "text 1", list[0].FirstPrompt)
	assert.Equal(t, 3, list[0].Prompts)

	h, err := store.ReadHistory(corpusID)
	require.NoError(t, err)
	assert.Equal(t, []string{"user: text 1", "agent: text 2", "user: text 3", "agent: text 6", "user: text 7", "agent: text 8"}, roles(h.Messages))

	// Pi prices its calls itself.
	d, err := store.Costs()
	require.NoError(t, err)
	c := d.Cost(corpusID)
	assert.Equal(t, 5, c.Calls)
	assert.InDelta(t, 0.0127324, c.USD, 1e-12)
	assert.Equal(t, pricing.Tokens{Input: 5105, CacheRead: 1024, Output: 242}, c.Tokens)
	assert.Empty(t, logs.String(), "the format knows every record, and reads every call")
}

// copyCorpus copies the corpus's dir to a new directory, with replace
// applied to its logs, and returns the copy.
func copyCorpus(t *testing.T, dir string, replace *strings.Replacer) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, filepath.WalkDir(corpusDir(dir), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(corpusDir(dir), path)
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".jsonl" {
			data = []byte(replace.Replace(string(data)))
		}
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), data, 0o600)
	}))
	return root
}

// The corpus with the fields of its calls renamed, as an agent's next
// version might name them: the calls, which would cost $0 without a word,
// are left out with a warning for each log.
func TestCorpusRenamedUsageWarns(t *testing.T) {
	for _, tc := range []struct {
		agent, dir string
		env        map[string]string
		replace    *strings.Replacer
		warning    string
		logs       int
	}{
		{
			agent: "claude", dir: "claude", env: map[string]string{"CLAUDE_CONFIG_DIR": ""},
			replace: strings.NewReplacer(`"input_tokens":`, `"inputTokens":`, `"output_tokens":`, `"outputTokens":`,
				`"cache_read_input_tokens":`, `"cacheReadInputTokens":`, `"cache_creation_input_tokens":`, `"cacheCreationInputTokens":`),
			warning: "model calls whose usage reads as no tokens", logs: 2,
		},
		{
			agent: "codex", dir: "codex", env: map[string]string{"CODEX_HOME": ""},
			replace: strings.NewReplacer(`"total_token_usage":`, `"total_usage":`, `"last_token_usage":`, `"last_usage":`),
			warning: "model calls whose usage reads as no tokens", logs: 1,
		},
		{
			agent: "pi", dir: filepath.Join("pi", "agent"), env: map[string]string{"PI_CODING_AGENT_DIR": "", "PI_CODING_AGENT_SESSION_DIR": "-"},
			replace: strings.NewReplacer(`"usage":{`, `"tokens":{`),
			warning: "model calls without a model or usage", logs: 1,
		},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			root := copyCorpus(t, tc.dir, tc.replace)
			for name, value := range tc.env {
				switch value {
				case "":
					value = root
				case "-":
					value = ""
				}
				t.Setenv(name, value)
			}
			store, logs := corpusStore(t, tc.agent)
			d, err := store.Costs()
			require.NoError(t, err)
			assert.Zero(t, d.Cost(corpusID).Calls, "the calls are left out")
			assert.Equal(t, tc.logs, strings.Count(logs.String(), "level=WARN"), logs.String())
			assert.Equal(t, tc.logs, strings.Count(logs.String(), tc.warning), logs.String())
			assert.Contains(t, logs.String(), "agent="+tc.agent)

			_, err = store.Costs()
			require.NoError(t, err)
			assert.Equal(t, tc.logs, strings.Count(logs.String(), "level=WARN"), "once per log")
		})
	}
}
