package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	mf "github.com/coder/agentapi/lib/msgfmt"
)

// startJSONLWatcher stops any existing JSONL watcher and starts a new one
// for the given agent PID. It is called once during server creation and
// again after each agent restart so that tool calls, thinking, and usage
// data from the new session are captured.
func (s *Server) startJSONLWatcher(pid int) {
	s.stopJSONLWatcher()
	if pid <= 0 {
		return
	}

	var resolver jsonlwatcher.SessionResolver
	var parser jsonlwatcher.LineParser
	var sessionEventParser jsonlwatcher.SessionEventParser

	switch s.agentType {
	case mf.AgentTypeClaude:
		resolver = &jsonlwatcher.ClaudeResolver{PID: pid}
		parser = jsonlwatcher.NewClaudeParser()
		sessionEventParser = jsonlwatcher.NewClaudeSessionEventParser()
	case mf.AgentTypeCodex:
		resolver = &jsonlwatcher.CodexResolver{
			PID:       pid,
			CWD:       s.cwd,
			NotBefore: time.Now(),
		}
		parser = jsonlwatcher.NewCodexParser()
		sessionEventParser = jsonlwatcher.NewCodexSessionEventParser()
	case mf.AgentTypePi:
		resolver = &jsonlwatcher.PiResolver{
			PID:       pid,
			CWD:       s.cwd,
			NotBefore: time.Now(),
		}
		parser = jsonlwatcher.NewPiParser()
		sessionEventParser = jsonlwatcher.NewPiSessionEventParser()
	case mf.AgentTypeGoose, mf.AgentTypeAider, mf.AgentTypeGemini, mf.AgentTypeCopilot, mf.AgentTypeAmp,
		mf.AgentTypeCursor, mf.AgentTypeAuggie, mf.AgentTypeAmazonQ, mf.AgentTypeOpencode, mf.AgentTypeKimi,
		mf.AgentTypeCustom:
		// No session log to follow; the chat uses the terminal output.
	}

	if resolver == nil || parser == nil {
		return
	}

	watchCtx, cancel := context.WithCancel(s.jsonlParentCtx)
	done := &sync.WaitGroup{}
	s.jsonlWatcherCancel, s.jsonlWatcherDone = cancel, done

	// A new agent process starts a new session, unnamed until the agent
	// names it.
	s.emitter.SetSessionName("")
	namer := jsonlwatcher.NewSessionNamer(string(s.agentType))
	if polled, ok := namer.(jsonlwatcher.PolledSessionNamer); ok {
		done.Add(1)
		go func() {
			defer done.Done()
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
					if name, changed := polled.Poll(); changed {
						s.emitter.SetSessionName(name)
					}
				}
			}
		}()
	}

	w := jsonlwatcher.New(jsonlwatcher.Config{
		Resolver: resolver,
		Parser:   parser,
		Logger:   s.logger,
		OnMessage: func(msg jsonlwatcher.RichMessage) {
			s.storeRichImages(&msg)
			s.emitter.EmitRichMessage(msg)
		},
		OnLine: func(line []byte) {
			if namer != nil {
				if name, changed := namer.Name(line); changed {
					s.emitter.SetSessionName(name)
				}
			}
			events, err := sessionEventParser.ParseSessionEvents(line)
			if err != nil {
				s.logger.Debug("Failed to normalize session event", "error", err)
				return
			}
			s.emitter.EmitSessionEvents(events)
		},
	})
	done.Add(1)
	go func() {
		defer done.Done()
		w.Start(watchCtx)
	}()
}

// stopJSONLWatcher stops the session-log watcher, if one runs, and waits
// until it did: a dying agent's session must not reach the conversation
// that replaces it.
func (s *Server) stopJSONLWatcher() {
	if s.jsonlWatcherCancel == nil {
		return
	}
	s.jsonlWatcherCancel()
	s.jsonlWatcherDone.Wait()
	s.jsonlWatcherCancel, s.jsonlWatcherDone = nil, nil
}
