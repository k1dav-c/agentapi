package httpapi_test

import (
	"bufio"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

type pageBody struct {
	HasMore    bool `json:"has_more"`
	NextBefore int  `json:"next_before"`
}

type richPage struct {
	Messages []transcript.RichMessage `json:"messages"`
	pageBody
}

type taskPage struct {
	Tasks []instanceapi.Task `json:"tasks"`
	pageBody
}

// The lists of the conversation come a page of tasks at a time, the
// latest without a cursor; earlier pages come from the state database,
// also after a restart, which loads only the latest tasks.
func TestHistoryPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	small := func(c *httpapi.ServerConfig) { c.HotTasks, c.RecentTasks = 5, 3 }
	n, srv, db, err := persistentServerConfig(t, path, "/work", nil, small)
	require.NoError(t, err)
	a := n.factory.Last()
	tasks := func(query string) taskPage {
		var body taskPage
		require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/tasks"+query, nil, &body))
		return body
	}
	const count = 12
	for i := 1; i <= count; i++ {
		n.send(fmt.Sprintf("task %d", i), "")
		require.Eventually(t, func() bool {
			page := tasks("?limit=1")
			return len(page.Tasks) == 1 && page.Tasks[0].Number == i
		}, 5*time.Second, 5*time.Millisecond)
		reply := fmt.Sprintf("reply %d", i)
		a.Sink().RichMessage(transcript.RichMessage{MessageID: "m" + strconv.Itoa(i), Role: "assistant", Timestamp: time.Now(), Content: []transcript.RichContentBlock{{Type: "text", Text: reply}}})
		a.Sink().SessionEvents([]transcript.SessionEvent{{Kind: "text", EventTime: time.Now(), Content: &reply}})
		a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
		require.Eventually(t, func() bool { return tasks("?limit=1").Tasks[0].Status == instanceapi.TaskStatusCompleted }, 5*time.Second, 5*time.Millisecond)
	}

	check := func() {
		t.Helper()
		latest := tasks("")
		require.Len(t, latest.Tasks, 3)
		assert.Equal(t, 10, latest.Tasks[0].Number)
		assert.True(t, latest.HasMore)
		assert.Equal(t, latest.Tasks[0].ID, latest.NextBefore)

		var msgs struct {
			Messages []httpapi.Message `json:"messages"`
			pageBody
		}
		// Without before and limit, every message, as upstream's clients
		// read them; with limit, a page like the others.
		n.do(http.MethodGet, "/messages", nil, &msgs)
		require.Len(t, msgs.Messages, 24)
		assert.False(t, msgs.HasMore)
		n.do(http.MethodGet, "/messages?limit=3", nil, &msgs)
		require.Len(t, msgs.Messages, 6)
		assert.Equal(t, "task 10", msgs.Messages[0].Content)
		assert.True(t, msgs.HasMore)

		// Paging back gets every rich message once, in order: the latest
		// from memory, the others from the state database.
		var texts []string
		before := 0
		for {
			var page richPage
			require.Equal(t, http.StatusOK, n.do(http.MethodGet, fmt.Sprintf("/rich-messages?before=%d&limit=4", before), nil, &page))
			var pageTexts []string
			for _, m := range page.Messages {
				pageTexts = append(pageTexts, m.Content[0].Text)
			}
			texts = append(pageTexts, texts...)
			if !page.HasMore {
				break
			}
			before = page.NextBefore
		}
		var want []string
		for i := 1; i <= count; i++ {
			want = append(want, fmt.Sprintf("reply %d", i))
		}
		assert.Equal(t, want, texts)

		var timeline struct {
			Events []transcript.SessionEvent `json:"events"`
			pageBody
		}
		n.do(http.MethodGet, fmt.Sprintf("/timeline?before=%d&limit=2", latest.NextBefore), nil, &timeline)
		require.Len(t, timeline.Events, 2)
		assert.Equal(t, "reply 8", *timeline.Events[0].Content)

		// The export streams the whole timeline.
		resp, err := http.Get(n.url + "/timeline/export")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))
		lines := 0
		for scanner := bufio.NewScanner(resp.Body); scanner.Scan(); {
			lines++
		}
		assert.Equal(t, count, lines)
	}
	check()
	stop(t, srv, db)

	n, srv, db, err = persistentServerConfig(t, path, "/work", nil, small)
	require.NoError(t, err)
	defer stop(t, srv, db)
	check()
}
