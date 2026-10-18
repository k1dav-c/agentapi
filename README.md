# AgentAPI

English | [繁體中文](README.zh-TW.md)

Control [Claude Code](https://github.com/anthropics/claude-code), [AmazonQ](https://aws.amazon.com/developer/learning/q-developer-cli/), [Opencode](https://opencode.ai/), [Goose](https://github.com/block/goose), [Aider](https://github.com/Aider-AI/aider), [Gemini](https://github.com/google-gemini/gemini-cli), [GitHub Copilot](https://github.com/github/copilot-cli), [Sourcegraph Amp](https://ampcode.com/), [Codex](https://github.com/openai/codex), [Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started), [Pi](https://pi.dev/), [Auggie](https://docs.augmentcode.com/cli/overview), and [Cursor CLI](https://cursor.com/en/cli) with an HTTP API.

![agentapi-chat](https://github.com/user-attachments/assets/57032c9f-4146-4b66-b219-09e38ab7690d)

You can use AgentAPI:

- to build a unified chat interface for coding agents
- as a backend in an MCP server that lets one agent control another coding agent
- to create a tool that submits pull request reviews to an agent
- and much more!

## About this fork

This fork started from the upstream v0.12.2 codebase. It keeps the original
terminal-emulation and HTTP API model as the default, adds an opt-in
[native transport](#native-transport) that drives Claude Code, Codex and Pi
through their own structured protocols, and extends AgentAPI into a more
complete workspace for operating long-running coding agents.
[CHANGELOG.md](CHANGELOG.md) lists what changed,
[docs/architecture.md](docs/architecture.md) describes how it is built, and
the [full comparison with upstream](https://github.com/coder/agentapi/compare/main...k1dav-c:agentapi:main)
has the complete commit history.

## Contents

- [Features at a glance](#features-at-a-glance)
- [Quickstart](#quickstart)
- [Agents and transports](#agents-and-transports)
- [Chat UI](#chat-ui)
- [Native transport](#native-transport)
- [PTY transport](#pty-transport)
- [Conversation API](#conversation-api)
- [Integrations](#integrations)
- [Security](#security)
- [CLI reference](#cli-reference)
- [HTTP API reference](#http-api-reference)
- [Architecture](#architecture)
- [Development](#development)
- [Runtime reliability](#runtime-reliability)
- [Roadmap](#roadmap) and [long-term vision](#long-term-vision)

## Features at a glance

**Drive coding agents over HTTP**

- Control 13 coding agents through their terminal UI with the
  [PTY transport](#pty-transport), which turns the screen into messages.
- Drive Claude Code, Codex and Pi through their own protocols with the
  [native transport](#native-transport); agents with an ACP server can use the
  experimental ACP transport (see [Transports](#transports)).
- Read structured activity (thinking, tool calls and their results, token
  usage) from `GET /rich-messages` and `GET /timeline`.
- Messages sent while the agent works wait in a [queue](#message-queue) that
  you can edit and reorder; with the native transport they can also
  [steer or interrupt](#steer-interrupt-and-the-queue) the running turn.
- Follow everything live over [server-sent events](#events-and-incremental-sync),
  with heartbeats and an incremental replay after a reconnect.

**Native agents**

- What the agent asks for (tool approvals, questions, plans) arrives as
  [requests](#requests-approvals-questions-and-plans), answered over HTTP or
  in the chat.
- Switch the [permission mode, model and reasoning effort](#settings-permission-mode-model-and-effort)
  while the agent runs: bypass (the default), auto, ask or plan.
- See how full the [context and the rate limits](#context-and-rate-limits)
  are, and [compact](#slash-commands-and-compaction) the context.
- A missing or expired [login](#logging-in) is reported with the command to
  run, and the agent starts by itself once it is logged in.
- A shared [terminal](#terminal) in the agent's working directory, for logins
  and quick commands.
- [Persistent sessions](#persistent-sessions): with `--state-db`, the
  conversation and the agent's own session survive an AgentAPI restart.

**Chat UI** (`/chat`)

- [Tasks](#tasks-and-the-transcript) read as a transcript, with tool calls as
  rows and thinking as margin notes; the [Session Explorer](#session-explorer)
  has links, files, a task index, export, MCP servers and Restart.
- [Decision cards](#decision-cards) for terminal dialogs and native requests,
  menus for the agent's settings, a ring for its context, and slash commands.
- A [file viewer](#file-viewer) with the file, its git diff and a preview of
  HTML, Markdown and images, [TTY mode](#tty-mode) and the native terminal,
  also on phones.
- [Background alerts](#alerts-connection-recovery-and-caching), browser
  notifications, a transcript cache, and recovery from dropped connections.

**Operations and integrations**

- [MCP management](#mcp-management) for Claude Code, Codex and Pi, with
  reusable profiles; restarting the agent updates Codex and Pi first.
- [Run-status webhooks](#run-status-webhooks) and a
  [human-reply API](#human-reply-api) let a bot tell you when the agent stops
  and pass your answer back.
- [Self-update](#agentapi-update), verified against the release's checksums.
- [Security](#security): an API token (the chat signs in with a cookie), the
  listen address, and the allowed hosts and origins.

## Quickstart

1. Install `agentapi`:

   ```bash
   OS=$(uname -s | tr "[:upper:]" "[:lower:]");
   ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/");
   curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" -o agentapi && chmod +x agentapi
   ```

   Alternatively, you can download this fork's latest binary from the
   [releases page](https://github.com/k1dav-c/agentapi/releases). Upstream
   `coder/agentapi` release binaries do not include this fork's features.

1. Verify the installation:

   ```bash
   agentapi --help
   ```

   > On macOS, if you're prompted that the system was unable to verify the binary, go to `System Settings -> Privacy & Security`, click "Open Anyway", and run the command again.

1. Run a Claude Code server (assumes `claude` is installed on your system and in the `PATH`):

   ```bash
   agentapi server -- claude
   ```

   > If you're getting an error that `claude` is not in the `PATH` but you can run it from your shell, try `which claude` to get the full path and use that instead.

   To drive Claude Code through its own protocol instead of its terminal UI,
   with approvals as structured requests, settings you can switch and a
   terminal in the chat, use the [native transport](#native-transport):

   ```bash
   agentapi server --transport native -- claude
   ```

1. Send a message to the agent:

   ```bash
   curl -X POST localhost:3284/message \
     -H "Content-Type: application/json" \
     -d '{"content": "Hello, agent!", "type": "user"}'
   ```

1. Get the conversation history:

   ```bash
   curl localhost:3284/messages
   ```

1. Try the chat web interface at http://localhost:3284/chat.

## Agents and transports

### Supported agents

The agent type decides how AgentAPI formats the agent's screen and which
features it offers. It comes from `--type`, or else from the agent command
when that is one of the type names (`agentapi server -- claude`, but not
`-- /usr/local/bin/claude`); anything else runs as `custom`.

| Agent | Type | PTY | Native | Structured activity | MCP management |
| --- | --- | :-: | :-: | :-: | :-: |
| [Claude Code](https://github.com/anthropics/claude-code) | `claude` | ✓ | ✓ | ✓ | ✓ |
| [Codex](https://github.com/openai/codex) | `codex` | ✓ | ✓ | ✓ | ✓ |
| [Pi](https://pi.dev/) | `pi` | ✓ | ✓ | ✓ | ✓ |
| [Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started) | `kimi` | ✓ | | | |
| [Goose](https://github.com/block/goose) | `goose` | ✓ | | | |
| [Aider](https://github.com/Aider-AI/aider) | `aider` | ✓ | | | |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | `gemini` | ✓ | | | |
| [GitHub Copilot](https://github.com/github/copilot-cli) | `copilot` | ✓ | | | |
| [Sourcegraph Amp](https://ampcode.com/) | `amp` | ✓ | | | |
| [Auggie](https://docs.augmentcode.com/cli/overview) | `auggie` | ✓ | | | |
| [Cursor CLI](https://cursor.com/en/cli) | `cursor` (`cursor-agent`) | ✓ | | | |
| [Amazon Q](https://aws.amazon.com/developer/learning/q-developer-cli/) | `q` (`amazonq`) | ✓ | | | |
| [Opencode](https://opencode.ai/) | `opencode` | ✓ | | | |
| Any other terminal program | `custom` | ✓ | | | |

- **Structured activity**: thinking, tool calls and token usage, read from
  the agent's session log on the PTY transport (see
  [Structured Claude, Codex and Pi activity](#structured-claude-codex-and-pi-activity))
  and from its protocol on the native transport.
- **MCP management**: the Explorer's MCP tab and the `/mcp` endpoints (see
  [MCP management](#mcp-management)).
- Agents with an ACP server, such as Kimi Code (`kimi acp`), can also use the
  experimental ACP transport.

> [!NOTE]
> When using Claude, Codex, Opencode, Copilot, Gemini, Amp or CursorCLI, always specify the agent type explicitly (eg: `agentapi server --type=codex -- codex`), or message formatting may break. Kimi and Pi are auto-detected when the executable name is `kimi` or `pi`; use `--type=kimi` / `--type=pi` for wrappers or ACP mode.

### Transports

`--transport` chooses how AgentAPI drives the agent:

| | PTY (default) | Native | ACP (experimental) |
| --- | --- | --- | --- |
| Flag | `--transport pty` | `--transport native` | `--transport acp` or `--experimental-acp` |
| Agents | all of the above | Claude Code, Codex, Pi | agents with an ACP server |
| How | runs the agent's terminal UI in a terminal emulator and reads its screen | speaks the agent's own protocol: `claude -p` stream-json, `codex app-server`, `pi --mode rpc` | speaks the Agent Client Protocol over the agent's stdio |
| Tool calls and thinking | from the session log (Claude Code, Codex, Pi) | streamed from the protocol | lines of text in the reply |
| Approvals, questions, plans | terminal dialogs, answered from decision cards or TTY mode | structured [requests](#requests-approvals-questions-and-plans) | approved automatically |
| Steer and interrupt a turn | | ✓ | |
| Settings, usage, compaction | | ✓ | |
| Survives an AgentAPI restart | `--state-file`: the messages | `--state-db`: the conversation and the agent's session | |
| Terminal in the chat | TTY mode: the agent's own terminal | a shell in the working directory | |

```bash
agentapi server -- claude
agentapi server --transport native --state-db ~/.agentapi/myproject.db -- codex
agentapi server --type=kimi --experimental-acp -- kimi acp
```

`--state-db <file>` (native transport only) keeps the conversation in a SQLite
file and resumes the agent's session when AgentAPI starts again with the same
file; see [Persistent sessions](#persistent-sessions). `--terminal` (default
true, native transport only) offers a shell in the agent's working directory;
see [Terminal](#terminal). `--state-file <file>` (PTY transport only) saves the
screen-derived messages on shutdown and loads them on startup (`--save-state`
and `--load-state` default to true when it is set).

## Chat UI

The chat UI at `/chat` is embedded in the binary and works with every
transport, on a desktop and on a phone. It has layouts for phones, searchable
tool activity, connection-state indicators and compact tool-call cards, and
its header shows the agent's mark (Claude's orange square, Pi's logo) and the
AgentAPI version.

### Tasks and the transcript

The embedded chat UI is organized around tasks instead of a single flat
transcript. Each user request becomes a navigable task with its associated
response, thinking blocks, tool calls, and background activity.

Tasks read as a transcript: a time rail, your prompt in a tinted block
labelled "You", the agent's turn opening with its mark and name (e.g. Pi), the
agent's reply as Markdown, tool calls as one-line rows (`$ command`, exit
code, duration, output preview), and thinking as margin notes on wide
screens. A status strip above the composer shows what the agent is doing
right now, and the header, tab title and queue say "Needs you" while the
agent waits for an answer. Long tasks show their latest 40 steps first.

Each message has its own markdown/raw toggle so you can switch render
modes without affecting the rest of the conversation.

### Session Explorer

The Session Explorer provides:

- links and file paths discovered in the current conversation;
- an index for jumping directly to earlier tasks;
- Markdown preview and export for individual tasks;
- access to the live terminal when the parsed conversation is not sufficient;
- MCP server configuration without leaving the chat UI;
- a Session tab with a two-step Restart (open the Explorer with ⌘K / Ctrl+K).

### Attachments and images

Images can be pasted straight into the composer (a pasted screenshot is
uploaded like an attachment; pasted text still pastes as text). Image
attachments and the images in sent messages show as thumbnails that open full
size; the agent gets the file path.

### Decision cards

What the agent waits for docks above the composer as a decision card: its
terminal dialogs on the PTY transport (see
[Interactive prompt support](#interactive-prompt-support)), its
[requests](#requests-approvals-questions-and-plans) on the native transport,
and the [login card](#logging-in) while it waits for a login. Number keys pick
options. On a phone a card takes at most 60% of the screen, and a title cut to
two lines can be shown in full.

### File viewer

File paths in the chat open the file the agent works on. The file name on a
tool call's row (Claude Code's Read, Write, Edit, MultiEdit and NotebookEdit;
Codex's apply_patch and view_image; Pi's read, write and edit), a path in
inline code in the agent's reply (`lib/foo.go:12` opens at line 12) and the
Explorer's Files tab open a viewer with two tabs: File, with line numbers and
syntax highlighting, and Diff, with the tool call's own change and the file's
uncommitted changes against HEAD (`GET /files/content`, `GET /files/diff`, cut
at 1 MiB). Only files inside the agent's working directory can be opened. An
expanded tool call lists its files and shows its change as a diff, with the
raw input a click away. On a phone the viewer is full screen.

HTML, Markdown and SVG files have a third tab, Preview, and an image file shows
as the image:

- An HTML page renders in an iframe, with the stylesheets, scripts, images,
  fonts and media it refers to by relative path inlined (`GET /files/preview`;
  up to 4 MiB each, 16 MiB in all; a note lists what was left out). The
  iframe is sandboxed without an origin of its own, so the page's scripts run
  but can't reach the chat, its session or the API; localStorage,
  sessionStorage and cookies are kept in memory while the preview is open.
  Script modules that import other files, and requests the page makes for
  relative URLs, don't load.
- A Markdown file opens on its preview: relative images load from the working
  directory, and relative links open their file in the viewer (`#L12` at its
  line).
- PNG, JPEG, GIF, WebP, BMP, ICO and SVG images show as images
  (`GET /files/image`).

### TTY mode

TTY mode is an escape hatch for when the chat view looks wrong: the terminal
button in the header swaps the conversation for the agent's terminal screen
(xterm.js) and sends every key straight to the agent, including arrows,
Ctrl/Alt keys, pastes and IME input. A key bar provides Esc, Tab, arrows,
Enter and Ctrl keys on phones. The font grows with the window (11–20px); the
mirror uses the agent's real terminal width, so start AgentAPI with
`--term-width 120` if you want longer lines.

### With the native transport

With `--transport native`, what the agent asks for (a tool approval, its
questions, a plan to approve) docks above the composer as a card, at most 60%
of a phone's screen, and while the agent works the send button can also steer
the message into the running turn or interrupt it. The composer's toolbar
switches the agent's permission mode, model and reasoning effort and shows how
full its context is and its rate limits; slash commands such as `/model` and
`/compact` work from the composer; and the terminal button opens a shell in
the agent's working directory instead of TTY mode, which needs the agent's own
terminal (the screen views stay hidden, also for ACP). See [Native
transport](#native-transport).

### Alerts, connection recovery and caching

While the tab is in the background, its icon shows an amber dot when the agent
needs you, a blue dot while it works, and a green dot when a task finished.
Browser notifications can be turned on from the status menu.

Long-running browser sessions are protected by SSE heartbeats, automatic
reconnection, stale-connection detection, and local recovery of messages that
failed before reaching the server. The document title and session header expose
the current task, agent status, and connection state.

Reopening a long conversation is fast: the chat caches the transcript in the
browser (IndexedDB), shows it immediately, and then asks the server only for
what changed since.

## Native transport

By default AgentAPI runs the agent in a terminal emulator and reads its screen
(the PTY transport). With `--transport native` it drives Claude Code, Codex
and Pi through their own structured protocols instead: Claude Code's
stream-json input and output with its control protocol, `codex app-server`
(JSON-RPC over stdio) and `pi --mode rpc`. Messages, rich messages (tool
calls, thinking, usage) and the timeline come straight from the protocol, so
there is no screen to parse and no session log to follow, and what the agent
asks for arrives as structured requests instead of terminal dialogs.

```bash
agentapi server --transport native -- claude --model sonnet
agentapi server --transport native -- codex -a on-request
agentapi server --transport native -- pi --provider openai-codex --model gpt-5.6-luna --thinking low
```

- The native transport is opt-in; PTY stays the default (`--transport pty`).
  `--transport acp` is the same as `--experimental-acp`, which can't be
  combined with `--transport native`.
- The agent type picks the adapter: `claude`, `codex` or `pi`, from the
  executable name or `--type`. Other agent types are refused at startup. To
  work on the chat without a real agent,
  `--transport native --type custom -- native-demo` runs a simulated one (see
  [chat/README.md](chat/README.md)).
- Text and thinking grow live: a rich message is replaced as a whole as it
  streams (at most ten snapshots a second per message, and the latest state
  within 100 ms when the stream pauses). For Claude Code, AgentAPI asks for
  summarized adaptive thinking; every Codex turn asks for detailed reasoning
  summaries; Pi's thinking streams as Pi writes it.
- `GET /status` reports `transport` (`native`), the agent's `capabilities`,
  `pending_requests`, its `settings` and `usage`, and `terminal`, and
  `status_change` events carry them too. While the agent waits only on a
  request, `status` is `stable`, as it is for a terminal dialog.
- The agent runs in its own process group, so the MCP servers and commands it
  started stop with it. Stopping it (AgentAPI exits, or a restart) first
  interrupts a running turn and waits up to 3 s for it to end, so the session
  log doesn't end in a tool call without result. Then it closes the agent's
  input so it can finish on its own, sends SIGTERM after 3 s and SIGKILL 2 s
  later.
- Started from inside another agent's session (for example by Claude Code's
  Bash tool), the agent would inherit the variables that session sets for the
  commands it runs, one of them a secret. AgentAPI removes them, whichever
  agent it runs and on every transport: Claude Code's (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`,
  `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`,
  `CLAUDE_CODE_SESSION_ATTENDED`, `CLAUDE_CODE_EXECPATH`,
  `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`,
  `CLAUDE_CODE_SSE_PORT`, `CLAUDE_PID`, `CLAUDE_EFFORT`,
  `CLAUDE_AGENT_SDK_VERSION`, `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS`,
  `CLAUDE_CODE_SDK_READS_SESSION_STATE`), Codex's (`CODEX_THREAD_ID`,
  `CODEX_SANDBOX`, `CODEX_SANDBOX_NETWORK_DISABLED`, `CODEX_CI`,
  `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`, `CODEX_TUI_RECORD_SESSION`,
  `CODEX_TUI_SESSION_LOG_PATH`), `PI_CODING_AGENT` and `AI_AGENT`, along
  with AgentAPI's own `AGENTAPI_API_TOKEN`. Configuration such as
  `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR` or `ANTHROPIC_*` is
  kept.

### Logging in

Native mode uses the agent CLI's own stored login. Log in with the CLI itself,
as the user account that runs AgentAPI: in the chat's
[terminal](#terminal) (the login card's **Run in terminal** runs the command
there, and so does `/login`), or in any terminal on that machine (or once in
PTY mode).

- Claude Code: `claude auth login` (Claude subscription by default, `--console`
  for Anthropic Console API billing). On a machine without a browser,
  `claude setup-token` creates a token for `CLAUDE_CODE_OAUTH_TOKEN`.
  `claude auth status` shows the login.
- Codex: `codex login` (ChatGPT plan), or
  `printenv OPENAI_API_KEY | codex login --with-api-key` for an API key.
  `codex login status` shows the login.
- Pi: `/login` inside `pi`, or a provider key such as `ANTHROPIC_API_KEY` in
  AgentAPI's environment.

AgentAPI checks the login when it starts the agent: Claude Code with
`claude auth status --json` (an API key, `CLAUDE_CODE_OAUTH_TOKEN`, an
`apiKeyHelper`, Bedrock and Vertex count as logged in; with `--settings` the
check is skipped, because `auth status` can't see an `apiKeyHelper` given
that way), Codex with
app-server's `account/read`, and Pi from its state (no model and no available
models). If the agent isn't logged in, AgentAPI keeps running instead of
exiting: `GET /status` and `status_change` report `login_required` (the agent,
what is wrong, the command to run, more help, and the host and user to run it
on and as), the chat docks a card with the command and a copy button above the
composer, and messages sent meanwhile wait in the queue. AgentAPI starts the
agent again every 5 seconds, so it notices the login by itself; **Check
again** in the chat (`POST /login/retry`) checks right away (a start that is
under way is that check). Warnings the agent prints while it starts are shown
once, not on every retry. When the check itself fails (an older CLI, a
wrapper program), the agent starts as before.

A login can also stop working while the agent runs (an expired token, a
revoked key). A turn that fails with a login error (`Not logged in`, an HTTP
401, `Invalid API key`, a refresh token that no longer works, Pi's
`No API key found` or, for an expired subscription login,
`Authentication failed for "<provider>"`) reports `login_required` the same
way. Log in, then press Check again (`POST /login/retry`): AgentAPI restarts
the agent, which resumes its session, and the conversation stays. A turn the
model completes clears it too (not an interrupted turn, nor a message the
agent handled itself).

### Requests: approvals, questions and plans

Tool approvals, questions (Claude's `AskUserQuestion`, Codex's
`request_user_input`, dialogs from Pi extensions) and plan approvals (Claude's
`ExitPlanMode`) arrive as requests:

- `GET /requests` lists the pending requests and the 50 most recently closed
  ones, and `GET /requests/{id}` returns one. `/events` sends a
  `request_update` event whenever one opens or closes, and `GET /status`
  counts the pending ones in `pending_requests`. Other transports return an
  empty list.
- `POST /requests/{id}/respond` answers one: `option_id` for approvals and
  plans (with optional `text` as feedback when the request has `feedback`),
  `answers` keyed by question ID for questions. A request is answered at most
  once: 409 when it is no longer pending (already answered, withdrawn or
  expired, also for a request restored from `--state-db`), 400 when the
  answer doesn't fit the request.
- Each request has a `status` (`pending`, `resolved`, `withdrawn`, `expired`)
  and a `response_capability`: `live` (the agent is waiting), `message` (the
  agent stopped waiting; answer with a new message) or `not_resumable` (it
  can't be answered any more: the agent that asked exited or was restarted,
  or stopped waiting for an approval or plan). When the agent stops waiting
  for a request (it withdraws it, or the turn ends), questions become
  `message` and approvals and plans `not_resumable`. An answer already on its
  way still counts if the agent takes it.
- Answers to secret questions are stored and reported as `[redacted]`; only the
  agent gets them.
- The [human-reply API](#human-reply-api) (`/handoff`) offers the oldest
  pending request with numbered choices: a number picks an option, other text
  answers a single question or goes with the deny option as feedback. A reply
  answers exactly the request that was offered; if that one was answered or
  withdrawn meanwhile, the reply is `superseded`, and one the request can't
  take is `invalid`.

In the chat, requests dock above the composer as a decision card. Approvals
show the command and options styled by what they do (allow, always allow,
deny), with optional feedback sent along with a denial; plans render as
Markdown and can be approved or sent back with feedback; questions take one
step each (single or multiple choice, typed or hidden answers) and are
submitted together. Number keys pick options, except while you type or while
a dialog, a menu or the terminal has the focus, and a held key doesn't repeat.
The tool call's row says whether it waits for approval, was approved or was
denied, and `AskUserQuestion`, `request_user_input` and `ExitPlanMode` calls
are answered on the card only. A request answered elsewhere or no longer
pending leaves the card, and a question the agent stopped waiting for (it
expired with the turn, or the agent withdrew it) points out that a normal
message can still answer it. The header, tab title, tab icon, the embed status
bar and notifications say "Needs you" while a request is pending.

### Steer, interrupt and the queue

- `POST /message` takes `dispatch_mode`: `queue` (the default, as on every
  transport: send the message once the agent is idle), `steer` (add it to the
  turn the agent is working on; the reply says `steered`) or `interrupt` (stop
  that turn and send the message next). They need the native transport. While
  a turn runs they also need the matching capability, otherwise the answer is
  400; when no turn is running (also while the agent restarts or waits for a
  login), both send or queue the message as usual.
- `POST /interrupt` stops the running turn (409 if there is none, or while no
  agent runs).
- `POST /queue/{id}/steer` moves a queued message into the running turn (409
  if no turn is running; the message stays queued).
- `PUT /queue` with `{"ids": [...]}` reorders the queue. It must list every
  queued message once (409 otherwise). This works on every transport.
- A queued message the agent refuses is tried again, up to 5 times, then
  dropped with an error that quotes it. One the agent didn't answer in time is
  dropped at once: it may have reached the agent, and sending it again could
  run the task twice.

`GET /status` reports `capabilities`: `steer`, `interrupt`, `resume` (the
session can be resumed when AgentAPI starts again, see
[Persistent sessions](#persistent-sessions)), `settings` (see
[Settings](#settings-permission-mode-model-and-effort)), `compact` (see
[Slash commands](#slash-commands-and-compaction)) and `images` (image attachments,
`@"<path>"` references to PNG, JPEG, GIF or WebP files up to 10 MB, are sent
to the agent as images as well as paths; to an agent without it, only the
paths are sent). While no agent runs (a restart, a wait for a login), it
reports what the last one supported.

In the chat, while the agent works, the send button's menu adds **Steer**
(⌘/Ctrl+Enter) and **Interrupt & send** (⌘/Ctrl+Shift+Enter) next to queueing
(Enter), and Stop interrupts the turn (`POST /interrupt`). Queued tasks can be
steered in right away, and moved up and down on every transport.

### Settings: permission mode, model and effort

The agent's permission mode, model and reasoning effort can change while it
runs. `GET /settings` returns them with the values they can take (the
permission modes the agent offers, its models with their efforts), and
`PUT /settings` changes the ones in its body, such as
`{"permission_mode": "ask"}` or `{"model": "sonnet", "effort": "high"}`; a
value a setting can't take is a 400. The current settings are also part of
`GET /status` and `status_change`.

The permission modes are **bypass** (tools run without asking; the default),
**auto** (the agent's own review decides what may run), **ask** (tool calls
that need approval come as requests) and **plan** (the agent proposes a plan,
which comes as a plan request, before it changes anything):

| Mode | Claude Code | Codex | Pi |
| --- | --- | --- | --- |
| bypass | `bypassPermissions` | no approvals, no sandbox | how Pi always runs |
| auto | `auto` | workspace sandbox, escalations decided by Codex's auto review | — |
| ask | `default` | `untrusted`: asks for every command it doesn't trust | — |
| plan | `plan` | plan collaboration mode | — |

A mode of the agent's own (Claude Code's `acceptEdits` after an approved
plan, a Codex policy from the command line) shows as no mode. Claude Code and
Pi apply a change at once; Codex takes it with its next turn, the only time
app-server does. The settings chosen are given again to the agent that
replaces this one: after a restart, a new login, and, with `--state-db`
(which stores them), after AgentAPI restarts. A setting the agent changes by
itself, such as the mode an approved plan switches to, is followed.

In the chat, the composer's toolbar has a menu for the mode and one for the
model and effort (on a phone in a row of their own above the buttons).

### Context and rate limits

`GET /status` and `status_change` report the agent's `usage`: how full its
context window is after the last turn (`context`: `used` and `size` in tokens)
and the account's rate limits (`rate_limits`: a label, how much is used, when
it resets and, for Claude Code, whether it is close to or at the limit).
Claude Code reports its 5-hour and weekly limits and its context after every
turn, Codex its context and its usage windows, and Pi its context. A new
session starts with an empty context; the rate limits, which are the
account's, stay. In the chat, a ring in the composer's toolbar shows how full
the context is, and its menu the limits with the time to each reset.

### Terminal

A native agent has no terminal of its own, so AgentAPI runs a shell in the
agent's working directory for the chat's terminal button: for a login, a git
command or anything else. It is a real terminal (colors, the cursor, full
screen programs), sized to the window, also on a phone, where a key bar adds
the keys an on-screen keyboard lacks. Everyone who opens it shares it, like a
tmux window, and it keeps running when nobody watches; once its shell exits,
the next open starts a new one. Its environment is the agent's, without
AgentAPI's own variables.

- `GET /terminal` streams its output as server-sent events (`output` events
  with base64 data, the first one the recent output to replay; `exit` when the
  shell exits) and starts the shell if it doesn't run.
- `POST /terminal/input` (`{"data": "..."}`) types into it and
  `POST /terminal/resize` (`{"cols": 120, "rows": 40}`) sizes it.
- It is on by default with the native transport; `--terminal=false` turns it
  off. It is as protected as the rest of the API, so use `--api-token` when
  the server is reachable by others.

### Slash commands and compaction

The agent's terminal UI isn't there to run its slash commands, so the chat
runs these itself for a native agent:

- `/model [model]`, `/effort [effort]` (`/think`) and `/mode [mode]`
  (`/permissions`) switch a setting by its id or name, or open its menu;
  `/plan` switches to the plan mode;
- `/compact` compacts the context;
- `/clear` (`/new`) starts a new conversation;
- `/terminal` opens the terminal, and `/login` runs the agent's login command
  in it;
- `/help` lists them.

While the command word is typed, the composer offers them; Tab completes the
first. Anything else is sent to the agent as typed, so the agent's own
commands (Claude Code's `/code-review`, Pi's extension commands) still work.
`POST /compact` compacts the context while the agent isn't working on a turn:
Codex (`thread/compact/start`) in a turn of its own, Pi (its `compact`
command) before it answers. Messages sent meanwhile wait in the queue, and
the agent's refusal (Pi's "Nothing to compact") is a 409 with its reason.
Claude Code compacts with the `/compact` message, which the chat sends it.

### Persistent sessions

`--state-db <file>` keeps the conversation in a SQLite file: messages, rich
messages (tool calls, thinking), the timeline, requests and the queue, plus
the agent's own session ID. When AgentAPI starts again with the same file, the
chat shows the conversation as it was, queued messages are still queued, and
the agent resumes its session, so it remembers the conversation too. After a
clean shutdown browser caches stay valid, because the conversation keeps its
epoch. Without `--state-db`, every start of AgentAPI is a new agent session.

```bash
agentapi server --transport native --state-db ~/.agentapi/myproject.db -- claude
```

- Requests that were open when AgentAPI stopped are marked expired: the agent
  that asked is gone.
- Uploads are kept next to the file (`<file>.uploads`), because messages refer
  to them by path.
- A file belongs to one agent type, transport and working directory; AgentAPI
  refuses to start with another one's file. It is locked while AgentAPI runs,
  so a second AgentAPI (or `sqlite3`) can't open it.
- A new file is created readable by its owner only (0600), and missing
  directories are created (0700).
- Restart (Explorer → Restart or `DELETE /messages`) clears the stored
  conversation and the queue and starts a new session. `?restart=true` on the
  MCP endpoints restarts the agent on its session instead, so the conversation
  stays. The settings chosen for the session are kept either way.
- Writes are batched every 200 ms, so streaming doesn't wait for the disk.
  After a crash the last of them may be missing, so the conversation gets a
  new epoch and clients load it again in full. Writes that keep failing are
  reported as an error.
- `--state-db` needs `--transport native`. With the PTY transport the agent
  can't resume its session; `--state-file` still saves the screen-derived
  messages there. The native transport doesn't take `--state-file`.

### Claude Code

AgentAPI runs `claude -p` headless with stream-json on stdin and stdout. Tool
calls carry their input, result, status and images, and each assistant message
has its model, stop reason and token usage.

The permission mode, model and effort switch with Claude Code's control
requests (`set_permission_mode`, `set_model`, `apply_flag_settings`); its
models and their efforts come from its initialize response, and the model and
effort it runs with from `get_settings`. AgentAPI adds
`--allow-dangerously-skip-permissions`, without which Claude Code ignores a
switch to `bypassPermissions`, except where Claude Code refuses that mode (as
root outside a sandbox). Approving a plan offers "Yes, and bypass permissions"
whenever the session can switch to it.

- Approvals offer Allow, Deny (with optional feedback for Claude) and, when
  Claude Code suggests a permission rule, "Allow always", which applies the
  rule to this session only. Commands Claude Code considers read-only run
  without asking.
- Questions list Claude's options; any other answer is accepted too.
- Plans show the plan and offer "Yes, and bypass permissions" (when the
  session can switch to it), "Yes, and auto-accept edits", "Yes, and manually
  approve edits" and "No, keep planning". Feedback sent with No makes Claude
  revise the plan and ask again.

Claude Code supports `steer` (a steered message joins the running turn at
Claude's next step), `interrupt` (if Claude Code doesn't end the interrupted
turn within 10 s, AgentAPI ends it), `resume` and `images`. The session name
is the title Claude Code generates after the first prompt; after `/clear` the
new session is titled too, and it is the one `--state-db` resumes. A turn
ends when Claude Code reports it is idle, so a background subagent and the
turn it wakes up when it finishes belong to the turn that started it; the
subagent's own steps are left out of the conversation, as in the session log,
and its tool call shows as running until it reports.

Command line and environment:

- AgentAPI adds `-p --output-format stream-json --input-format stream-json
  --verbose --include-partial-messages --replay-user-messages
  --permission-prompt-tool stdio`, and `--session-id` for a new session or
  `--resume` for a resumed one.
- Unless you choose otherwise it adds `--permission-mode bypassPermissions`,
  so tools run without asking; questions (AskUserQuestion) and plans
  (ExitPlanMode) still come as requests, and approving a plan offers to
  bypass permissions. Pass `--permission-mode default` (or `acceptEdits`,
  `plan`, …) to start in another mode, or switch it while Claude Code runs
  (see [Settings](#settings-permission-mode-model-and-effort)); AgentAPI also
  adds `--allow-dangerously-skip-permissions`, so a session can switch to
  bypass later. Claude Code refuses `bypassPermissions` as root outside a
  sandbox, so when AgentAPI runs as root it adds `--permission-mode default`
  instead, leaves out `--allow-dangerously-skip-permissions` and logs a
  warning; set `IS_SANDBOX=1` in a sandbox, or choose a mode yourself
  (`--allow-dangerously-skip-permissions` alone still makes Claude Code exit
  as root). It also adds `--thinking adaptive --thinking-display summarized`
  (without it thinking is empty). Your `--permission-mode`,
  `--dangerously-skip-permissions`, `--thinking`, `--max-thinking-tokens` or
  `--thinking-display` wins.
- Flags that conflict with the protocol or the session AgentAPI manages are
  rejected at startup: `-p`/`--print`, `--output-format`, `--input-format`,
  `-r`/`--resume`, `-c`/`--continue`, `--session-id`, `--fork-session`,
  `--from-pr`, `--teleport`, `--permission-prompt-tool`,
  `--permission-prompts`, `--bg`/`--background`, `--cloud`/`--remote`,
  `--desktop`, `--remote-control`/`--rc`, `--sdk-url`, `--resume-session-at`
  and `--resume-drops-turn`, also inside a group of short flags such as
  `-pc`. Your own copies of `--verbose`,
  `--include-partial-messages` and `--replay-user-messages` are dropped.
  Everything else (`--model`, `--allowedTools`, `--mcp-config`,
  `--append-system-prompt`, ...) is passed through.
- Besides removing a parent agent session's variables (see
  [Native transport](#native-transport)), AgentAPI sets
  `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and
  `CLAUDE_CODE_ENTRYPOINT=sdk-cli`.

Resuming a session keeps Claude's context; Claude Code doesn't replay the
earlier messages, so the chat shows them only from the `--state-db` file. A
session can be resumed once it had a turn; with `--no-session-persistence` it
can't, and `resume` is false. Requests still open when Claude Code exits or
restarts can't be answered anymore. Slash commands that need the interactive
UI don't work.

### Codex

AgentAPI starts `codex app-server` and starts or resumes a thread. Messages,
rich messages and the timeline have the same shape the session-log parser
produces, so the chat shows Codex's replies, reasoning summaries, commands (the
command itself, not the `bash -lc` wrapper), file edits, MCP tool calls, plan
updates and token usage as they stream. Every turn asks for detailed reasoning
summaries unless you choose `model_reasoning_summary` with `-c`; whether the
model writes any depends on the model and the reasoning effort.

Codex's command line after `codex` is handled like this:

- `-c`/`--config`, `--enable`, `--disable`, `--code-mode-host`,
  `--strict-config` and `--analytics-default-enabled` are passed to
  `codex app-server`.
- `-m`/`--model`, `-s`/`--sandbox`, `-a`/`--ask-for-approval`
  (`untrusted`, `on-request`, `never`) and `-C`/`--cd` become the thread's
  settings; `--dangerously-bypass-approvals-and-sandbox` means `-a never -s
  danger-full-access`. Without them (or `-c approval_policy=...`,
  `sandbox_mode`, `approvals_reviewer`), the thread runs without approvals or
  sandbox, the native transport's bypass mode; switch it from the chat or with
  `PUT /settings` (see [Settings](#settings-permission-mode-model-and-effort)).
  Codex 0.160 refuses `approval_policy = "untrusted"` in its config, so use
  `-a untrusted` for that policy.
- `--no-alt-screen`, `--no-daemon` and `--stdio` are ignored.
- Short flags take attached values as Codex does (`-mo3`, `-m=o3`); a value
  on a switch (`--dangerously-bypass-approvals-and-sandbox=false`) is
  rejected.
- A prompt, a subcommand (other than `app-server`), `-p`/`--profile`,
  `-i`/`--image`, `--oss`, `--local-provider`, `--search`, `--add-dir`,
  `--approve-for-me`, `--dangerously-bypass-hook-trust`, `--remote`,
  `--remote-auth-token-env`, `--worktree`, `--listen`, `--help`, `--version`
  and unknown flags are rejected at startup with an explanation. Use
  `--initial-prompt` for a first message and attach images to messages
  instead.

Codex supports `steer`, `interrupt`, `resume` and `images`. Command, file-edit
and MCP tool approvals, permission requests, MCP form elicitations and
`request_user_input` questions arrive as requests (Codex asks those questions
in the plan mode; with codex-cli 0.160,
`--enable default_mode_request_user_input` lets Codex ask them in the default
mode too). The plan mode is Codex's plan collaboration mode, an experimental
part of app-server's API that AgentAPI opts into: Codex ends its turn with a
plan, and AgentAPI asks "Implement this plan?" as the Codex TUI does. The
Yes options (`implement_bypass`, `implement_auto` and `implement_ask`, the
mode before the plan first) leave the plan mode for that permission mode and
have Codex implement the plan in the same turn; No (`keep_planning`) with
feedback sends the feedback back to Codex, still planning; No alone ends the
turn. The other requests' option IDs are Codex's own decisions (`accept`,
`acceptForSession`, `acceptWithExecpolicyAmendment`, `decline`, `cancel`, …),
nothing is approved automatically, and requests AgentAPI can't show are
declined. A command approval shows the command that runs, without the shell
wrapper; Codex's reading of it (files it reads, searches) is in the detail.
Requests still open when a turn ends are declined (MCP elicitations
cancelled), so neither Codex nor an MCP server keeps waiting. A steered message
joins the running turn at Codex's next model request (the current output keeps
streaming); the reply to it is filed under the steering message. An interrupt
ends the turn within milliseconds, and tool calls it cut short show as failed.

The session reference is Codex's thread ID. Resuming it continues the thread
with its context; a turn that was running when the process died comes back as
interrupted, approvals that were pending are gone, and Codex's earlier turns
are not loaded into AgentAPI's conversation (the chat shows them only from the
`--state-db` file). A thread that can't be resumed is replaced by a new one,
with a warning. Codex doesn't name threads itself; a name set elsewhere
(`thread/name/updated`) becomes the session name. MCP servers managed from the
Explorer live in `$CODEX_HOME/config.toml`, which app-server reads when it
starts; saving them with restart restarts Codex on its thread.

Codex's own slash commands are TUI features; the chat runs `/model`,
`/compact` and the others in [Slash commands](#slash-commands-and-compaction)
itself, and `/review` isn't available. Command output streams as a preview
that Codex may leave incomplete, and the full output replaces it when the
command ends.

### Pi

AgentAPI runs Pi in its RPC mode (`pi --mode rpc`, JSON over stdin and
stdout).

- **Flags.** AgentAPI adds `--mode rpc --session-id <id>` (a new
  `agentapi-<hex>` ID for every new session, including after a restart). It
  refuses options that pick the mode or the session or make Pi exit (`--mode`,
  `--print`/`-p`, `--session`, `--session-id`, `--continue`/`-c`,
  `--resume`/`-r`, `--fork`, `--no-session`, `--export`, `--list-models`,
  `--help`/`-h`, `--version`/`-v`), `@file` arguments, messages (after `--` or
  as bare arguments, which RPC mode would ignore) and subcommands
  (`pi install`, `remove`, `uninstall`, `update`, `list`, `config`, `auth`,
  `mcp`), with an error that names the option. It drops the TUI-only
  `--tui-mode` and `--use-theme` with their values. Everything else is passed on
  (`--provider`, `--model`, `--thinking`, `--session-dir`, `-e`, `--approve`,
  extension flags); `--flag=value` is split for Pi's built-in options, which
  Pi only reads in the two-token form. Set the thinking level with
  `--thinking`.
- **Capabilities.** Steer (`dispatch_mode: "steer"` is sent as a prompt with
  `streamingBehavior: "steer"`), interrupt (`POST /interrupt` sends `abort`; the
  turn ends as interrupted, without the "This operation was aborted" error Pi
  adds when it aborts during a tool, and if Pi doesn't end the turn within 10
  seconds AgentAPI ends it), resume, and images (attached images go to Pi as
  base64 `images`; reported unless Pi says its model doesn't take images).
- **Messages.** Text and thinking stream into the rich messages as they arrive,
  bash output streams into the tool result, and the final messages have the
  same shape as the ones parsed from Pi's session log. Images in tool results
  (an image file Pi reads) are kept.
- **Requests.** Stock Pi has no tool approvals. Dialogs that extensions open
  (`select`, `confirm`, `input`, `editor`) become requests: a `select` or
  `confirm` while a tool runs is an approval of that tool call (option IDs are
  the extension's labels), any `confirm` takes `yes`/`no`, and other dialogs
  are questions (`choice` or `value`; option `cancel` dismisses them). A dialog
  Pi times out is withdrawn. Dialogs still open when the turn ends expire with
  it, like other agents' requests (a question can then be answered with a
  message), and the extension gets a cancel; interrupting an extension command
  that waits on a dialog withdraws the dialog. An extension command that runs
  without a model turn ends with the stop reason `handled`. An extension's
  `notify` with level error shows as
  an error, and its `setTitle` stands in for the session name until Pi has one;
  Pi's own name (`--name`, renames) is the session name.
- **Resume.** Pi's session ID is the session reference. Resuming starts Pi with
  the same `--session-id` (keep the same directory and `--session-dir`), so Pi
  keeps the context; Pi doesn't replay earlier messages, so the chat shows them
  only from the `--state-db` file.
- **Shutdown.** Stopping AgentAPI (or a restart) aborts a running turn first,
  so the session log doesn't end in a tool call without result, then stops Pi
  as described in [Native transport](#native-transport).
- **Settings.** Pi has no permission modes. Its model (`set_model`, from the
  models it has a provider for) and thinking level (`set_thinking_level`,
  with the levels each model supports) can change; Pi fits the level to a new
  model by itself.
- **Commands.** Pi's TUI commands (`/settings`, `/login` and the like) are
  unavailable; the chat runs `/model`, `/compact` and the others in
  [Slash commands](#slash-commands-and-compaction), and extension slash
  commands work. Log in from the chat's terminal (`pi`, then `/login`).

### Limitations

- The agent has no terminal of its own. The chat hides TTY mode, the terminal
  prompt card, the live screen tail and "Terminal output" (for ACP too, where
  Stop and the answer buttons on tool calls aren't offered either), `raw`
  messages (keys for the terminal) are refused with 400, and the agent's slash
  commands that need its interactive UI don't work, apart from the ones the
  chat runs itself. The [terminal](#terminal) is a separate shell.
- Restart (Explorer → Restart or `DELETE /messages`) replaces the agent with a
  new one in a new session and clears the conversation and the queue; requests
  still open become `not_resumable`. The `--initial-prompt` is sent to the new
  session again, and Codex and Pi are updated first, as with PTY.
- Without `--state-db`, the next start of AgentAPI is a new session; the
  agent's earlier session isn't resumed.
- Images reach the agent only as `@"<path>"` attachments of a message, not as
  command-line flags (`codex -i` and Pi's `@file` are refused).

## PTY transport

The default transport works with every agent that has a terminal UI. For
Claude Code, Codex and Pi it also follows their session logs for structured
activity, and the chat answers their terminal dialogs.

### How it works

AgentAPI runs an in-memory terminal emulator. It translates API calls into appropriate terminal keystrokes and parses the agent's outputs into individual messages.

#### Splitting terminal output into messages

There are 2 types of messages:

- User messages: sent by the user to the agent
- Agent messages: sent by the agent to the user

To parse individual messages from the terminal output, we take the following steps:

1. The initial terminal output, before any user messages are sent, is treated as the agent's first message.
2. When the user sends a message through the API, a snapshot of the terminal is taken before any keystrokes are sent.
3. The user message is then submitted to the agent. From this point on, any time the terminal output changes, a new snapshot is taken. It's diffed against the initial snapshot, and any new text that appears below the initial content is treated as the agent's next message.
4. If the terminal output changes again before a new user message is sent, the agent message is updated.

This lets us split the terminal output into a sequence of messages.

#### Removing TUI elements from agent messages

Each agent message contains some extra bits that aren't useful to the end user:

- The user's input at the beginning of the message. Coding agents often echo the input back to the user to make it visible in the terminal.
- An input box at the end of the message. This is where the user usually types their input.

AgentAPI automatically removes these.

- For user input, we strip the lines that contain the text from the user's last message.
- For the input box, we look for lines at the end of the message that contain common TUI elements, like `>` or `------`.

#### What will happen when Claude Code, Goose, Aider, or Codex update their TUI?

Splitting the terminal output into a sequence of messages should still work, since it doesn't depend on the TUI structure. The logic for removing extra bits may need to be updated to account for new elements. AgentAPI will still be usable, but some extra TUI elements may become visible in the agent messages.

### Structured Claude, Codex and Pi activity

In addition to parsing terminal snapshots, this fork can watch Claude, Codex
and Pi session logs. This provides structured thinking blocks, tool invocations, tool
results, usage information, and stable message identifiers that cannot always
be reconstructed reliably from terminal output alone.

- `GET /rich-messages` returns the structured conversation.
- `GET /timeline` returns normalized session events suitable for export,
  auditing, or building another UI.
- Background and delegated tasks remain visible with their running, completed,
  or failed state and output details.
- Claude thinking blocks are rendered inline as collapsible sections in the
  task timeline.
- Codex is started with `-c model_reasoning_summary="auto"` (unless you set it
  yourself) so its log holds readable reasoning summaries instead of only
  encrypted reasoning.

Terminal parsing remains the fallback for other agents and for environments
where a session log is unavailable. With the
[native transport](#native-transport), the same endpoints are filled from the
agent's protocol instead of its session log.

### Interactive prompt support

The chat UI detects interactive TUI prompts — such as Claude Code's plan
approval dialog or permission confirmation, Codex's approvals, and Pi's project
trust prompt — and docks them above the composer as a decision card. Number
keys pick an option. Multi-select questions (Claude Code's AskUserQuestion with
`multiSelect`) show checkboxes and a Continue button that moves on to the next
question or the review step. A numbered list in the agent's answer is not
mistaken for options. `GET /status` reports the prompt as `terminal_prompt`.
With the [native transport](#native-transport) there are no terminal prompts;
approvals, questions and plans arrive as structured requests instead.

### Kimi Code CLI

This fork adds the `kimi` agent type, automatic detection for the `kimi`
executable, chat UI labeling, terminal message formatting, readiness detection,
and tests for its startup state.

Kimi can use the regular interactive PTY transport:

```bash
agentapi server -- kimi
```

It can also use Kimi's native ACP server after completing `/login` once:

```bash
agentapi server --type=kimi --experimental-acp -- kimi acp
```

### Pi coding agent

The `pi` agent type runs the [Pi coding agent](https://pi.dev/)
(`npm install -g @earendil-works/pi-coding-agent`) in its interactive terminal
UI. It is auto-detected when the executable name is `pi`:

```bash
agentapi server -- pi
```

Log in once with `/login` in Pi (TTY mode in the chat UI works), or set a
provider key such as `ANTHROPIC_API_KEY`. AgentAPI follows Pi's session log
(`~/.pi/agent/sessions/`, or `$PI_CODING_AGENT_DIR` /
`$PI_CODING_AGENT_SESSION_DIR`) for structured messages, so the chat shows
Pi's tool calls, thinking and token usage like it does for Claude Code and
Codex.

Pi's dialogs (such as the project trust prompt) can be answered from the chat,
and the header and tab icon show Pi's logo. The Explorer's MCP tab manages
Pi's user-level servers in `~/.pi/agent/mcp.json`, and restarting the agent
runs `pi update --self` first.

AgentAPI starts Pi with its own `--session-id` (a new one on every start or
restart) so it follows the right session log even when several Pi processes
share a directory. If you pass `--session`, `--continue`, `--resume` or
`--session-id` yourself, AgentAPI leaves the session to you; with
`--continue`/`--resume` the chat falls back to the terminal output.

To drive Pi through its RPC mode instead of its terminal UI, see
[Native transport](#native-transport).

## Conversation API

These endpoints work with every transport; the
[HTTP API reference](#http-api-reference) lists them all.

### Messages and status

- `POST /message` sends a message: `{"content": "Hello, agent!", "type": "user"}`.
  A 200 means AgentAPI saw the agent start on it, or queued it (`queued`)
  while the agent is busy; `dispatch_mode` chooses what happens while it is
  busy (see [Steer, interrupt and the queue](#steer-interrupt-and-the-queue)).
  `"type": "raw"` types keys into the agent's terminal instead (PTY
  transport).
- `GET /messages` returns the conversation; `GET /rich-messages` and
  `GET /timeline` return its structured form (see
  [Structured Claude, Codex and Pi activity](#structured-claude-codex-and-pi-activity)).
- `DELETE /messages` clears the conversation (messages, rich messages,
  requests, timeline, errors and the queue) and restarts the agent (with the
  native transport, in a new session).
- `GET /status` returns the backward-compatible `stable`/`running` status,
  the detailed lifecycle (`starting`, `ready`, `running`, `restarting`,
  `exited` or `failed`), the transport, a session ID, a monotonically
  increasing run ID, the conversation's `epoch`, the session name, the
  AgentAPI version, the agent's open dialog (`terminal_prompt`) and the
  terminal width (`terminal_columns`). With the native transport it also
  reports `capabilities`, `pending_requests`, `settings`, `usage`, `terminal`
  and `login_required`.
- `GET /title` returns the session's human-readable title.
- `POST /upload` stores a file (up to 10 MB) and returns its path, which a
  message references as `@"<path>"`.

### Message queue

Messages submitted while an agent is busy are placed in a FIFO queue instead
of being rejected. Queued messages can be inspected, edited, or deleted before
delivery through both the API and chat UI.

- `GET /queue` lists the queued messages.
- `PUT /queue/{id}` edits a queued message and `DELETE /queue/{id}` removes
  it.
- `PUT /queue` with `{"ids": [...]}` reorders the queue; it must list every
  queued message once.
- `POST /queue/{id}/steer` moves a queued message into the turn the agent is
  working on (native transport).

### Events and incremental sync

`GET /events` streams server-sent events: `message_update`, `status_change`,
`rich_message_update`, `agent_error`, `request_update` (native transport) and
a `heartbeat` every 15 seconds, so a client can tell a dead connection. With
`?sync=1` the stream starts with a `session_sync` event and every update
carries a `seq`; reconnecting with `&since=<seq>&epoch=<epoch>` replays only
what changed.

With `?sync=1`, events also carry SSE ids (the end of the replay has the
replay's `seq`, later changes their own), so a plain `EventSource` that
reconnects by itself resumes from its `Last-Event-ID`; put the `epoch` from
`GET /status` in the URL. When a message is taken back (a turn the agent
refused to start), the conversation gets a new epoch and connected clients
receive a `session_sync` with `full: true` followed by the whole state.

## Integrations

### Run-status webhooks

AgentAPI can send an HTTP POST whenever a run changes between `running` and
`stable`. Webhooks can be initialized with CLI flags or `AGENTAPI_WEBHOOK_*`
environment variables, then inspected or changed through `GET/PUT /webhook`.

Delivery runs asynchronously with a configurable timeout and retry count.
An optional Go `text/template` payload template lets you reshape the POST body
for any receiver — available fields are `.ID`, `.Type`, `.CreatedAt`, `.RunID`,
`.Status`, `.PreviousStatus`, `.AgentType`, and `.Transport`. When no template
is set, the default JSON payload is sent unchanged.

```bash
agentapi server \
  --webhook-url https://example.com/agentapi/events \
  -- claude
```

The equivalent environment variables are `AGENTAPI_WEBHOOK_URL`,
`AGENTAPI_WEBHOOK_PAYLOAD_TEMPLATE`, `AGENTAPI_WEBHOOK_TIMEOUT`, and
`AGENTAPI_WEBHOOK_MAX_ATTEMPTS`. The timeout defaults to `10s`, and delivery is
attempted up to 3 times.

The request body has this format:

```json
{
  "id": "unique-delivery-id",
  "type": "run.status_changed",
  "created_at": "2026-07-26T12:00:00Z",
  "data": {
    "run_id": "agentapi-process-run-id",
    "status": "stable",
    "previous_status": "running",
    "agent_type": "claude",
    "transport": "pty"
  }
}
```

Each request includes `X-AgentAPI-Delivery`, `X-AgentAPI-Event`, and
`X-AgentAPI-Timestamp` headers.

### Human-reply API

Another client, such as a chat bot, can answer the agent without the web UI.
While the agent waits for you, `GET /handoff` returns what it waits on, with an
`id` and a `kind`: its latest response (`message`), the terminal prompt it is
showing (`terminal`) or, with the native transport, the oldest pending request
(`request`). Otherwise it returns `{"request": null}`.

`POST /handoff/reply` with that `request_id`, a unique reply `id` and the
`content` answers it. A reply to a response is sent as a normal user message,
and a reply to a request answers it as described under
[Requests](#requests-approvals-questions-and-plans). A terminal prompt takes a
displayed option number, `enter` or `esc`, or free text where the prompt has a
text field (such as Claude Code's plan feedback). The `outcome` is `applied`,
`superseded` (no longer open, for example because it was answered in the web
UI), `invalid` (the reply doesn't fit; it stays open) or `uncertain`, and
nothing is answered twice. Combined with the run-status webhooks above, an
integration can tell you when the agent stops and pass your answer back.

### MCP management

Claude, Codex and Pi MCP servers can be managed while AgentAPI is running. The
implementation preserves unrelated settings in `.mcp.json` (Claude),
`$CODEX_HOME/config.toml` (Codex) or `~/.pi/agent/mcp.json` (Pi).

The API and Session Explorer support:

- reading or replacing the complete MCP server map;
- creating, updating, and deleting individual servers;
- checking remote HTTP connectivity and resolving local stdio executables;
- saving, importing, exporting, and applying reusable MCP profiles;
- optionally restarting the agent to apply changes immediately.

When an agent is restarted, AgentAPI itself and its HTTP/SSE clients remain
online. A PTY agent receives a new process and session-log watcher, although
its previous in-memory conversation context is not retained. A native agent
restarts on its session, so the conversation stays (a 409 while it works on a
turn, which a restart would stop). Before a restart from the Explorer or
`DELETE /messages`, AgentAPI updates Codex (`codex update`) and Pi
(`pi update --self`), so the restarted agent is the latest release; Claude
Code updates itself.

## Security

AgentAPI has no users or roles: whoever can reach the API can do what the
agent can. Out of the box it takes no token and accepts any connection whose
`Host` header names the machine itself (`localhost`, `127.0.0.1`, `[::1]`),
on every network interface. The host check keeps web pages from calling it
through DNS rebinding; it doesn't stop a program on the network, which can
send any `Host` header. Protect the server before anyone else can reach the
port.

### API token authentication

All API endpoints can be protected with a Bearer token. Authentication is
**disabled by default** for backward compatibility.

```bash
# Auto-generate a random token (printed to stderr on startup)
agentapi server --api-token -- claude

# Use a specific token
agentapi server --api-token=my-secret -- claude

# Via environment variable
AGENTAPI_API_TOKEN=my-secret agentapi server -- claude
```

When enabled, every API request must include `Authorization: Bearer <token>`
or the chat UI's session cookie. Static file routes (`/`, `/chat/*`) are exempt
so browsers can open the chat UI without a token. Tokens are compared in
constant time.

The chat UI signs in by trading the token for a cookie, since browsers can't add
an `Authorization` header to the event stream or to images:

- Open `http://localhost:3284/chat/embed#token=<token>`. With an auto-generated
  token the server prints this link on startup. The token is in the URL
  fragment, which the browser never sends to the server, a proxy or an access
  log; the page exchanges it and removes it from the address bar. A link to a
  server without `--api-token` just opens the chat.
- Or just open the chat: when the server answers 401 it asks for the token.
  The dialog names the server that asks and warns when it isn't the page's
  own origin, and says so when the browser didn't keep the session cookie.
  After Cancel, **Reconnect now** asks again.

`POST /auth/session` with `Authorization: Bearer <token>` sets the cookie
(`agentapi_session_<hash>`, HttpOnly, SameSite=Strict, `Path=/`, and Secure
over HTTPS or behind a proxy that sends `X-Forwarded-Proto: https`). It holds
an HMAC of the token, not the token, so changing the token signs every browser
out, and the name comes from it, so servers with different tokens on one host
keep their own sign-ins. `DELETE /auth/session` signs out, and
`GET /auth/session` (no credentials needed) reports `auth_required` and
`authenticated`. Because the cookie is SameSite=Strict, pages on other sites
can't make requests with it. A chat UI served from another origin (e.g.
`bun run dev` on port 3000) needs that origin in `--allowed-origins`;
`--allowed-origins '*'` doesn't allow requests with cookies. Ports don't make
another site, and the default allowed origins include
`http://localhost:3000` and `http://localhost:3001` for chat development: on
a shared machine, set `--allowed-origins` to the origins you use, so a signed-in
browser's cookie only works for them.

### Bind address

By default, the server listens on all network interfaces. Use `--bind` (or
`AGENTAPI_BIND`) to listen on one address only, e.g. loopback so nothing else
on the network can connect. It takes an IP address or hostname without a port;
the port stays `--port`.

```bash
agentapi server --bind 127.0.0.1 -- claude
agentapi server --bind ::1 -- claude          # IPv6 loopback
AGENTAPI_BIND=127.0.0.1 agentapi server -- claude
```

`--bind` decides who can connect; `--allowed-hosts` below decides which `Host`
headers are accepted from those who can.

### Allowed hosts

By default, the server only allows requests with the host header set to `localhost`, `127.0.0.1` or `[::1]`. If you'd like to host AgentAPI elsewhere, you can change this by using the `AGENTAPI_ALLOWED_HOSTS` environment variable or the `--allowed-hosts` flag. Hosts must be hostnames only (no ports); the server ignores the port portion of incoming requests when authorizing. This checks the `Host` header only; to keep the server off the network altogether, also listen on loopback with `--bind 127.0.0.1` (see [Bind address](#bind-address)).

To allow requests from any host, use `*` as the allowed host.

```bash
agentapi server --allowed-hosts '*' -- claude
```

To allow a specific host, use:

```bash
agentapi server --allowed-hosts 'example.com' -- claude
```

To specify multiple hosts, use a comma-separated list when using the `--allowed-hosts` flag, or a space-separated list when using the `AGENTAPI_ALLOWED_HOSTS` environment variable.

```bash
agentapi server --allowed-hosts 'example.com,example.org' -- claude
# or
AGENTAPI_ALLOWED_HOSTS='example.com example.org' agentapi server -- claude
```

### Allowed origins

By default, the server allows CORS requests from `http://localhost:3284`, `http://localhost:3000`, and `http://localhost:3001`. If you'd like to change which origins can make cross-origin requests to AgentAPI, you can change this by using the `AGENTAPI_ALLOWED_ORIGINS` environment variable or the `--allowed-origins` flag.

To allow requests from any origin, use `*` as the allowed origin:

```bash
agentapi server --allowed-origins '*' -- claude
```

To allow a specific origin, use:

```bash
agentapi server --allowed-origins 'https://example.com' -- claude
```

To specify multiple origins, use a comma-separated list when using the `--allowed-origins` flag, or a space-separated list when using the `AGENTAPI_ALLOWED_ORIGINS` environment variable. Origins must include the protocol (`http://` or `https://`) and support wildcards (e.g., `https://*.example.com`):

```bash
agentapi server --allowed-origins 'https://example.com,http://localhost:3000' -- claude
# or
AGENTAPI_ALLOWED_ORIGINS='https://example.com http://localhost:3000' agentapi server -- claude
```

### What an API client can do

- Have the agent run anything it can. Native agents start in the bypass
  permission mode, where tools run without asking; Claude Code gets
  `--allow-dangerously-skip-permissions` (except as root), so a client can
  switch any session to bypass; Codex's default overrides the policies in
  `~/.codex/config.toml` unless the command line picks one; and the ACP
  transport approves every permission request.
- Run commands in the [terminal](#terminal) (native transport, on by default;
  `--terminal=false` turns it off).
- Read files in the agent's working directory with the file viewer endpoints;
  paths outside it, also through symlinks, are refused, and the diff only
  reads the git repository. An HTML preview's scripts run in a sandbox
  without access to the chat or the API.
- Upload files of up to 10 MB; only images are served back.
- Change the MCP servers and the webhook, and restart the agent.

Use `--api-token` together with `--bind` or a reverse proxy whenever someone
else can reach the server.

## CLI reference

### `agentapi server`

Run an HTTP server that lets you control an agent. If you'd like to start an agent with additional arguments, pass the full agent command after the `--` flag.

```bash
agentapi server -- claude --allowedTools "Bash(git*) Edit Replace"
```

You may also use `agentapi` to run the Aider and Goose agents:

```bash
agentapi server -- aider --model sonnet --api-key anthropic=sk-ant-apio3-XXX
agentapi server -- goose
```

Pi runs through its interactive terminal UI (see [Pi coding agent](#pi-coding-agent)):

```bash
agentapi server -- pi
```

Kimi Code can run through its interactive terminal UI:

```bash
agentapi server -- kimi
```

Kimi Code also provides a native ACP server. After logging in once with
`kimi` and `/login`, you can use AgentAPI's ACP transport:

```bash
agentapi server --type=kimi --experimental-acp -- kimi acp
```

#### Flags

Every flag can also be set with an environment variable: `AGENTAPI_` and the
flag's name in capitals, with `_` for `-` (`AGENTAPI_ALLOWED_HOSTS`). Lists
are comma-separated in a flag and space-separated in a variable.

| Flag | Default | Description |
| --- | --- | --- |
| `-t`, `--type` | from the agent command | Agent type; see [Supported agents](#supported-agents) |
| `-p`, `--port` | `3284` | Port to listen on |
| `--bind` | all interfaces | Address to listen on; see [Bind address](#bind-address) |
| `--transport` | `pty` | `pty`, `native` or `acp`; see [Transports](#transports) |
| `--experimental-acp` | `false` | The same as `--transport acp` |
| `--state-db` | | SQLite file that keeps the conversation and resumes the agent's session (native); see [Persistent sessions](#persistent-sessions) |
| `--terminal` | `true` | Shell in the agent's working directory for the chat (native); see [Terminal](#terminal) |
| `-s`, `--state-file` | | File that keeps the screen-derived messages (PTY) |
| `--load-state`, `--save-state` | `true` with `--state-file` | Load the state file on startup, save it on shutdown |
| `-I`, `--initial-prompt` | | First message for the agent; read from stdin when piped |
| `-W`, `--term-width` | `80` | Width of the emulated terminal, and of TTY mode's lines (PTY) |
| `-H`, `--term-height` | `1000` | Height of the emulated terminal (PTY) |
| `--api-token` | off | Require a Bearer token; without a value, a random token is generated and printed; see [API token authentication](#api-token-authentication) |
| `-a`, `--allowed-hosts` | `localhost`, `127.0.0.1`, `[::1]` | Accepted `Host` headers, `*` for any; see [Allowed hosts](#allowed-hosts) |
| `-o`, `--allowed-origins` | `http://localhost:3284`, `http://localhost:3000`, `http://localhost:3001` | Origins allowed to call the API from a browser, `*` for any; see [Allowed origins](#allowed-origins) |
| `--webhook-url` | | URL notified when the run status changes; see [Run-status webhooks](#run-status-webhooks) |
| `--webhook-timeout` | `10s` | Timeout of each delivery attempt |
| `--webhook-max-attempts` | `3` | Delivery attempts |
| `--webhook-payload-template` | | Go `text/template` for the POST body |
| `-c`, `--chat-base-path` | `/chat` | Base path of the chat UI |
| `--pid-file` | | File to write the server's process ID to, for shutdown scripts |
| `-P`, `--print-openapi` | | Print the OpenAPI schema and exit |

### `agentapi update`

Update the agentapi binary to the latest release from GitHub.

```bash
agentapi update          # download and install the latest version
agentapi update --check  # only check if an update is available
agentapi update --force  # update even if already at the latest version
```

Release binaries are verified against the release's `checksums.txt` SHA-256
manifest before the running executable is replaced. An update is rejected if
the manifest is missing, malformed, or does not match the download.

### `agentapi attach`

Attach to a running agent's terminal session (PTY transport; ACP and native
agents have no terminal to attach to).

```bash
agentapi attach --url localhost:3284
```

A server started with `--api-token` needs the token: pass `--api-token` or
set `AGENTAPI_API_TOKEN`. Press `ctrl+c` to detach from the session.

## HTTP API reference

The server describes its API with OpenAPI: [openapi.json](openapi.json) in
this repository, `/openapi.json` on a running server, and a documentation UI
at `/docs`. It listens on port 3284 by default. Endpoints marked *native*
need the native transport.

### Conversation

| Endpoint | Description |
| --- | --- |
| `GET /messages` | All messages of the conversation |
| `POST /message` | Send a message, or queue it while the agent is busy; `dispatch_mode` is `queue` (default), `steer` or `interrupt` (*native*) |
| `DELETE /messages` | Clear the conversation and the queue, and restart the agent (*native*: in a new session) |
| `GET /rich-messages` | The structured conversation: thinking, tool calls with results, usage |
| `GET /timeline` | Normalized session events, for export or another UI |
| `GET /status` | Status, lifecycle, run and session IDs, epoch, session name, version, open dialog; *native*: capabilities, pending requests, settings, usage, terminal, login |
| `GET /title` | The session's human-readable title |
| `GET /events` | Server-sent events; `?sync=1` for incremental replay on reconnect |
| `GET /workspace` | The Coder workspace URL, when AgentAPI runs in one |

### Queue

| Endpoint | Description |
| --- | --- |
| `GET /queue` | The messages waiting for the agent |
| `PUT /queue` | Reorder the queue (`{"ids": [...]}`, every queued message once) |
| `PUT /queue/{id}`, `DELETE /queue/{id}` | Edit or remove a queued message |
| `POST /queue/{id}/steer` | Move a queued message into the running turn (*native*) |

### Native agent control

| Endpoint | Description |
| --- | --- |
| `POST /interrupt` | Stop the running turn |
| `GET /requests`, `GET /requests/{id}` | The requests the agent waits on (approvals, questions, plans) and the recently closed ones |
| `POST /requests/{id}/respond` | Answer a request: `option_id`, or `answers` keyed by question, with optional `text` |
| `GET /settings`, `PUT /settings` | The permission mode, model and reasoning effort, with the values they can take; PUT changes the ones in its body |
| `POST /compact` | Compact the agent's context (Codex, Pi) |
| `POST /login/retry` | Check the agent's login again now, while `login_required` is reported (409 when no login is needed) |

### Terminal and files

| Endpoint | Description |
| --- | --- |
| `GET /terminal` | The shell's output as server-sent events (*native*, unless `--terminal=false`) |
| `POST /terminal/input`, `POST /terminal/resize` | Type into the shell; set its size |
| `GET /files/content` | A file in the agent's working directory, cut at 1 MiB |
| `GET /files/diff` | The file's uncommitted changes against HEAD |
| `GET /files/preview` | An HTML file with the files it refers to inlined, for a sandboxed iframe |
| `GET /files/image` | An image file in the agent's working directory (raster images judged by content, and SVG) |

### Uploads

| Endpoint | Description |
| --- | --- |
| `POST /upload` | Store a file (up to 10 MB) and return its path, for `@"<path>"` in a message |
| `GET /uploads/{checksum}/{name}` | An uploaded image (PNG, JPEG, GIF, WebP or BMP, judged by content), for the chat |

### Integration endpoints

| Endpoint | Description |
| --- | --- |
| `GET /handoff`, `POST /handoff/reply` | The [human-reply API](#human-reply-api): what the agent waits on, and an answer to it |
| `GET /webhook`, `PUT /webhook` | Read or change the [run-status webhook](#run-status-webhooks) |
| `GET /mcp`, `PUT /mcp` | The MCP servers and the managed config file; PUT replaces them, `?restart=true` restarts the agent (*native*: on its session) |
| `POST /mcp/check` | Check remote HTTP servers and resolve stdio executables |
| `POST /mcp/servers`, `PATCH /mcp/servers/{name}`, `DELETE /mcp/servers/{name}` | Create, update or remove one MCP server |
| `GET /mcp/profiles` | Export the saved MCP profiles |
| `PUT /mcp/profiles/{name}`, `DELETE /mcp/profiles/{name}` | Import, replace or remove a profile |
| `POST /mcp/profiles/{name}/apply` | Replace the MCP configuration with a profile |

### Authentication

| Endpoint | Description |
| --- | --- |
| `GET /auth/session` | Whether the API needs a token, and whether the caller has one (no credentials needed) |
| `POST /auth/session`, `DELETE /auth/session` | Sign a browser in with the token (session cookie), or sign it out |

The chat itself is served at `/chat/` (and `/chat/embed`), and TTY mode reads
the agent's screen from `/internal/screen`, which isn't part of the public
API.

## Architecture

AgentAPI is a single Go binary: an HTTP server (chi, with huma generating the
OpenAPI schema) that runs one agent process and serves the chat UI, a Next.js
app embedded in the binary. Every transport implements the same conversation
interface and reports to the same event emitter, so the HTTP API, the event
stream, the queue and the chat work the same over all three. See
[docs/architecture.md](docs/architecture.md) for the packages, the data flow
of each transport, the event stream, persistence and the security model.

## Development

```bash
make build      # out/agentapi, with the chat UI embedded
make gen        # regenerate openapi.json and the version
make lint       # Go, TypeScript, shellcheck and actionlint
go test ./...   # Go tests, including the e2e smoke test
```

Tests run with `CGO_ENABLED=0`. Commit `openapi.json` with every API change,
and regenerate it only with `make gen` (see [AGENTS.md](AGENTS.md)). The chat
UI lives in [chat/](chat/README.md): `bun run dev` serves it against a running
AgentAPI, and `agentapi server --transport native --type custom -- native-demo`
runs a simulated native agent for working on it without a real one.

## Runtime reliability

The fork also includes fixes for wide-character terminal cursor tracking,
PTY lifecycle leaks, concurrent event delivery, message tracking races, ACP
shutdown, TUI re-render artifacts, JSONL watcher flushing, rich message
content-block merging during Claude delta streaming, and runtime session-file
switching when Claude Code parks a session to a new JSONL. These changes are
intended to keep AgentAPI stable across long sessions, process replacement, and
temporary browser or network interruptions.

## Roadmap

Pending feedback, we're considering the following features:

- [Support the MCP protocol](https://github.com/coder/agentapi/issues/1)
- [Support the Agent2Agent Protocol](https://github.com/coder/agentapi/issues/2)

## Long-term vision

In the short term, AgentAPI solves the problem of how to programmatically control coding agents. As time passes, we hope to see the major agents release proper SDKs. One might wonder whether AgentAPI will still be needed then. We think that depends on whether agent vendors decide to standardize on a common API, or each sticks with a proprietary format.

In the former case, we'll deprecate AgentAPI in favor of the official SDKs. In the latter case, our goal will be to make AgentAPI a universal adapter to control any coding agent, so a developer using AgentAPI can switch between agents without changing their code.
