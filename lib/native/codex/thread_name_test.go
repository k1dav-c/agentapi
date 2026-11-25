package codex

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoNameThread(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("first")
	h.item("completed", "first", `{"id":"image","type":"userMessage","content":[{"type":"localImage","path":"/tmp/image.png"},{"type":"text","text":" \n "}]}`)
	h.completeTurn("first", "completed", "")
	h.expectNoNameRequest()

	h.beginTurn("second")
	prompt := "\n修正\t通知 " + strings.Repeat("測", 80) + "\n重現步驟"
	h.userText("second", "prompt", prompt)
	rename := h.expectSent("thread/name/set")
	name := "修正 通知 " + strings.Repeat("測", 53) + "…"
	assert.JSONEq(t, `{"threadId":"`+testThread+`","name":"`+name+`"}`, string(rename.Params))

	// The turn can finish while the rename is still waiting for its response.
	h.completeTurn("second", "completed", "")
	require.Len(t, snapshot(h.rec).completions, 2)
	h.feedJSON(`{"id":` + string(rename.ID) + `,"result":{}}`)
	h.feedJSON(string(mustJSON(map[string]any{
		"method": "thread/name/updated",
		"params": map[string]string{"threadId": testThread, "threadName": name},
	})))
	h.sync()
	assert.Equal(t, []string{name}, snapshot(h.rec).names)

	h.beginTurn("third")
	h.userText("third", "followup", "Also update the docs")
	h.completeTurn("third", "completed", "")
	h.expectNoNameRequest()

	h.beginTurn("fourth")
	h.userText("fourth", "new-topic", "換個主題：檢查登入逾時")
	rename = h.expectSent("thread/name/set")
	assert.JSONEq(t, `{"threadId":"`+testThread+`","name":"檢查登入逾時"}`, string(rename.Params))
	h.feedJSON(`{"id":` + string(rename.ID) + `,"result":{}}`)
	h.feedJSON(`{"method":"thread/name/updated","params":{"threadId":"` + testThread + `","threadName":"檢查登入逾時"}}`)
	h.completeTurn("fourth", "completed", "")
	assert.Equal(t, []string{name, "檢查登入逾時"}, snapshot(h.rec).names)
	assert.Equal(t, []string{testThread}, snapshot(h.rec).refs)
}

func TestAutoNamePreservesResumedName(t *testing.T) {
	h := newHarness(t, testThread, nil)
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{}}`)
	h.expectSent("initialized")
	h.answerAccount()
	resume := h.expectSent("thread/resume")
	h.feedJSON(`{"id":` + string(resume.ID) + `,"result":{"thread":{"id":"` + testThread + `","name":"Existing name"}}}`)
	h.settle()
	require.NoError(t, h.err("start"))

	h.beginTurn("follow-up")
	h.userText("follow-up", "prompt", "Continue where we left off")
	h.completeTurn("follow-up", "completed", "")
	h.expectNoNameRequest()
	assert.Equal(t, []string{"Existing name"}, snapshot(h.rec).names)
}

func TestAutoNameFailureLeavesTurnRunning(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn")
	h.userText("turn", "prompt", "Fix the parser")
	rename := h.expectSent("thread/name/set")
	h.feedJSON(`{"id":` + string(rename.ID) + `,"error":{"code":-32601,"message":"Unknown method"}}`)
	h.completeTurn("turn", "completed", "")
	state := snapshot(h.rec)
	require.Len(t, state.completions, 1)
	assert.Empty(t, state.names)
	assert.Empty(t, state.errors)
}

func (h *harness) userText(turnID, itemID, text string) {
	h.t.Helper()
	h.item("completed", turnID, string(mustJSON(map[string]any{
		"id": itemID, "type": "userMessage", "content": []userInput{{Type: "text", Text: text}},
	})))
}

func (h *harness) expectNoNameRequest() {
	h.t.Helper()
	select {
	case req := <-h.nameRequests:
		h.t.Fatalf("unexpected rename request: %s", req.Params)
	case <-time.After(50 * time.Millisecond):
	}
}
