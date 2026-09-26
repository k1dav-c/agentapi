# Changelog

## Unreleased

### Features
- Agents panel: lists the sub-agents Codex spawns (name, nickname, status, current shell command, last message, elapsed time, tokens) and updates live; open it from the header or with Alt+↑ in chat mode. Backed by the new `GET /agents` endpoint and `agents_update` SSE event, read from Codex's per-agent session logs

### Fixes
- Codex: the rich-message/timeline watcher no longer switches to a sub-agent's session log (sub-agent logs share the cwd and are newer than the main one)
- Codex: detect the composer regardless of how many footer lines follow it (Codex v0.157+), so an idle Codex no longer reports `running` forever
- Claude Code: ignore the footer and background agents panel below the input box when detecting stability, so ticking subagent timers no longer keep the status `running`
- Message sending compares only the region above the input box, so a changing footer is not mistaken for the agent accepting the message
- Queue dispatch only looks for interactive terminal prompts near the bottom of the screen and never while the agent's input box is visible, so numbered lists in earlier output no longer block the queue
- Queued messages the agent never submitted are dropped with an error instead of being retyped into the input box
- Codex: messages are no longer merged with a leftover composer draft. When a turn with queued follow-up inputs is interrupted, Codex puts them back into the composer, and the next message sent through AgentAPI was appended to that text (Codex received "Then reply XReply with Y"). The composer is now cleared before each message
- Codex follow-up questions (question tool): a chat message sent while a question is showing becomes that question's free-text answer ("None of the above" + notes) instead of being typed into the dialog, where Enter would pick the default option. The question dialog (including its notes field and a user message just above it) is no longer mistaken for the composer. The tool card renders Codex's questions with option buttons and follows the question Codex is currently asking
- Option buttons and Discord option replies send only the digit for Codex too, which, like Claude Code, selects on the digit; the extra Enter answered the next question with its default
- Claude plan mode: a chat message sent while the ExitPlanMode approval dialog is showing is delivered as "Tell Claude what to change" feedback instead of approving the plan (the carriage return used to pick "Yes, and use auto mode")
- Chat messages sent while any other interactive prompt (e.g. a permission request) is showing are queued until it is answered instead of being typed into it and approving the highlighted option
- Discord free-text replies to the plan approval dialog are delivered as plan feedback
- Option buttons (chat UI) and Discord option replies no longer append Enter for Claude Code, which selects on the digit alone; the extra Enter submitted empty plan feedback (rejecting the plan) or approved the next prompt. Enter-only options no longer send Enter twice

## v0.13.0

### Features
- Render Claude thinking blocks as collapsible sections in the task timeline
- Per-message markdown/raw render toggle (replaces global toolbar toggle)

### Fixes
- Merge rich message content blocks on re-emit instead of dropping earlier blocks during Claude delta streaming
- Switch tailed JSONL file at runtime when Claude Code parks a session to a new path
- Reorder resolver priority so ParkedJobID scan runs before direct file check, preventing stale session selection

## v0.12.2

### Fixes
- drop x\b workaround for fixed Claude Code 0.2.70 paste-echo bug
- handle partial/malformed tool call detection for claude-code
- exorcise goroutine-leaking util.After from the codebase
- make writeStabilize Phase 1 non-fatal when agents don't echo input

## v0.12.1

### Fixes
- Prevent terminal echo from being captured as agent messages
- Update codex message box detection

## v0.12.0

### Features
- Experimental ACP integration
- Introduce state persistence

### Fixes
- Fix pr-preview and build-release workflow errors

### Chore
- Codebase refactor for abstraction
- Go version bump to 1.24
- Use coder/quartz

## v0.11.8

### Fix
- Update message box formatting detection for Claude

## v0.11.7

### Features
- format codex messages to skip the coder_report_task tool call

## v0.11.6

### Features
- Bump Next.js to 15.4.10

## v0.11.5

### Features
- Add tool call logging.
- Improve parsing/detection of tool call messages.

## v0.11.4

### Features
- Temporarily remove coder report_task tool-call logs

## v0.11.3

### Features
- format claude messages to skip the coder_report_task tool call

## v0.11.2

### Features
- Improved handling of initial prompt

## v0.11.1

### Features
- Add tooltips for buttons
- Autofocus message box on user's turn
- Add msgfmt logic for amp module
- Update msgfmt for latest version in opencode

## v0.11.0

### Features
- Support sending initial prompt via stdin

## v0.10.2

### Features
- Improve autoscroll UX

## v0.10.1

### Features
- Visual indicator for agent name in the UI (not in embed)
- Downgrade openapi version to v3.0.3
- Add CLI installation instructions in README.md

## v0.10.0

### Features
- Feature to upload files to agentapi
- Introduced clickable links
- Added e2e tests
- Fixed the resizing scroll issue

## v0.9.0

### Features
- Add support for initial prompt via `-I` flag

## v0.8.0

### Features
- Add Support for GitHub Copilot
- Fix inconsistent openapi generation

## v0.7.1

### Fixes

- Adds headers to prevent proxies buffering SSE connections

## v0.7.0

### Features
- Add Support for Opencode.
- Add support for Agent aliases
- Explicitly support AmazonQ
- Bump NEXT.JS version

## v0.6.3

- CI fixes.

## v0.6.2

- Fix incorrect version string.

## v0.6.1

### Features
- Handle animation on Amp cli start screen.

## v0.6.0

### Features

- Adds support for Auggie CLI.

## v0.5.0

### Features

- Adds support for Cursor CLI.

## v0.4.1

### Fixes

- Sets `CGO_ENABLED=0` in build process to improve compatibility with older Linux versions.

## v0.4.0

### Breaking changes

- If you're running agentapi behind a reverse proxy, you'll now likely need to set the `--allowed-hosts` flag. See the [README](./README.md) for more details.

### New features

- Sourcegraph Amp support
- Added a new `--allowed-hosts` flag to the `server` command.

### Fixes

- Updated Codex support after its TUI has been updated in a recent version.
