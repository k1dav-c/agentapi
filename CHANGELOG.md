# Changelog

## Unreleased

### Features
- TTY mode works on phones: a Keyboard button in the key bar opens and closes the on-screen keyboard (it no longer pops up when TTY mode opens), the key bar stays above the keyboard on iOS, and the bar has ←, → and Shift+Tab. The terminal input turns off autocorrect and auto-capitalization
- Chat: Codex shows its logo (the OpenAI mark, as on Codex's own pages) in the header, the agent's turn and the tab icon, drawn in the text color so it follows dark mode
- The chat header shows the session name, the one the agent gives it: Claude Code's title (or its `/rename`), Codex's thread name, or Pi's `/name`. On a phone it replaces the agent name next to the agent's mark. `GET /status` and `status_change` report it as `session_name`
- Chat: your messages read as yours. Each prompt sits in a tinted block labelled "You", and the agent's turn starts with its mark and name (e.g. Pi), so the prompt and the reply no longer run together
- Pi coding agent support (`--type=pi`, auto-detected for the `pi` executable). The chat shows Pi's replies, tool calls, thinking and token usage from its session log, and knows when Pi is ready for input from its editor
- Pi in the chat: the header and tab icon show Pi's logo; Pi's dialogs (such as project trust) appear as a question card; the Explorer's MCP tab edits Pi's servers (`~/.pi/agent/mcp.json`); `GET /usage` reports the limits of the provider Pi is logged in to; and Restart runs `pi update --self` first, like `codex update` for Codex
- TTY mode fills the window: the terminal font grows with the window (11–20px) instead of a fixed 13px, and the mirror uses the agent's real terminal width. `GET /status` and `status_change` report it as `terminal_columns`, so servers started with `--term-width` line up too
- Chat: background-tab alerts. The tab icon shows an amber dot while the agent needs you, a blue dot while it works, and a green dot when a task finished while you were away. Optional browser notifications (status menu → Notify me in the background) fire when the agent needs you or finishes while the tab is hidden
- Chat: the composer is just a task box (text, files, voice, send). The Terminal tab's keystroke pad is gone; TTY mode covers typing into the agent and now has a key bar (Esc, Tab, arrows, Enter, Ctrl+C/L, and Ctrl+D/Z with a second press) for phones
- Chat: a new session shows only the agent's state and how tasks queue; the suggested starter prompts are gone
- Chat redesign ("Transcript"): one 48px top bar; tasks read as a transcript with a time rail, the prompt as the heading, agent replies as Markdown prose, tool calls as one-line instrument rows (`$ command`, exit code, duration, output preview) and thinking as margin notes on wide screens. A status strip above the composer shows what the agent is doing; when it asks something, a decision card replaces the in-transcript option bar (number keys answer) and the header, tab title and queue say "Needs you" / held instead of "Ready". The screen-parsed copy of an answer is hidden behind "Terminal output" when the session log has it. Graphite & Signal palette with the same hue in light and dark, Geist Mono for machine output; Explorer opens with ⌘K, lists filter, and Restart moved to a Session tab with a two-step confirm; TTY mode has an 80-column ruler. Long tasks show their latest 40 steps first
- Chat: reopening a long conversation is fast. The transcript is cached in the browser (IndexedDB) and shown immediately; the page then asks the server only for what changed. Incoming updates are applied in batches (a reload used to re-render once per replayed message), and older tasks render progressively while the browser is idle. On a 13-task, 764-message session the latest reply appears after 0.3 s instead of 1.8 s, and on a phone-class CPU after 1.2 s instead of 7.2 s
- `GET /events?sync=1` (opt-in): the stream starts with a `session_sync` event and every `message_update` / `rich_message_update` carries a `seq`. Reconnecting with `&since=<seq>&epoch=<epoch>` replays only what changed since; a different epoch (server restart, conversation reset) replays everything, and a reset notifies connected clients. Clients that don't pass `sync` get the same stream as before
- The chat UI shows the AgentAPI version next to the agent name (header, session details and embed status bar). The server reports it as `agentapi_version` in `GET /status` and the `status_change` SSE event
- Releases are cut automatically on every push to `main`: the version is bumped from Conventional Commits (breaking → major, `feat` → minor, otherwise patch), tagged `vX.Y.Z`, and stamped into the release binaries
- TTY mode: an escape hatch for when the chat view looks wrong. The terminal button in the chat header swaps the conversation for the agent's terminal screen (xterm.js) and sends every key straight to the agent, including arrows, Ctrl/Alt keys, multi-line paste (as one bracketed paste) and IME input. "Back to chat" returns to the chat view; the conversation is unaffected
- Codex: thinking shows in the chat. AgentAPI starts Codex with `-c model_reasoning_summary="auto"` so it logs readable reasoning summaries (otherwise its session log only holds encrypted reasoning), unless `model_reasoning_summary` is already set on the command line or in `$CODEX_HOME/config.toml`. Only applies when the program is the `codex` CLI itself, not a wrapper started with `--type codex`
- Explorer: Links, Files and Index list the newest task first; items keep their task numbers
- Chat header: the conversation JSONL download lives in the status badge menu ("Download conversation JSONL") instead of a separate task-toolbar button, and the status badge is the same size as the token badge next to it
- Codex: restarting the agent (Explorer → Restart, `DELETE /messages`, or an MCP change with restart) runs `codex update` before starting it again (only when the program is the `codex` CLI), so the restarted Codex is the latest release. A failed update (e.g. offline) still restarts the installed version. Claude Code updates itself in the background, so its restart already starts the latest version

### Removed
- `GET /usage` (upstream rate-limit utilization for Claude, Codex and Pi). The chat never used it, and its ChatGPT lookup no longer worked
- The sub-agents (Agents) panel, together with the `GET /agents` endpoint and the `agents_update` SSE event

### Fixes
- TTY mode on iPhone: the key bar sits above Safari's arrows-and-Done bar instead of under it. That bar sits on top of the keyboard but isn't counted in the space the keyboard takes
- Chat: the dark mode button stays in the header on phones instead of moving into the status menu
- Chat: the conversation is centered. The text column sat about 8rem left of center, past a time rail and with 17rem kept free on the right for thinking notes; now the 46rem column and the composer are centered, the time rail hangs in the left margin (shown inline when there is no room) and thinking notes use the right margin from 84rem up
- Chat: the Open Coder workspace button stays in the header on small screens instead of disappearing below 640px
- Chat: a task is marked Failed only when it ends on a failed command. A command the agent went on from (a missing directory, a grep with no match) used to mark the whole task Failed
- Codex code mode: commands run through Codex's `exec` tool show as `$ <command>` with their real output and exit code. They showed as the JavaScript snippet Codex runs (`text(await tools.exec_command({cmd:...}))`) with only a "Script completed / Wall time / Output:" header, the output itself cut off. `GET /rich-messages` and `GET /timeline` carry the command as `{"cmd", "script"}` and the output without the header
- Chat: multi-select questions from Claude Code (AskUserQuestion with `multiSelect`) can be answered in the decision card. Options show as checkboxes that toggle on click or with their number key, and Continue moves on to the next question or the review step. Previously each option was a one-shot button, so the answer could not be submitted from the chat. The question form's tab bar no longer shows as context, and an input line left on screen above a dialog no longer becomes its title
- Chat: a queued task no longer lingers as "queued" for up to 30 s after the server has sent it to the agent, which looked like a message that failed to send
- Chat: the Geist webfont never applied (its CSS variable was defined on <body> while Tailwind sets font-family on <html>), so the UI used the system font
- Chat accessibility: the composer's Task/Terminal tabs controlled panels that didn't exist, the attachment input had no label, the page had no level-one heading, and light-theme state colors were below 4.5:1 contrast
- Codex: reasoning that is only logged encrypted no longer shows as an "(encrypted)" thinking block; reasoning summaries are shown instead
- Chat: option buttons ("Select an option or switch to Terminal tab…") no longer appear under answers that merely contain a numbered list or words like "allow", "approve" or "sign in". The server now reports the prompt the agent is actually showing as `terminal_prompt` in `GET /status` and `status_change` (only when its input box is gone), and the chat shows a single option bar from it at the end of the conversation. A prompt is reported once it has been on screen for a second (Claude Code ignores keys sent the moment a dialog is drawn) and cleared as soon as it changes or goes away. Options come from the last numbered list in the prompt, and unnumbered options (Claude's folder trust dialog) move the cursor by the right number of rows
- Claude Code: agent replies are split into separate blocks again after a resumed session (`--continue`, `/resume`) or `/clear`. Claude Code can advertise a session id whose JSONL file is never written; the watcher waited for that file forever and never picked up the session actually in use, so the chat UI fell back to one undivided block of screen text
- Claude Code: rich messages work in working directories containing `.`, `_` or other non-alphanumeric characters (e.g. `~/.cache/app`, `cpt_sft_baseline`). Claude Code turns every such character into `-` in its project directory name; only `/` was converted. Session files are also found by id if the directory name still doesn't match
- Codex v0.158+: the full-screen "Update available" dialog shown at startup is skipped with Esc (this launch only). Until it was answered every message, including the initial prompt, stayed in the queue
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
