package codex

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/jsonlwatcher"
)

func TestUnwrapShell(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{`/bin/bash -lc 'python3 -c "print(6*7)"'`, `python3 -c "print(6*7)"`},
		{`/bin/zsh -lc 'echo it'\''s'`, `echo it's`},
		{`sh -c "echo \"hi\" \$HOME"`, `echo "hi" $HOME`},
		{`bash -lc 'rg -n foo src | head -5 && echo done'`, `rg -n foo src | head -5 && echo done`},
		{"bash -lc 'line one\nline two'", "line one\nline two"},
		{`bash -lc ''`, ``},
		// Anything else is shown as it is.
		{`git -c core.pager=cat log`, `git -c core.pager=cat log`},
		{`python3 -c 'print(1)'`, `python3 -c 'print(1)'`},
		{`bash -lc 'a' && echo 'b'`, `bash -lc 'a' && echo 'b'`},
		{`bash -lc 'a'&&b`, `bash -lc 'a'&&b`},
		{`bash -c 'echo $0' arg`, `bash -c 'echo $0' arg`},
		{`bash -l -c 'ls'`, `bash -l -c 'ls'`},
		{`bash -c "echo $HOME"`, `bash -c "echo $HOME"`},
		{"bash -c \"a\\\nb\"", "bash -c \"a\\\nb\""},
		{`bash -lc 'unbalanced`, `bash -lc 'unbalanced`},
		{`bash -lc ls*`, `bash -lc ls*`},
		{`ls -la`, `ls -la`},
		{``, ``},
	} {
		assert.Equal(t, tc.want, unwrapShell(tc.command), tc.command)
	}
}

func TestActionsDetail(t *testing.T) {
	assert.Empty(t, actionsDetail(nil, "make"))
	assert.Empty(t, actionsDetail([]commandAction{{Type: "unknown", Command: "make test"}}, "make test"),
		"a reading that is just the command adds nothing")
	assert.Equal(t, []string{
		"Reads /work/a.txt", "Lists files in src", "Lists files", `Searches for "TODO" in src`, "Searches", "Runs make",
	}, actionsDetail([]commandAction{
		{Type: "read", Command: "cat a.txt", Path: "/work/a.txt"},
		{Type: "listFiles", Command: "ls src", Path: "src"},
		{Type: "listFiles", Command: "ls"},
		{Type: "search", Command: "rg TODO src", Query: "TODO", Path: "src"},
		{Type: "search", Command: "rg"},
		{Type: "unknown", Command: "make"},
	}, "cat a.txt; ls src; ls; rg TODO src; rg; make"))
}

func TestWebSearchSummary(t *testing.T) {
	for _, tc := range []struct{ action, query, want string }{
		{`{"type":"search","queries":["go 1.24","quartz"]}`, "", "Searched the web: go 1.24; quartz"},
		{`{"type":"search","query":"codex app-server"}`, "", "Searched the web: codex app-server"},
		{`null`, "from the item", "Searched the web: from the item"},
		{`{"type":"openPage","url":"https://go.dev"}`, "", "Opened https://go.dev"},
		{`{"type":"findInPage","url":"https://go.dev","pattern":"generics"}`, "", `Searched https://go.dev for "generics"`},
	} {
		assert.Equal(t, tc.want, webSearchSummary(&threadItem{Type: "webSearch", Query: tc.query, Action: json.RawMessage(tc.action)}), tc.action)
	}
}

func TestMCPContent(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
	text, images := mcpContent([]json.RawMessage{
		json.RawMessage(`{"type":"text","text":"one"}`),
		json.RawMessage(`{"type":"image","data":"` + png + `","mimeType":"image/png"}`),
		json.RawMessage(`{"type":"inputText","text":"two"}`),
		json.RawMessage(`{"type":"inputImage","imageUrl":"data:image/png;base64,` + png + `"}`),
		// Not images.
		json.RawMessage(`{"type":"inputImage","imageUrl":"https://example.com/a;base64,` + png + `"}`),
		json.RawMessage(`{"type":"image","data":"not base64","mimeType":"image/png"}`),
		json.RawMessage(`"junk"`),
	})
	assert.Equal(t, "one\ntwo", text)
	img := jsonlwatcher.RichImage{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}
	assert.Equal(t, []jsonlwatcher.RichImage{img, img}, images)
}
