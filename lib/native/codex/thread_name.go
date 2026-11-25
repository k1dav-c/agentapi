package codex

import (
	"context"

	"github.com/coder/agentapi/lib/native"
)

// nameThreadFromPrompt runs after Codex records the user message. App-server
// confirms the title through thread/name/updated.
func (s *loopState) nameThreadFromPrompt(prompt string) {
	name := s.topic.Observe(prompt)
	if name == "" && s.nameFromPrompt {
		name = native.PromptTitle(prompt)
	}
	if name == "" || name == s.threadName {
		return
	}
	s.nameFromPrompt = false
	a := s.a
	params := threadSetNameParams{ThreadID: s.threadID, Name: name}
	a.titles.Send(func(ctx context.Context) {
		if _, err := a.call(ctx, "thread/name/set", params, nil); err != nil {
			a.log.Debug("Failed to name the Codex thread", "thread", params.ThreadID, "error", err)
		}
	})
}
