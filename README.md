# AgentAPI

English | [繁體中文](README.zh-TW.md)

Use AgentAPI to control [Claude Code](https://github.com/anthropics/claude-code),
[AmazonQ](https://aws.amazon.com/developer/learning/q-developer-cli/),
[Opencode](https://opencode.ai/), [Goose](https://github.com/block/goose),
[Aider](https://github.com/Aider-AI/aider), [Gemini](https://github.com/google-gemini/gemini-cli),
[GitHub Copilot](https://github.com/github/copilot-cli), [Sourcegraph Amp](https://ampcode.com/),
[Codex](https://github.com/openai/codex),
[Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started), [Pi](https://pi.dev/),
[Auggie](https://docs.augmentcode.com/cli/overview), and [Cursor CLI](https://cursor.com/en/cli)
with an HTTP API.

![The AgentAPI chat with two tasks: prompts, tool calls, thinking notes and the composer](docs/screenshots/chat-desktop.png)

The screenshots show AgentAPI v0.28.0 running the built-in `native-demo` agent. Its replies, tool
results, models and usage are simulated. See the [chat UI guide](docs/chat-ui.md) for features and
animations, or the [screenshot guide](docs/screenshots/README.md) to reproduce the captures.

Use AgentAPI to:

- Build a unified chat interface for coding agents.
- Provide an MCP server backend that lets one agent control another coding agent.
- Create tools that delegate pull request reviews to an agent.
- Watch many agents from one dashboard. Each AgentAPI pushes its tasks, the requests that need
  you, its messages and heartbeats there with [webhooks](#webhooks), and a client that connects
  to many follows each through its [summary](#watching-many-instances). The built-in
  [hub](#running-a-hub) is such a dashboard, on a desktop or a phone.

## About this fork

This fork builds on upstream v0.12.2. It drives Claude Code, Codex and Pi through their own
protocols with the [native transport](#native-transport) by default, and keeps terminal emulation
(the [PTY transport](#pty-transport)) for every other agent. Session management and chat features
support long-running tasks across transports, and [webhooks](#webhooks) report them to a central
platform. [CHANGELOG.md](CHANGELOG.md) lists what
changed, [docs/architecture.md](docs/architecture.md) describes how it is built,
[docs/hub.md](docs/hub.md) how to watch many AgentAPIs from one page, and the
[full comparison with upstream](https://github.com/coder/agentapi/compare/main...k1dav-c:agentapi:main)
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
- [Resource usage](#resource-usage)
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
  [native transport](#native-transport), which they use by default; agents with
  an ACP server can use the experimental ACP transport (see
  [Transports](#transports)).
- Read structured activity (thinking, tool calls and their results, token
  usage) from `GET /rich-messages` and `GET /timeline`.
- Messages sent while the agent works wait in a [queue](#message-queue) that
  you can edit and reorder; with the native transport they can also
  [steer or interrupt](#steer-interrupt-and-the-queue) the running turn.
- The server tracks each message and the agent's turn on it as a
  [task](#tasks): running, completed, failed, or stopped by a usage limit.
- Follow everything live over [server-sent events](#events-and-incremental-sync),
  with heartbeats and an incremental replay after a reconnect.

**Native agents**

- Tool approvals, questions, and plans arrive as
  [requests](#requests-approvals-questions-and-plans), answered over HTTP or
  in the chat.
- Switch the [permission mode, model and reasoning effort](#settings-permission-mode-model-and-effort)
  while the agent runs: bypass (the default), auto, ask or plan.
- See how full the [context and the rate limits](#context-and-rate-limits)
  are, and [compact](#slash-commands-and-compaction) the context; the chat
  suggests it once Claude Code's prompt cache has expired.
- When a [usage limit](#usage-limits) stops a turn, the queue waits until the
  limit resets, and the turn can continue by itself then.
- AgentAPI reports a missing or expired [login](#logging-in) with the command
  to run, and starts the agent by itself once it is logged in.
- A shared [terminal](#terminal) in the agent's working directory, for logins
  and quick commands.
- [Persistent sessions](#persistent-sessions): with `--state-db`, the
  conversation survives an AgentAPI restart, and a native agent resumes its
  own session.
- Open one of the agent's [earlier sessions](#earlier-sessions) in its working
  directory, or start a copy of one.
- See what each [session and task cost](#session-costs) at the model
  providers' list prices (the API-equivalent cost), subagents included, with
  totals for today and the last 7 and 30 days.

**Chat UI** (`/chat`)

- [Tasks](#tasks-and-the-transcript) read as a transcript, with tool calls as
  rows, thinking as margin notes and what each task cost, and can be exported
  as Markdown; the
  [Session Explorer](#session-explorer) has links, files, a task index, MCP
  servers, the agent's [earlier sessions](#earlier-sessions), Restart and the
  [webhook](#webhooks).
- [Lineage](#lineage): the subagents the agent ran, and what each did, step by
  step, even while it works.
- [Decision cards](#decision-cards) for terminal dialogs and native requests,
  menus for the agent's settings, a ring for its context, and slash commands.
- A [file viewer](#file-viewer) with the file, its git diff and a preview of
  HTML, Markdown and images, which finds a file the agent named relative to a
  repository below its working directory; [TTY mode](#tty-mode) and the native
  terminal, also on phones.
- [Background alerts](#alerts-connection-recovery-and-caching), browser
  notifications, a transcript cache, and recovery from dropped connections.

**Operations and integrations**

- [MCP management](#mcp-management) for Claude Code, Codex and Pi, with
  reusable profiles; restarting the agent updates Codex and Pi first.
- [A hub](#running-a-hub) (`agentapi hub serve`) watches the AgentAPIs of many
  machines, lists what needs you first, and opens any of them in the chat on a
  desktop or a phone. [`agentapi hub join`](#joining-a-hub) connects a
  machine's AgentAPIs to it, and they stay connected after a restart.
- [Webhooks](#webhooks) push HMAC-signed tasks, requests, messages and
  heartbeats to a dashboard that watches many agents. A client that connects
  to many follows each through one [summary stream](#watching-many-instances).
  With the [human-reply API](#human-reply-api), a bot can tell you when the
  agent stops and pass your answer back.
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

   This drives Claude Code through its own protocol, the
   [native transport](#native-transport): approvals arrive as structured
   requests, its settings can be switched and the chat has a terminal. To drive
   its terminal UI instead (the [PTY transport](#pty-transport), with TTY mode
   in the chat), add `--transport pty`:

   ```bash
   agentapi server --transport pty -- claude
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

The agent type determines screen formatting and available features. Set it with `--type`, or let
AgentAPI detect it from a command that matches a known type name, such as `agentapi server -- claude`.
Full paths such as `-- /usr/local/bin/claude` need an explicit type; unrecognized commands use `custom`.

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
| [Amazon Q](https://aws.amazon.com/developer/learning/q-developer-cli/) | `amazonq` (`q`) | ✓ | | | |
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
> AgentAPI detects the agent type from the agent command only when the command is a type name, such as `claude`, `codex`, `kimi` or `pi`. When you run the agent through a full path or a wrapper script, specify the type explicitly (e.g., `agentapi server --type=claude -- /usr/local/bin/claude`), or message formatting may break.

### Transports

`--transport` chooses how AgentAPI drives the agent. Without it, Claude Code, Codex and Pi use the
native transport, and every other agent the PTY transport, as does a server started with the
deprecated `--state-file`:

| | PTY | Native | ACP (experimental) |
| --- | --- | --- | --- |
| Flag | `--transport pty` | `--transport native` | `--transport acp` or `--experimental-acp` |
| Default for | every other agent, and with `--state-file` | Claude Code, Codex, Pi | |
| Agents | all of the above | Claude Code, Codex, Pi | agents with an ACP server |
| How | runs the agent's terminal UI in a terminal emulator and reads its screen | speaks the agent's own protocol: `claude -p` stream-json, `codex app-server`, `pi --mode rpc` | speaks the Agent Client Protocol over the agent's stdio |
| Tool calls and thinking | from the session log (Claude Code, Codex, Pi) | streamed from the protocol | tool calls as `[Tool: …]` lines in the reply; no thinking |
| Approvals, questions, plans | terminal dialogs, answered from decision cards or TTY mode | structured [requests](#requests-approvals-questions-and-plans) | approved automatically |
| Steer and interrupt a turn | | ✓ | |
| Settings, usage, compaction | | ✓ | |
| Survives an AgentAPI restart | `--state-db`: the conversation; the agent starts anew | `--state-db`: the conversation and the agent's session | |
| Terminal in the chat | TTY mode: the agent's own terminal | a shell in the working directory | |

```bash
agentapi server -- claude                    # native
agentapi server --transport pty -- claude    # Claude Code's terminal UI
agentapi server --state-db ~/.agentapi/myproject.db -- codex
agentapi server -- aider                     # PTY
agentapi server --type=kimi --experimental-acp -- kimi acp
```

`--state-db <file>` (native and PTY transports) keeps the conversation in a SQLite file, and a native
agent resumes its session when AgentAPI starts again with the same file; see
[Persistent sessions](#persistent-sessions). `--terminal` (default true, native transport only)
offers a shell in the agent's working directory; see [Terminal](#terminal). `--state-file <file>` is
deprecated: AgentAPI imports its conversation into a state database next to it and keeps it there
from then on.

## Chat UI

The chat UI at `/chat` is embedded in the binary and works with every transport on desktops and
phones. It includes searchable tool activity, compact tool-call cards and connection indicators.
The header shows the AgentAPI version and the agent's mark, such as Claude's orange square or Pi's
logo. See the [chat UI guide](docs/chat-ui.md) for screenshots and animations.

![Sending a task in the chat: the agent thinks, runs a tool and replies](docs/screenshots/demo-task.gif)

### Tasks and the transcript

The chat groups each user request with its response, thinking, tool calls and background activity
as a task. You can navigate between tasks in the transcript.

Each task has a timeline on the left: when it started, how long it took and, for Claude Code, Codex
and Pi, what it [cost](#session-costs). Your prompt appears in a tinted block labelled "You", followed
by the agent's logo, name, and reply in Markdown. Each tool call occupies one row with its command
(`$ command`), exit code, duration, and output preview. On wide screens, thinking appears as margin
notes.

The server's [task](#tasks) determines the displayed state (Working, Done, Failed, Limit reached)
and which tool calls and thinking belong to it. A strip above the composer shows the agent's
current activity. While the agent waits for an answer, the header, tab title and status strip show
"Needs you", and queued tasks are marked as held. Long tasks show their latest 40 steps first.

Each agent message has its own Markdown/raw toggle so you can switch display modes without
affecting the rest of the conversation.

### Session Explorer

The Session Explorer provides:

- links and file paths discovered in the current conversation;
- an index for jumping directly to earlier tasks;
- MCP server configuration without leaving the chat UI;
- a Session tab with the agent's [earlier sessions](#earlier-sessions) to open or copy, what they
  [cost](#session-costs), and a two-step restart confirmation;
- a Webhook tab that sets up the [webhook](#webhooks) (open the Explorer with ⌘K / Ctrl+K).

![The Session Explorer's Index tab listing the session's tasks](docs/screenshots/explorer-index.png)

Each task's ⋯ menu previews, copies or exports the task as Markdown.

### Lineage

When Claude Code or Codex runs subagents with the native transport, a header button shows their
total and running counts. It opens the Lineage panel, which lists each subagent's task, kind,
state and elapsed time. Select one to follow its thinking, tool calls, results and answer as it
works. The **Steps** button on an Agent or spawn_agent tool call opens that subagent directly.

![The Lineage panel on a subagent: its task, a file it read, a command it ran and its answer](docs/screenshots/lineage.png)

AgentAPI reads the steps from each subagent's log. Claude Code keeps these logs beside the parent
session's log. The panel includes subagents from [earlier sessions](#earlier-sessions) you open;
a copy of a session starts without the original's subagents. `GET /subagents` lists them, and
`GET /subagents/{id}` returns a subagent's task and messages.

Claude Code reports a subagent's state through its Agent tool call. Codex's spawn call returns
before the subagent finishes, so AgentAPI reads its state from its own log. Codex encrypts the task
sent to a subagent; the panel uses its task name and nickname as the label.

### Attachments and images

Paste an image into the composer to upload it as an attachment. Pasted text remains text. Image
attachments and images in sent messages appear as thumbnails; click one to view the full image. The
agent receives the file path.

### Decision cards

Items awaiting your response appear above the composer as decision cards: its terminal dialogs on
the PTY transport (see [Interactive prompt support](#interactive-prompt-support)), its
[requests](#requests-approvals-questions-and-plans) on the native transport, and the
[login card](#logging-in) while it waits for a login. Number keys pick options. On a phone a card
takes at most 60% of the screen, and a title cut to two lines can be shown in full.

![A question card on the first of its three steps, above the composer](docs/screenshots/native-question.png)

The agent's questions come one step at a time (here the package manager, then the checks and notes)
and are sent together. The header, the status line and the tool row show that the agent is waiting
for you.

### File viewer

File paths in the chat open the file the agent works on. The file name on a tool call's row (Claude
Code's Read, Write, Edit, MultiEdit and NotebookEdit; Codex's apply_patch and view_image; Pi's read,
write and edit), a path in inline code in the agent's reply (`lib/foo.go:12` opens at line 12) and
the Explorer's Files tab open a viewer with two tabs: File, with line numbers and syntax
highlighting, and Diff, with the tool call's own change and the file's uncommitted changes against
HEAD (`GET /files/content`, `GET /files/diff`, cut at 1 MiB). Only files inside the agent's working
directory can be opened. An expanded tool call lists its files and shows its change as a diff, with
the raw input a click away. On a phone the viewer is full screen.

If a path isn't in the working directory, AgentAPI looks for it below (`GET /files/find`), ignoring
case. This covers an agent whose working directory is a home directory and that names files
relative to a repository below it. One match opens with a note naming the path the agent wrote;
several are listed to choose from. The search leaves out hidden directories, dependencies and Go's
module cache unless the path goes into them, and stops after 50,000 entries or 2 seconds.

![The viewer on my-app/docs/notes.md, with a note that docs/notes.md isn't in the working directory](docs/screenshots/file-find.png)

HTML, Markdown and SVG files have a third tab, Preview, and an image file shows as the image:

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

![The file viewer previewing index.html: the page rendered with its stylesheet and logo](docs/screenshots/file-preview.png)

![The Diff tab with the uncommitted changes to README.md](docs/screenshots/file-diff.png)

The [chat UI guide](docs/chat-ui.md#files) also shows the File tab and a Markdown preview.

### TTY mode

With the PTY transport, TTY mode is an escape hatch for when the chat view looks wrong: the terminal
button in the header swaps the conversation for the agent's terminal screen (xterm.js) and sends
every key straight to the agent, including arrows, Ctrl/Alt keys, pastes and IME input. The same key
bar as the [terminal](#terminal)'s gives a phone the keys its keyboard lacks. The font grows with
the window (11–20px); the mirror uses the agent's real terminal width, so start AgentAPI with
`--term-width 120` if you want longer lines.

![TTY mode on bash run as a PTY agent, with the column ruler and the key bar](docs/screenshots/tty-mode.png)

### With the native transport

Native requests for tool approval, answers or plan approval appear as cards above the composer,
using at most 60% of a phone's screen. While the agent works, the send menu can steer a message into
the running turn or interrupt it. The toolbar controls permission mode, model and reasoning effort,
and shows context usage and rate limits. Slash commands include `/model` and `/compact`.

The terminal button opens a shell in the agent's working directory. TTY mode requires the agent's
own terminal, so screen views stay hidden for native and ACP sessions. See
[Native transport](#native-transport).

### Alerts, connection recovery and caching

While the tab is in the background, its icon shows an amber dot when the agent needs you, a blue dot
while it works, and a green dot when a task finishes. Turn on browser notifications from the status
menu. The menu also downloads the conversation as JSONL, as does the Explorer's Session tab; for a
native Claude Code or Codex, its subagents' steps follow the agent's own.

![The session details menu opened from the status in the header](docs/screenshots/session-details.png)

The agent's warnings and errors show as notifications that stay until you close them. The server
resends its recent ones on every connection, so the chat remembers per server which ones you
closed, and a reload doesn't bring them back.

Long-running browser sessions are protected by SSE heartbeats, automatic reconnection,
stale-connection detection, and local recovery of messages that failed before reaching the server.
The page title and session header show the current task, agent status, and connection state.

Reopening a long conversation is fast: the chat caches the transcript in the browser (IndexedDB),
shows it immediately, and then asks the server only for changes since the last sync.

## Native transport

Native transport uses the agent's structured protocol to receive replies, tool
calls, and usage, and to handle approvals, questions, and plans in the chat UI.

**Choosing the native transport**

Claude Code, Codex and Pi use the native transport by default. `--transport native` asks for it
explicitly, and `--transport pty` drives their terminal UI instead:

```bash
agentapi server -- claude --model sonnet
agentapi server -- codex -a on-request
agentapi server -- pi --provider openai-codex --model gpt-5.6-luna --thinking low
```

- AgentAPI selects the adapter from the command name. Specify `--type` for
  full executable paths or wrapper scripts; without it they run as `custom`
  agents with the PTY transport.
- A server started with the deprecated `--state-file` and no `--transport`
  keeps the PTY transport, so command lines written for it still start; see
  [Persistent sessions](#persistent-sessions).
- ACP is a separate transport. `--transport acp` is equivalent to
  `--experimental-acp` and cannot be enabled together with native mode.
- For UI development, `--type custom -- native-demo` runs a simulated agent with
  the native transport; see the [development guide](chat/README.md). With
  `--type claude`, `codex` or `pi` instead, the demo poses as that agent, and
  AgentAPI reads that agent's logs for its earlier sessions and subagents.
  `--transport native` with any other agent type is rejected at startup.

**What to expect**

- Replies, tool results, and thinking update live as the turn runs.
- Approvals and questions appear as decision cards above the composer; see
  [request handling](#requests-approvals-questions-and-plans).
- When the agent is stopped or restarted and doesn't exit within 3 seconds of
  its input closing, AgentAPI signals its whole process group, which also stops
  its MCP servers and child commands.

AgentAPI removes environment markers and secrets inherited from a parent agent
session while preserving login and provider configuration. See
[child process environment](docs/architecture.md#child-process-environment) for
the complete list.

<details>
<summary>Technical details: protocols, streaming, status fields, and shutdown</summary>

**Agent protocols and thinking**

| Agent | Protocol | Thinking |
| --- | --- | --- |
| Claude Code | stream-json and control protocol | Summarized adaptive thinking |
| Codex | `codex app-server`, using JSON-RPC over stdio | Detailed reasoning summaries requested each turn |
| Pi | `pi --mode rpc` | Streamed as Pi writes it |

Messages come directly from the protocol, without screen parsing or a session-log
watcher. Each streaming update replaces the entire rich message, at most 10
snapshots per second per message. The latest state arrives within 100 ms after
the stream pauses.

**API status**

`GET /status` and `status_change` events include these fields:

| Field | Meaning |
| --- | --- |
| `transport` | `native` in this mode |
| `capabilities` | Operations the agent supports |
| `pending_requests` | Number of requests waiting for a response |
| `settings` | Current session settings |
| `usage` | Context and usage information |
| `terminal` | Whether a separate shell terminal is available |

When the agent is only waiting for a request response, `status` is `stable`, as
it is when waiting for a terminal dialog response.

**Shutdown**

The agent runs in its own process group. Shutdown first interrupts a running
turn and waits up to 3 seconds for it to end, so the session log doesn't end in
a tool call without result. Then it closes the agent's input so it can exit; if
it is still running 3 seconds later, AgentAPI sends SIGTERM to the group, then
SIGKILL 2 seconds after that.

</details>

### Logging in

Native mode uses the agent CLI's stored login. Log in as the same operating-system user that runs
AgentAPI. You can use the chat's [terminal](#terminal), any terminal on the same machine, or a PTY
session. The login card's **Run in terminal** button and the `/login` command both run the login
command in the chat terminal.

![A login card: the command to run, where to run it, Run in terminal and Check again](docs/screenshots/login-card.png)

- Claude Code: `claude auth login` uses a Claude subscription by default;
  `--console` selects Anthropic Console API billing. On a machine without a
  browser, `claude setup-token` creates a token for `CLAUDE_CODE_OAUTH_TOKEN`.
  Check the login with `claude auth status`.
- Codex: `codex login --device-auth` signs in with a ChatGPT plan from any
  device: open the link it shows and enter its one-time code. The login card
  and `/login` run it because AgentAPI usually runs on a server without a
  browser: `codex login`'s browser sign-in redirects the browser to
  `localhost:1455` on the machine that runs Codex, which a browser elsewhere
  can't reach. `codex login` still works with a browser on the same machine,
  and `printenv OPENAI_API_KEY | codex login --with-api-key` uses an API key.
  Check the login with `codex login status`.
- Pi: run `/login` inside `pi`, or set a provider key such as
  `ANTHROPIC_API_KEY` in AgentAPI's environment.

AgentAPI checks authentication when starting the agent:

- Claude Code uses `claude auth status --json`. API keys,
  `CLAUDE_CODE_OAUTH_TOKEN`, `apiKeyHelper`, Bedrock, and Vertex count as
  authenticated. With `--settings`, the check is skipped because `auth status`
  cannot see an `apiKeyHelper` supplied that way.
- Codex uses app-server's `account/read`.
- Pi checks its state: no selected model and no available models indicate a
  missing login.

If authentication is missing, AgentAPI keeps running and queues messages. `GET /status` and
`status_change` report `login_required`, including the agent, problem, login command, help, host,
and operating-system user. The chat shows these details in a card above the composer, with a button
to copy the command.

AgentAPI retries startup every 5 seconds and detects a completed login automatically. **Check
again** (`POST /login/retry`) retries immediately; if a startup check is already running, that check
is reused. Startup warnings appear once rather than on every retry. If the check itself fails, for
example with an older CLI or a wrapper, AgentAPI starts the agent as before.

Authentication can also expire during a session. A turn that fails with a login error reports
`login_required` in the same way. Recognized errors include `Not logged in`, HTTP 401,
`Invalid API key`, an unusable refresh token, Pi's `No API key found`, and
`Authentication failed for "<provider>"` after a subscription login expires.

After logging in, select **Check again**. AgentAPI restarts the agent on the same session and
preserves the conversation. A completed model turn also clears the login error; interrupted turns
and messages handled by the agent itself do not.

### Requests: approvals, questions and plans

Tool approvals, questions (Claude's `AskUserQuestion`, Codex's `request_user_input`, dialogs from
Pi extensions) and plan approvals (Claude's `ExitPlanMode`) arrive as requests:

- `GET /requests` lists the pending requests and the 50 most recently closed
  ones, and `GET /requests/{id}` returns one. `/events` sends a
  `request_update` event whenever one opens or closes, and `GET /status`
  counts the pending ones in `pending_requests`. Other transports return an
  empty list.
- `POST /requests/{id}/respond` answers one: `option_id` for approvals and
  plans (with optional `text` as feedback when the request has `feedback`),
  `answers` keyed by question ID for questions. A request is answered at most
  once: 409 when it is no longer pending (already answered, withdrawn or
  expired, including a request restored from `--state-db`), 400 when the
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

In the chat, requests appear above the composer as decision cards:

- Approvals show the command and distinct Allow, Always allow, and Deny options.
  A denial can include feedback.
- Plans render as Markdown and can be approved or returned with feedback.
- Questions appear one step at a time, with single-choice, multiple-choice,
  typed, or hidden answers. All answers are submitted together.

Number keys select options. They are disabled while you type or while a dialog,
menu, or terminal has focus; holding a key does not repeat the action.

Tool rows show whether approval is pending, granted, or denied. Answer
`AskUserQuestion`, `request_user_input`, and `ExitPlanMode` calls on the card.
Requests answered elsewhere or no longer pending disappear from the card. If a
question expires with the turn or is withdrawn, the UI explains that you can
still answer it with a normal message.

While a request is pending, the header and tab title say "Needs you", the tab
icon shows an amber dot, the embed status bar's icon turns amber, and a
background tab with notifications turned on shows a notification that the agent
needs you.

![An approval card for a command: Allow, Always allow Bash in this session, and Deny with a note](docs/screenshots/native-approval.png)

An approval shows what would run and why. **Allow** runs it once, **Always allow** stops asking for
the rest of the session, and **Deny** refuses it, with an optional note on what to do instead.

![A plan card with the plan in Markdown, Approve the plan, Keep planning and a feedback field](docs/screenshots/native-plan.png)

Plans stay above the composer while the agent waits. Read the plan, approve it, or enter feedback
and select **Keep planning** to ask for changes. The plan shown here is generated by `native-demo`;
approving it does not change files.

### Steer, interrupt and the queue

- `POST /message` takes `dispatch_mode`: `queue` (the default, as on every
  transport: send the message once the agent is idle), `steer` (add it to the
  turn the agent is working on; the reply says `steered`) or `interrupt` (stop
  that turn and send the message next). `steer` and `interrupt` need the
  native transport. While a turn runs they also need the matching capability,
  otherwise the answer is 400. When no turn is running (including while the
  agent restarts or waits for a login), both send or queue the message as
  usual.
- `POST /interrupt` stops the running turn (409 if there is none, or while no
  agent runs).
- `POST /queue/{id}/steer` moves a queued message into the running turn (409
  if no turn is running; the message stays queued).
- `PUT /queue` with `{"ids": [...]}` reorders the queue. It must list every
  queued message once (409 otherwise). This works on every transport.
- If the agent refuses a queued message, AgentAPI tries it up to 5 times in
  all, then drops it with an error that quotes it. A message the agent didn't
  answer in time is dropped at once: it may have reached the agent, and
  sending it again could run the task twice.

`GET /status` reports the supported `capabilities`: `steer`, `interrupt`, `resume` (the session
can be resumed when AgentAPI starts again, see [Persistent sessions](#persistent-sessions)),
`settings` (see [Settings](#settings-permission-mode-model-and-effort)), `compact` (see
[Slash commands](#slash-commands-and-compaction)) and `images` (image attachments, `@"<path>"`
references to PNG, JPEG, GIF or WebP files up to 10 MB, are sent to the agent as images as well as
paths; to an agent without it, only the paths are sent). While no agent runs (a restart, a wait for
a login), it reports what the last one supported.

In the chat, while the agent works, the send button's menu adds **Steer** (⌘/Ctrl+Enter) and
**Interrupt & send** (⌘/Ctrl+Shift+Enter) next to queueing (Enter), and Stop interrupts the turn
(`POST /interrupt`). Queued tasks can be steered in right away, and moved up and down on every
transport.

![A long turn with two queued tasks and the send menu open: Queue, Steer, and Interrupt & send](docs/screenshots/queue-steer.png)

### Settings: permission mode, model and effort

You can change the agent's permission mode, model, and reasoning effort while it runs.
`GET /settings` returns the current settings and available choices, including models and their
supported effort levels.

`PUT /settings` updates only the fields in the request body, such as `{"permission_mode": "ask"}` or
`{"model": "sonnet", "effort": "high"}`. Unsupported values return 400. `GET /status` and
`status_change` also include the current settings.

The permission modes are **bypass** (tools run without asking; the default), **auto** (the agent's
own review decides what may run), **ask** (tool calls that need approval come as requests) and
**plan** (the agent proposes a plan, which comes as a plan request, before it changes anything):

| Mode | Claude Code | Codex | Pi |
| --- | --- | --- | --- |
| bypass | `bypassPermissions` | no approvals, no sandbox | how Pi always runs |
| auto | `auto` | workspace sandbox, escalations decided by Codex's auto review | — |
| ask | `default` | `untrusted`: asks for every command it doesn't trust | — |
| plan | `plan` | plan collaboration mode | — |

If the agent uses a policy outside these four modes, such as Claude Code's `acceptEdits` after plan
approval or a custom Codex command-line policy, the UI does not label it as one of these modes.
Claude Code and Pi apply changes immediately. Codex applies them on the next turn, when app-server
accepts settings. AgentAPI reapplies your choices after an agent restart or a new login; with
`--state-db`, it also restores them after an AgentAPI restart. The UI follows changes the agent
makes itself, such as switching modes after plan approval.

In the chat, the composer's toolbar has a menu for the mode and one for the model and effort (on a
phone in a row of their own above the buttons).

![The permission mode menu: Bypass, Auto, Ask and Plan](docs/screenshots/permission-mode.png)

![The model menu with three models and the reasoning efforts of the chosen one](docs/screenshots/model-settings.png)

The permission control in the composer's toolbar changes how the agent handles approvals, and the
model control chooses a model and its reasoning effort. Swift, Deep, and Plain are demonstration
models supplied by `native-demo`.

### Context and rate limits

`GET /status` and `status_change` report the agent's `usage`: how full its context window is after
the last turn (`context`: `used` and `size` in tokens) and the account's rate limits (`rate_limits`:
a label, how much is used, when it resets and, for Claude Code, whether it is close to or at the
limit). Claude Code reports its 5-hour and weekly limits and its context after every turn, Codex its
context and its usage windows, and Pi its context. A new session starts with an empty context; the
rate limits belong to the account and stay. In the chat, a ring in the composer's toolbar shows how
full the context is, and its menu shows the limits with the time to each reset.

Claude Code also reports its `prompt_cache`: how long the model provider keeps the conversation's
cache after its last use (`ttl_seconds`: an hour on a subscription, five minutes with an API key, on
Bedrock or Vertex) and when it expires (`expires_at`). Once it has expired, the model reads the
whole context again on the next message, at the full price and against the usage limits. While the
agent is idle with an expired cache and at least 50k tokens of context, the chat says so above the
composer and offers to compact first. A dismissed note stays hidden until the next turn.

![A note above the composer: the prompt cache expired and the next message reads 120k tokens again, with Compact](docs/screenshots/cold-cache.png)

![The context popover: the tokens in the context window, and the 5-hour and weekly usage limits](docs/screenshots/context-usage.png)

### Usage limits

When the account reaches a usage limit, the agent stops its turn: Claude Code says "You've hit your
limit", Codex reports `usageLimitExceeded`, Pi the provider's quota error. `GET /status` and
`status_change` then report `limit_stop`: the agent's `message`, when it stopped and, if the agent
says, when the limit resets (`resets_at`, else the reset of the limit `rate_limits` reports as
reached). Queued messages wait until then instead of failing one after another. A message you send
yourself goes out at once, ahead of them, and ends the stop.

- `POST /limit/continue` with `{"at": "now"}` sends a message that asks the
  agent to go on with the task; `{"at": "reset"}` schedules it for a minute
  after the reset (409 without a reset time). The queued messages go after it.
- `DELETE /limit/continue` cancels a scheduled continuation; the queue still
  waits for the reset.
- `DELETE /limit` forgets the stop, so the queued messages go out as soon as
  the agent can take them.
- `--auto-continue` schedules the continuation for every stop with a reset
  time. A continuation the limit stops again is scheduled again.

The stop is kept with `--state-db`, so a scheduled continuation still goes out after AgentAPI
restarts. In the chat, the stop is a card above the composer with the reset time, **Continue at
reset**, **Continue now**, and **Send queued tasks**, and the queue says it waits for the limit.

![A task stopped on the 5-hour limit, marked Limit reached: the card with the reset time, and a task held in the queue](docs/screenshots/limit-card.png)

### Terminal

AgentAPI provides a shell in the native agent's working directory for logins, git commands and
other work. It supports colors, cursor movement and full-screen programs, and fits the window on
desktops and phones. Everyone shares the same shell, like a tmux window. It keeps running with no
viewers; if it exits, opening the terminal starts a new one. Its environment matches the agent's,
with AgentAPI's own variables removed.

Web links are clickable even when wrapped across lines. The last link printed by a command, such
as a login URL, also appears above the terminal for opening or copying. TTY mode behaves the same.

The key bar supplies arrows, Enter, Esc, Tab, Shift+Tab, Ctrl+C, Ctrl+R, Ctrl+A, Ctrl+E, Ctrl+U,
Ctrl+W, Home, End, Page up, Page down, / - | ~, Alt+↑ and Ctrl+L. Ctrl+D and Ctrl+Z require a second
press. The Ctrl and Alt buttons hold the modifier for the next key.

- `GET /terminal` streams its output as server-sent events (`output` events
  with base64 data, the first one the recent output to replay; `exit` when the
  shell exits) and starts the shell if it doesn't run.
- `POST /terminal/input` (`{"data": "..."}`) types into it and
  `POST /terminal/resize` (`{"cols": 120, "rows": 40}`) sizes it.
- It is on by default with the native transport; `--terminal=false` turns it
  off. It is as protected as the rest of the API, so use `--api-token` when
  the server is reachable by others.

![The shell terminal: git status and git log in the agent's working directory, and the key bar](docs/screenshots/terminal.png)

### Slash commands and compaction

Native agents do not run their interactive terminal UI. The chat handles these slash commands for
them:

- `/model [model]`, `/effort [effort]` (`/think`) and `/mode [mode]`
  (`/permissions`) switch a setting by its id or name, or open its menu;
  `/plan` switches to the plan mode;
- `/compact` compacts the context;
- `/clear` (`/new`) starts a new conversation;
- `/terminal` opens the terminal, and `/login` runs the agent's login command
  in it;
- `/help` lists them.

The composer suggests commands as you type their names. Tab completes the first suggestion. Anything
else is sent to the agent as typed, so the agent's own commands (Claude Code's `/code-review`, Pi's
extension commands) still work. `POST /compact` compacts the context while the agent isn't working
on a turn: Codex (`thread/compact/start`) in a turn of its own, Pi (its `compact` command) before it
answers. Messages sent meanwhile wait in the queue, and the agent's refusal (Pi's "Nothing to
compact") is a 409 with its reason. Claude Code compacts with the `/compact` message, which the chat
sends it.

![The slash commands listed after typing a slash in the composer](docs/screenshots/slash-commands.png)

### Persistent sessions

`--state-db <file>` keeps the conversation in a SQLite file: messages, rich messages (tool calls,
thinking), the timeline, requests and the queue, plus the agent's own session ID and the webhook
settings changed through the API. When AgentAPI starts again with the same file, the chat shows the
conversation as it was and queued messages are still queued. A native agent resumes its session, so
it remembers the conversation too. A PTY agent starts fresh below the restored conversation, and the
last agent message keeps its text until the next message. After a clean shutdown, browser caches
stay valid because the conversation keeps its epoch. Without `--state-db`, every start of AgentAPI
is a new conversation.

```bash
agentapi server --state-db ~/.agentapi/myproject.db -- claude
```

- Requests that were open when AgentAPI stopped are marked expired: the agent
  that asked is gone.
- Uploads are kept next to the file (`<file>.uploads`), because messages refer
  to them by path.
- A file belongs to one agent type, transport and working directory; AgentAPI
  refuses to start with another one's file. It is locked while AgentAPI runs,
  so a second AgentAPI (or `sqlite3`) can't open it.
- A new file is created readable and writable by its owner only (0600), and missing
  directories are created (0700).
- Restart (Explorer → Restart or `DELETE /messages`) clears the stored
  conversation and the queue and starts a new session. `?restart=true` on the
  MCP endpoints restarts the agent on its session instead, so the conversation
  stays; an agent that can't resume its session (`resume` is false) starts a
  new one, and the conversation is cleared. The settings chosen for the session
  are kept either way.
- Writes are batched every 200 ms, so streaming doesn't wait for the disk.
  After a crash the last of them may be missing, so the conversation gets a
  new epoch and clients load it again in full. Writes that keep failing are
  reported as an error.
- A restored native conversation doesn't get `--initial-prompt` again. A PTY
  conversation records the prompt it got and sends it again after a restart
  only if it changed.
- Webhook settings changed with `PUT /webhook` or in the Explorer apply after
  a restart, unless AgentAPI starts with other webhook flags.
- The time a client last showed the conversation (`POST /tasks/seen`) is
  kept, so a task that ended unseen is still [unread](#watching-many-instances)
  after a restart. A database written before 1.2 treats its tasks as seen.
- The ACP transport doesn't keep its conversation.
- `--state-file <file>`, the PTY transport's former persistence, is
  deprecated. The conversation it holds is imported the first time into a
  state database next to it (the same name with `.db`, or `--state-db`), which
  keeps the conversation from then on; the file isn't written anymore.
  `--load-state=false` skips the import, and `--save-state` has no effect.
  SIGUSR1 writes what the state database hasn't written yet.

### Earlier sessions

AgentAPI reads Claude Code, Codex and Pi session logs to list and open earlier sessions in the
working directory. This works without `--state-db`:

- `GET /sessions` lists them, the latest first, with the one the agent has
  open (`current`): each with its `id`, `title` (the agent's or the user's
  name for it), `first_prompt`, number of `prompts`, and when it started and
  last changed. Sessions in which the user sent nothing are left out. They
  are read from Claude Code's projects directory, Codex's sessions and Pi's
  session directory (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`,
  `PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR` and a `--session-dir`
  passed to Pi are honored). A session belongs to the directory its log
  records, even when AgentAPI runs in it through a symlink. Claude Code gives
  some directories the same name (`/w/a_b` and `/w/a-b`); AgentAPI keeps their
  sessions apart.
- `POST /sessions/{id}/resume` restarts the agent on that session, and
  replaces the conversation with the session's, read from its log. With
  `{"fork": true}` the agent opens a copy of it under a new id (Claude Code's
  `--fork-session`, Codex's `thread/fork`, Pi's `--fork`), and the session
  itself stays as it was. The queue and open requests are cleared, as on a
  restart. It returns 409 while the agent works on a turn, compacts its
  context or restarts, and the refused resume leaves the conversation as it
  was.

In the chat, the Explorer's Session tab lists the sessions with **Open** and **Start a copy**, which
ask for a second click.

![The Explorer's Session tab: what the sessions cost today, in 7 and 30 days and in all, and Claude Code's sessions in the directory with Open and Start a copy](docs/screenshots/explorer-sessions.png)

### Session costs

AgentAPI reads model calls from the agent's session logs and prices their tokens in USD at the
providers' list prices. This is an API-equivalent cost: Claude and ChatGPT subscriptions don't
charge per token, so it estimates what the same work would cost with an API key.

- Claude Code: every call in the session's log and in its subagents' logs, with the cache writes
  priced as Anthropic's 5-minute or one-hour cache. Calls Claude Code counts in its own total but
  doesn't log one by one, such as compacting the context, are added from its cost records as
  unitemized calls.
- Codex: each turn's token counts, its subagents' included; input read from the cache is priced as a
  cache read, and the rest as input.
- Pi: the cost Pi reports for each call.

A call that a fork copied from another session counts in the session it came from, so totals don't
count it twice. The built-in prices cover Anthropic's and OpenAI's models; `--prices <file>` adds
models or replaces their prices, in USD per million tokens:

```json
{
  "my-model": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75},
  "gpt-6.1-sol": {"input": 2, "output": 10, "cache_read": 0.1, "cache_write": 2.5,
    "tiers": [{"input_tokens_above": 272000, "input": 4, "output": 15, "cache_read": 0.2, "cache_write": 5}]}
}
```

A tier applies to a call whose input (uncached, read from and written to the cache) is above
`input_tokens_above`. Model IDs are matched ignoring case, a provider prefix (`anthropic/`), a
context suffix (`[1m]`) and a release date, else by the longest known prefix
(`claude-opus-5-5-x` is priced as `claude-opus-5-5`). A call to a model without a price counts its
tokens but no cost (`priced: false`).

- `GET /sessions` gives each session's `cost`: `usd`, `tokens`, `calls`, the cost per model
  (`models`) and per hour (`hourly`, in UTC), and `total_cost`, the sum of all of them.
- `GET /cost` gives the open session's cost and its model `calls` (`time`, `model`, `tokens`, `usd`,
  `subagent_id`), from which a task's cost is the calls made from its message until the next.

In the chat, each task shows what it cost under its start time and duration (in its header on narrow
screens), with the tokens in the tooltip. The chat refreshes the cost when a turn ends, and every 15
seconds while one runs. The Explorer's Session tab shows what each session cost, and what all of
them cost today, in the last 7 and 30 days and in all, by your time zone.

![A task's time rail: its start time, duration and cost](docs/screenshots/task-cost.png)

### Claude Code

AgentAPI runs `claude -p` headless with stream-json on stdin and stdout. Tool calls carry their
input, result, status and images, and each assistant message has its model, stop reason and token
usage.

The permission mode, model and effort switch with Claude Code's control requests
(`set_permission_mode`, `set_model`, `apply_flag_settings`); its models and their efforts come
from its initialize response, and the model and effort it runs with from `get_settings`. AgentAPI
adds `--allow-dangerously-skip-permissions`, without which Claude Code ignores a switch to
`bypassPermissions`, except where Claude Code refuses that mode (as root outside a sandbox).
Approving a plan offers "Yes, and bypass permissions" whenever the session can switch to it.

- Approvals offer Allow, Deny (with optional feedback for Claude) and, when
  Claude Code suggests a permission rule, "Allow always", which applies the
  rule to this session only. Commands Claude Code considers read-only run
  without asking.
- Questions list Claude's options; any other answer is accepted too.
- Plans show the plan and offer "Yes, and bypass permissions" (when the
  session can switch to it), "Yes, and auto-accept edits", "Yes, and manually
  approve edits" and "No, keep planning". Feedback sent with No makes Claude
  revise the plan and ask again.

Claude Code supports `steer` (a steered message joins the running turn at Claude's next step),
`interrupt` (if Claude Code doesn't end the interrupted turn within 10 s, AgentAPI ends it),
`resume` and `images`. The session name is the title Claude Code generates after the first prompt;
after `/clear` the new session is titled too, and it is the one `--state-db` resumes. A turn ends
when Claude Code reports it is idle, so a background subagent and the turn it wakes up when it
finishes belong to the turn that started it. The subagent's own steps are left out of the
conversation, as in the session log, and its tool call shows as running until it reports.

Command line and environment:

- AgentAPI adds `-p --output-format stream-json --input-format stream-json
  --verbose --include-partial-messages --replay-user-messages
  --permission-prompt-tool stdio`, and `--session-id` for a new session or
  `--resume` for a resumed one.
- Unless you choose otherwise, it adds `--permission-mode bypassPermissions`,
  so tools run without asking. Questions (AskUserQuestion) and plans
  (ExitPlanMode) still come as requests, and approving a plan offers to
  bypass permissions. Pass `--permission-mode default` (or `acceptEdits`,
  `plan`, …) to start in another mode, or switch it while Claude Code runs
  (see [Settings](#settings-permission-mode-model-and-effort)). AgentAPI also
  adds `--allow-dangerously-skip-permissions`, so a session can switch to
  bypass later. Claude Code refuses `bypassPermissions` as root outside a
  sandbox. When AgentAPI runs as root, it adds `--permission-mode default`
  instead, leaves out `--allow-dangerously-skip-permissions` and logs a
  warning. Set `IS_SANDBOX=1` in a sandbox, or choose a mode yourself
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
  and `--resume-drops-turn`, even inside a group of short flags such as
  `-pc`. Your own copies of `--verbose`,
  `--include-partial-messages` and `--replay-user-messages` are dropped.
  Everything else (`--model`, `--allowedTools`, `--mcp-config`,
  `--append-system-prompt`, ...) is passed through.
- Besides removing a parent agent session's variables (see
  [Native transport](#native-transport)), AgentAPI sets
  `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and
  `CLAUDE_CODE_ENTRYPOINT=sdk-cli`.

Resuming a session keeps Claude's context; Claude Code doesn't replay the earlier messages, so the
chat shows them only from the `--state-db` file. A session can be resumed once it had a turn; with
`--no-session-persistence` it can't, and `resume` is false. Requests still open when Claude Code
exits or restarts can't be answered anymore. Slash commands that need the interactive UI don't work.

### Codex

AgentAPI starts `codex app-server` and starts or resumes a thread. Messages, rich messages and the
timeline have the same shape the session-log parser produces, so the chat shows Codex's replies,
reasoning summaries, commands (the command itself, not the `bash -lc` wrapper), file edits, MCP tool
calls, plan updates and token usage as they stream. Every turn asks for detailed reasoning summaries
unless you choose `model_reasoning_summary` with `-c`; whether the model writes any depends on the
model and the reasoning effort.

Codex's command line after `codex` is handled like this:

- `-c`/`--config`, `--enable`, `--disable`, `--code-mode-host`,
  `--strict-config` and `--analytics-default-enabled` are passed to
  `codex app-server`.
- `-m`/`--model`, `-s`/`--sandbox`, `-a`/`--ask-for-approval`
  (`untrusted`, `on-request`, `never`) and `-C`/`--cd` become the thread's
  settings; `--dangerously-bypass-approvals-and-sandbox` means `-a never -s
  danger-full-access`. Without them (or `-c approval_policy=...`,
  `sandbox_mode`, `approvals_reviewer`), the thread runs without approvals or
  sandbox (the native transport's bypass mode). Switch it from the chat or with
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

Codex supports `steer`, `interrupt`, `resume` and `images`. Command, file-edit and MCP tool
approvals, permission requests, MCP form elicitations and `request_user_input` questions arrive as
requests. Codex asks those questions in the plan mode; with codex-cli 0.160,
`--enable default_mode_request_user_input` lets it ask them in the default mode too. The plan mode
is Codex's plan collaboration mode, an experimental part of app-server's API that AgentAPI opts
into. Codex ends its turn with a plan, and AgentAPI asks "Implement this plan?" as the Codex TUI
does. The Yes options (`implement_bypass`, `implement_auto` and `implement_ask`, the mode before
the plan first) leave the plan mode for that permission mode, and Codex implements the plan in the
same turn. No (`keep_planning`) with feedback sends the feedback back to Codex, which keeps
planning; No alone ends the turn.

The other requests' option IDs are Codex's own decisions (`accept`, `acceptForSession`,
`acceptWithExecpolicyAmendment`, `decline`, `cancel`, …). Nothing is approved automatically, and
requests AgentAPI can't show are declined. A command approval shows the command that runs, without
the shell wrapper; Codex's reading of it (files it reads, searches) is in the detail. Requests still
open when a turn ends are declined (MCP elicitations cancelled), so neither Codex nor an MCP server
keeps waiting. A steered message joins the running turn at Codex's next model request (the current
output keeps streaming); the reply to it is filed under the steering message. An interrupt ends the
turn within milliseconds, and tool calls it cut short show as failed.

The session reference is Codex's thread ID. Resuming it continues the thread with its context. A
turn that was running when the process died comes back as interrupted, approvals that were pending
are gone, and Codex's earlier turns are not loaded into AgentAPI's conversation (the chat shows them
only from the `--state-db` file). A thread that can't be resumed is replaced by a new one, with a
warning. Codex doesn't name threads itself; a name set elsewhere (`thread/name/updated`) becomes the
session name. MCP servers managed from the Explorer live in `$CODEX_HOME/config.toml`, which
app-server reads when it starts; saving them with restart restarts Codex on its thread.

Codex's own slash commands are TUI features; the chat runs `/model`, `/compact` and the others in
[Slash commands](#slash-commands-and-compaction) itself, and `/review` isn't available. Command
output streams as a preview that Codex may leave incomplete, and the full output replaces it when
the command ends.

### Pi

AgentAPI runs Pi in its RPC mode (`pi --mode rpc`, JSON over stdin and stdout).

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
  `notify` with level error shows as an error, and its `setTitle` stands in
  for the session name until Pi has one; Pi's own name (`--name`, renames) is
  the session name.
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
  Stop and the answer buttons on tool calls aren't offered either). `raw`
  messages (keys for the terminal) are refused with 400. The agent's slash
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

The PTY transport works with every agent that has a terminal UI, and is the default for every agent
but Claude Code, Codex and Pi, which use it with `--transport pty`. For those three it also follows
their session logs for structured activity, and the chat answers their terminal dialogs.

### How it works

AgentAPI runs an in-memory terminal emulator. It translates API calls into appropriate terminal
keystrokes and parses the agent's outputs into individual messages.

#### Splitting terminal output into messages

There are two types of messages:

- User messages: sent by the user to the agent
- Agent messages: sent by the agent to the user

To parse individual messages from the terminal output, we take the following steps:

1. The initial terminal output, before any user messages are sent, is treated as the agent's first message.
2. When the user sends a message through the API, a snapshot of the terminal is taken before any keystrokes are sent.
3. The user message is then submitted to the agent. From this point on, any time the terminal output changes, a new snapshot is taken. It's diffed against the initial snapshot, and any new text that appears below the initial content is treated as the agent's next message.
4. If the terminal output changes again before a new user message is sent, the agent message is updated.

#### Removing TUI elements from agent messages

AgentAPI removes two common TUI elements from replies:

- Echoed user input: lines containing the text of the user's last message.
- The input box: trailing lines with common TUI elements such as `>` or `------`.

#### What will happen when Claude Code, Goose, Aider, or Codex update their TUI?

Message splitting should still work because it doesn't depend on the TUI structure. Removing TUI
elements may need an update; until then, replies may include extra interface text.

### Structured Claude, Codex and Pi activity

In addition to parsing terminal snapshots, this fork can watch Claude, Codex and Pi session logs.
This provides structured thinking blocks, tool invocations, tool results, usage information, and
stable message identifiers that cannot always be reconstructed reliably from terminal output alone.

- `GET /rich-messages` returns the structured conversation.
- `GET /timeline` returns normalized session events suitable for export,
  auditing, or building another UI. With `?subagents=true` (native Claude
  Code and Codex), the events of the subagents the agent ran follow, read
  from their own logs. Each subagent's events come in turn, starting with a
  system event; each event has its `subagent_id` and, for Claude Code, the
  `parent_tool_use_id` of the Agent tool call that started it. The agent's
  own events only have the subagent's prompt and final answer.
- Background and delegated tasks remain visible with their running, completed,
  or failed state and output details.
- Claude thinking blocks are rendered inline as collapsible sections in the
  task timeline.
- Codex is started with `-c model_reasoning_summary="auto"` (unless you set it
  yourself) so its log contains readable reasoning summaries instead of only
  encrypted reasoning.

Terminal parsing remains the fallback for other agents and for environments where a session log is
unavailable. With the [native transport](#native-transport), the same endpoints are filled from the
agent's protocol instead of its session log.

### Interactive prompt support

The chat UI detects interactive TUI prompts — such as Claude Code's plan approval dialog or
permission confirmation, Codex's approvals, and Pi's project trust prompt — and docks them above the
composer as a decision card. Number keys pick an option. Multi-select questions (Claude Code's
AskUserQuestion with `multiSelect`) show checkboxes and a Continue button that moves on to the next
question or the review step. A numbered list in the agent's answer is not mistaken for options.
`GET /status` reports the prompt as `terminal_prompt`. With the
[native transport](#native-transport) there are no terminal prompts; approvals, questions and plans
arrive as structured requests instead.

### Kimi Code CLI

This fork adds the `kimi` agent type, automatic detection for the `kimi` executable, chat UI
labeling, terminal message formatting, readiness detection, and tests for its startup state.

Kimi can use the regular interactive PTY transport:

```bash
agentapi server -- kimi
```

It can also use Kimi's native ACP server after completing `/login` once:

```bash
agentapi server --type=kimi --experimental-acp -- kimi acp
```

### Pi coding agent

With `--transport pty`, the `pi` agent type runs the [Pi coding agent](https://pi.dev/) (`npm
install -g @earendil-works/pi-coding-agent`) in its interactive terminal UI. It is auto-detected
when the executable name is `pi`:

```bash
agentapi server --transport pty -- pi
```

Log in once with `/login` in Pi (TTY mode in the chat UI works), or set a provider key such as
`ANTHROPIC_API_KEY`. AgentAPI follows Pi's session log (`~/.pi/agent/sessions/`, or
`$PI_CODING_AGENT_DIR` / `$PI_CODING_AGENT_SESSION_DIR`) for structured messages, so the chat shows
Pi's tool calls, thinking and token usage like it does for Claude Code and Codex.

Pi's dialogs (such as the project trust prompt) can be answered from the chat, and the header and
tab icon show Pi's logo. The Explorer's MCP tab manages Pi's user-level servers in
`~/.pi/agent/mcp.json`, and restarting the agent runs `pi update --self` first.

AgentAPI starts Pi with its own `--session-id` (a new one on every start or restart) so it follows
the right session log even when several Pi processes share a directory. If you pass `--session`,
`--continue`, `--resume` or `--session-id` yourself, AgentAPI uses your session settings; with
`--continue`/`--resume` the chat falls back to the terminal output.

Without `--transport pty`, Pi runs in its RPC mode; see [Native transport](#native-transport).

## Conversation API

These endpoints work with every transport, except where noted; the
[HTTP API reference](#http-api-reference) lists them all.

### Messages and status

- `POST /message` sends a message: `{"content": "Hello, agent!", "type": "user"}`.
  A 200 means AgentAPI saw the agent start on it, or queued it (`queued`)
  because the agent is busy; `dispatch_mode` chooses what happens then (see
  [Steer, interrupt and the queue](#steer-interrupt-and-the-queue)).
  `"type": "raw"` types keys into the agent's terminal instead (PTY
  transport).
- `GET /messages` returns the conversation; `GET /rich-messages` and
  `GET /timeline` return its structured form (see
  [Structured Claude, Codex and Pi activity](#structured-claude-codex-and-pi-activity)).
- A message that went out but wasn't confirmed (the call timed out, or
  AgentAPI was stopping) may have reached the agent: it isn't sent again,
  but leaves the queue with an error.
- `DELETE /messages` clears the conversation (messages, rich messages,
  requests, tasks, timeline, errors and the queue) and restarts the agent
  (with the native transport, in a new session). With the ACP transport,
  which can't restart its agent, it returns 400. While the agent restarts,
  messages wait in the queue; raw keys, another restart and opening a
  session get 409, and so does a restart while AgentAPI stops.
- `GET /status` returns the backward-compatible `stable`/`running` status,
  the detailed lifecycle (`starting`, `ready`, `running`, `restarting`,
  `exited` or `failed`), the transport, `server_id` (a random identifier of
  this AgentAPI run, new at every start), `turn` (how many times the agent
  started working), the conversation's `epoch`, the session name, the
  AgentAPI version, the agent's open dialog (`terminal_prompt`) and the
  terminal width (`terminal_columns`). With the native transport it also
  reports `agent_session_id` (the agent's own session, which
  [`GET /sessions`](#earlier-sessions) lists), `capabilities`,
  `pending_requests`, `settings`, `usage`, `terminal` and `login_required`.
- `GET /tasks` returns the conversation's [tasks](#tasks).
- `GET /title` returns the session's human-readable title.
- `POST /upload` stores a file (up to 10 MB; a body over 11 MiB is refused
  before it is read) and returns its path, which a message references as
  `@"<path>"`.

### Tasks

AgentAPI groups the conversation into tasks, for every transport: a task is a message of yours and
the agent's turn on it. Messages you add while it runs (steering) belong to it; the next message
after it ends starts the next task. A task ends when the agent is idle again with no request or
terminal dialog open. When answering a request or dialog leaves the agent idle, AgentAPI waits 3
seconds before ending the task, since the agent usually continues working.

`GET /tasks` lists them, the earliest first, each with:

- `id`: the ID of the message that started it, as in `GET /messages`;
- `status`: `running`, `completed`, `failed` (the agent reported an error or
  exited; `errors` says what) or `limited` (a [usage limit](#usage-limits)
  stopped it; `limit` has the stop);
- `prompt` (the start of the message), `started_at` and `ended_at`;
- `reply_id`: the agent's last message of the task;
- `steered`: the IDs of the messages added while it ran.

Each rich message carries the `task_id` of the task it began in, so a client groups the tool calls
and thinking by task without guessing from timestamps; a resumed session's rich messages have none.
Messages restored or opened from before AgentAPI started are history: their tasks are completed. The
[state database](#persistent-sessions) keeps the tasks, and a task that ran when AgentAPI stopped is
over after a restart. `/events?sync=1` sends `task_update` when a task starts or changes, and the
chat and the [webhook](#webhooks)'s `task.*` events use the same tasks.

### Message queue

Messages submitted while an agent is busy are placed in a FIFO queue instead of being rejected.
Queued messages can be inspected, edited, or deleted before delivery through both the API and chat
UI.

- `GET /queue` lists the queued messages.
- `PUT /queue/{id}` edits a queued message and `DELETE /queue/{id}` removes
  it.
- `PUT /queue` with `{"ids": [...]}` reorders the queue; it must list every
  queued message once.
- `POST /queue/{id}/steer` moves a queued message into the turn the agent is
  working on (native transport).
- With `?sync=1`, `/events` sends a `queue_update` with the whole queue
  whenever it changes, so a client sees what another client queued without
  asking.

### Events and incremental sync

`GET /events` streams server-sent events: `message_update`, `status_change`,
`rich_message_update`, `agent_error`, `request_update` (native transport) and a `heartbeat` every
15 seconds, so clients can detect a lost connection. With `?sync=1` the stream starts with a
`session_sync` event, adds `task_update` and `queue_update`, and every update carries a `seq`;
reconnecting with `&since=<seq>&epoch=<epoch>` replays only what changed. Clients without `sync`
don't get those three events: some (such as agentapi-sdk-go) reject event types they don't know.

With `?sync=1`, events also carry SSE IDs (the end of the replay has the replay's `seq`, later
changes their own), so a plain `EventSource` that reconnects by itself resumes from its
`Last-Event-ID`; put the `epoch` from `GET /status` in the URL. When a message is taken back (a
turn the agent refused to start), the conversation gets a new epoch and the server ends the event
streams (screen streams excepted): clients reconnect, and with their old epoch get the whole state
again. Opening a session with more than 256 rich messages ends them too, rather than streaming it.

## Integrations

Use [webhooks](#webhooks) to send tasks, requests, messages and heartbeats to a receiver,
the [summary](#watching-many-instances) to monitor several AgentAPIs,
the [human-reply API](#human-reply-api) to answer the agent through a bot, and
[MCP management](#mcp-management) to configure the agent's tools.

### Watching many instances

To display several AgentAPIs in a sidebar or dashboard, read one summary response or stream per
instance:

- `GET /summary` returns the `instance` identity (as in [webhook](#webhooks) events: `id`, `name`,
  `server_id`, host, port, directory, agent and `url`), `status`, `lifecycle` and a `title` from the
  session name or the start of the latest prompt. It also includes the latest [task](#tasks), task
  count, `pending_requests`, the oldest pending `request` (its kind, title and when the agent asked),
  `queued_messages`, the open terminal dialog, `login_required`, `limit_stop`, `usage` (context
  window and account limits) and, for native Claude Code, Codex and Pi, `spend`: the API-equivalent
  cost of the agent's sessions in the working directory over the last 24 hours (`day_usd`) and 7
  days (`week_usd`), worked out from its logs once a minute.
- `attention` identifies why the agent needs you, in priority order: `login`, `request` (approval,
  question or plan), `dialog` (in the terminal), `limit` (a usage limit stopped the turn), `exited`,
  or `none`.
- `GET /events?only=summary` sends `summary_update` on connection and when the summary changes,
  plus heartbeats. It omits messages and screen updates, allowing a client to follow dozens of
  instances. Reply text growing doesn't change the summary; task starts and ends, requests, status
  changes and queue changes do. Other `/events` clients don't receive `summary_update`.
- `unread` is true when the latest task ended after a client last showed the conversation, and
  `seen_at` says when that was. `POST /tasks/seen` marks the tasks seen for every client of the
  instance; the chat calls it while it shows the conversation. The
  [state database](#persistent-sessions) keeps it, so an unseen task stays unread after a restart.

To find the instances on a machine, read the registry: while it serves, each AgentAPI keeps an
entry in `~/.agentapi/instances` (`--registry-dir`; `--registry=false` turns it off) with its `id`,
`name`, `server_id`, `url` (`--public-url`, else its listen address), PID, directory, agent and
`auth_required`, never the token. It removes the entry when it stops; an entry whose process is
gone (after a crash) is removed when the registry is listed. Check that `GET /summary` returns the
entry's `server_id` before using one: another AgentAPI may have taken the port.
[`agentapi list`](#agentapi-list) shows the registered instances with their summaries.

Set `--instance-id` to identify the instance (default `<hostname>:<port>`), `--name` for its display
name (default the working directory's name), and `--public-url` for its address. `PUT /webhook` can
change the ID and URL at runtime. Webhook events carry the same `instance` identity, so clients
using webhooks or direct connections can identify instances consistently.

For a browser client on another origin, add that origin to
[`--allowed-origins`](#allowed-origins). To send an [API token](#api-token-authentication), read the
stream with `fetch` and an `Authorization` header; `EventSource` can't set that header.

#### Running a hub

A hub watches many AgentAPIs on any machines it can reach. Run it somewhere that stays up, such as
an always-on workspace, and reach it and the AgentAPIs over a private network such as your own VPN.
The [hub guide](docs/hub.md) covers setup step by step, every state, tokens, troubleshooting and the
hub's API.

```bash
agentapi hub serve --token "$HUB_TOKEN" --secret "$HUB_SECRET" --instance-token "$AGENTAPI_TOKEN"
```

![The hub: every agent on the left, and an overview of what needs you, with a card per request](docs/screenshots/hub-desktop.png)

Open `http://<hub>:3290/` on a desktop or a phone and sign in with `--token`. The list shows every
instance, those that need you first. Each shows its state (the reason it waits, such as an
approval or question, a login or a usage limit; or working, finished a task you haven't seen, idle,
unreachable or stopped) with its title, agent, machine, queue and usage limit. The tab title counts
what needs you. Pick one to open it in the chat. On a phone the list and the chat take turns.

- AgentAPIs join it with [`agentapi hub join`](#joining-a-hub): their signed `instance.*` events
  add them, and the hub follows each one's [summary stream](#watching-many-instances) from one
  connection. `+` adds one by its URL (and token) instead. Each row's `⋯` menu puts the
  instance in a group of your own, mutes what it waits for until that changes (a login you won't
  do), or forgets a stopped or unreachable one.
- The hub opens an instance through itself (`/i/<id>/...`), sending the instance's API token
  (`--instance-token`, or the one given when it was added) in place of your credentials. The
  browser never holds the instances' tokens or reaches them itself, so no instance needs CORS for
  it.
- It accepts only events signed with `--secret` and at most 10 minutes old, and ignores a delivery
  it already received, because an event names the URL the hub sends an instance token to.
- It keeps the instances in `~/.agentapi/hub/instances.json` (`--state`, readable by its owner
  only), so it knows them after a restart.
- `--port` (default 3290) and `--bind` (such as the VPN address) set where it listens. Serve it
  over HTTPS, or only on the private network: signing in sends the token. To reach the instances
  through a VPN client running in userspace, set `ALL_PROXY` to its SOCKS5 proxy.
- The hub serves the chat of its own version: run the same AgentAPI version on the hub and the
  instances.

#### Joining a hub

[`agentapi hub join`](#agentapi-hub) connects a machine's AgentAPIs
to it without changing how they start, so it also works for AgentAPIs a script starts with fixed
flags, such as a Coder workspace's:

```bash
sudo agentapi update   # the latest release reads the config file
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" --public-url 'http://dev-1.vpn:{port}'
```

`join` writes the hub's webhook (`webhook-url`, `webhook-secret`, `webhook-events`, `public-url`)
into `~/.agentapi/config.yaml`, which every `agentapi server` of the user reads when it starts, and
applies it to the registered AgentAPIs running now: each sends `instance.heartbeat` to the hub at
once. After a restart (of the workspace, for example), the AgentAPIs send `instance.started` from
the file. `--public-url` is where the hub reaches them; `{port}` and `{hostname}` are filled in for
each. AgentAPIs that started before 1.2 aren't registered: restart them once. `agentapi hub status`
shows the hub and where each running AgentAPI reports; `agentapi hub leave` removes it. `join`
first sends the hub a signed test event and stops if the hub doesn't take it (not reached, a sign-in
page in front of it, or another secret); `--no-check` skips that for a hub that doesn't run yet. An agent can
do it for you with the [agentapi-hub-join skill](skills/agentapi-hub-join/SKILL.md); see the
[hub guide](docs/hub.md#3-join-each-machines-agentapis) to install it.

### Webhooks

AgentAPI POSTs JSON events to `--webhook-url`, for example to a dashboard monitoring several agents.
It sends only `run.status_changed` by default. Select events with `--webhook-events` by type
(`task.completed`), group (`task.*`) or all events (`*`):

| Event | Sent when | `data` |
| --- | --- | --- |
| `run.status_changed` | The agent changes between `running` and `stable` | `status`, `previous_status`, `server_id`, `agent_type`, `transport` |
| `instance.started`, `instance.heartbeat`, `instance.stopped` | AgentAPI starts, every `--webhook-heartbeat` (60s) and when `PUT /webhook` points the webhook at a new URL, and shuts down | What the agent does: `status`, `lifecycle`, `session_name`, `pending_requests`, `queued_messages`, and `agent_session_id`, `terminal_prompt`, `login_required`, `limit_stop`, `usage`, `settings` when set |
| `task.started` | A [task](#tasks) starts: a message of yours reaches the agent | The task as `GET /tasks` has it: `id` (the message's), `status`, `prompt`, `started_at` |
| `task.completed`, `task.failed` | The [task](#tasks) ends: `completed`, or `failed` and `limited` (`task.failed`) | The task with `status`, `ended_at`, `duration_seconds`, the agent's last message (`reply`, `reply_id`), `steered`, `errors`, `limit`, `session_name`, and for native Claude Code, Codex and Pi its API-equivalent `cost` (see [Session costs](#session-costs)) |
| `request.opened`, `request.closed` | An approval, question or plan (native; the request as `GET /requests` has it), or a terminal dialog (PTY; `kind` `terminal_prompt` with the `prompt`), opens or closes | The request |
| `login.required`, `login.completed` | The native agent needs a login, or no longer does | `agent`, `message`, `command` to run |
| `limit.stopped`, `limit.cleared` | A usage limit stopped a turn, or the stop is over | `message`, `stopped_at`, `resets_at` |
| `agent.error` | The agent reported an error or a warning | `message`, `level`, `time` |
| `message.created` | A message of yours or a final message of the agent's | `id`, `role`, `content` (cut at 64 KiB, with `truncated`), `time` |
| `tool.completed` | A tool call returned | `tool_use_id`, `name`, `input`, `result` and `is_error`, each cut at 4 KiB |

Every event names the AgentAPI it comes from, so one receiver can tell many apart:

```json
{
  "id": "unique-delivery-id",
  "type": "task.completed",
  "created_at": "2026-10-22T12:00:00Z",
  "instance": {
    "id": "my-host:3284",
    "server_id": "agentapi-run-id",
    "hostname": "my-host",
    "port": 3284,
    "pid": 4242,
    "cwd": "/home/me/project",
    "agent_type": "claude",
    "transport": "native",
    "agentapi_version": "1.0.0",
    "url": "https://agent.example.com",
    "workspace_url": "https://coder.example.com/@me/dev"
  },
  "data": {
    "id": 4,
    "prompt": "Fix the failing test",
    "started_at": "2026-10-22T11:58:30Z",
    "ended_at": "2026-10-22T12:00:00Z",
    "duration_seconds": 90,
    "status": "completed",
    "reply_id": 5,
    "reply": "Fixed: the test compared times in two time zones.",
    "cost": {"usd": 0.42, "tokens": {"input": 12, "cache_read": 81000, "cache_write": 4200, "output": 1900}, "calls": 6},
    "session_name": "Fix the failing test"
  }
}
```

The instance `id` is `--instance-id`, by default `<hostname>:<port>`, and `name` is `--name`, by
default the working directory's name. `url` is `--public-url`, where the receiver can reach this
AgentAPI to open its chat or call its API, and `workspace_url` the Coder workspace it runs in, if
any. Messages and tool calls from before AgentAPI
started, or of a session it opened from the agent's history, are not sent again.

```bash
agentapi server \
  --webhook-url https://dashboard.example.com/agentapi/events \
  --webhook-events 'task.*,request.*,login.*,limit.*,instance.*' \
  --webhook-secret "$WEBHOOK_SECRET" \
  --public-url https://agent.example.com \
  -- claude
```

Each request includes `X-AgentAPI-Delivery`, `X-AgentAPI-Event`, `X-AgentAPI-Timestamp` and
`X-AgentAPI-Instance` headers. With `--webhook-secret`, `X-AgentAPI-Signature-256` signs it:
`sha256=` and the hex HMAC-SHA256, keyed with the secret, of the timestamp, a `.` and the body. A
receiver checks it, and that the timestamp is recent, before trusting the event:

```python
expected = "sha256=" + hmac.new(secret, f"{timestamp}.".encode() + body, hashlib.sha256).hexdigest()
valid = hmac.compare_digest(expected, signature) and abs(time.time() - int(timestamp)) < 300
```

Events are delivered in order, one at a time. Network errors, timeouts and HTTP 5xx, 408 or 429
responses trigger retries after 1, 2, 4… seconds, capped at 30 seconds. Other 4xx responses aren't
retried. Each delivery allows `--webhook-max-attempts` (3) attempts, each with a
`--webhook-timeout` (10s) timeout; exhausted deliveries are logged and discarded.

The queue holds 1,024 events and drops new ones when full. A receiver that misses events should
read the current state from the API or the next heartbeat. On shutdown, AgentAPI waits up to
5 seconds to deliver `instance.stopped`.

An optional Go `text/template` (`--webhook-payload-template`) reshapes the body for the receiver,
such as a chat service's incoming webhook. Available fields are `.ID`, `.Type`, `.CreatedAt`,
`.Instance` and `.Data` (the event's data with its JSON field names, such as `{{.Data.prompt}}`), and
for `run.status_changed` `.ServerID`, `.Status`, `.PreviousStatus`, `.AgentType` and `.Transport`.
A template that uses another field (such as `.RunID`, named `.ServerID` since 1.0) is refused at
startup and by `PUT /webhook`, since it would fail every delivery.

All flags have `AGENTAPI_` environment variables (`AGENTAPI_WEBHOOK_URL`, `AGENTAPI_WEBHOOK_EVENTS`
with the events separated by spaces, `AGENTAPI_WEBHOOK_SECRET`, and so on). `GET /webhook` shows the
configuration (whether a secret is set, never the secret) and `PUT /webhook` changes it without a
restart; its `events`, `secret`, `heartbeat_seconds`, `instance_id` and `public_url` are kept when
left out.

The Explorer's Webhook tab configures the receiver URL, event groups, signing secret, instance ID,
heartbeat and public URL. **Delivery and payload** contains the timeout, attempt count and template.
You can configure a webhook here even if AgentAPI started without one.

Changes made here or through `PUT /webhook` last until AgentAPI restarts. With
[`--state-db`](#persistent-sessions), they survive restarts unless different webhook flags override
them. **Send test event** (`POST /webhook/test`) sends one signed `webhook.test` event to the saved
URL, regardless of the event selection, and shows the receiver's response.

![The Explorer's Webhook tab: the receiver URL, event groups, signing secret, instance ID, heartbeat and public URL](docs/screenshots/explorer-webhook.png)

The run-status event, which AgentAPI sends when `--webhook-events` isn't given:

```json
{
  "id": "unique-delivery-id",
  "type": "run.status_changed",
  "created_at": "2026-07-26T12:00:00Z",
  "instance": {"id": "my-host:3284", "server_id": "agentapi-run-id", "agent_type": "claude", "transport": "pty"},
  "data": {
    "server_id": "agentapi-run-id",
    "status": "stable",
    "previous_status": "running",
    "agent_type": "claude",
    "transport": "pty"
  }
}
```

### Human-reply API

Another client, such as a chat bot, can answer the agent without the web UI. While the agent waits
for you, `GET /handoff` returns what it waits on, with an `id` and a `kind`: its latest response
(`message`), the terminal prompt it is showing (`terminal`) or, with the native transport, the
oldest pending request (`request`). Otherwise it returns `{"request": null}`.

`POST /handoff/reply` with that `request_id`, a unique reply `id` and the `content` answers it. A
reply to a response is sent as a normal user message, and a reply to a request answers it as
described under [Requests](#requests-approvals-questions-and-plans). A terminal prompt takes a
displayed option number, `enter` or `esc`, or free text where the prompt has a text field (such as
Claude Code's plan feedback). The `outcome` is `applied`, `superseded` (no longer open, for example
because it was answered in the web UI), `invalid` (the reply doesn't fit; it stays open) or
`uncertain`, and nothing is answered twice. Combined with the webhooks above, an
integration can tell you when the agent stops and pass your answer back.

### MCP management

You can manage Claude, Codex and Pi MCP servers while AgentAPI is running. AgentAPI preserves
unrelated settings in `.mcp.json` (Claude), `$CODEX_HOME/config.toml` (Codex) or
`~/.pi/agent/mcp.json` (Pi).

The API and Session Explorer support:

- reading or replacing the complete MCP server map;
- creating, updating, and deleting individual servers;
- checking remote HTTP connectivity and resolving local stdio executables;
- saving, importing, exporting, and applying reusable MCP profiles;
- optionally restarting the agent to apply changes immediately.

When an agent is restarted, AgentAPI and its HTTP/SSE clients remain online. A PTY agent receives a
new process and session-log watcher, although its previous in-memory conversation context is not
retained. For MCP changes, a native agent reloads the same session and preserves the conversation
(an agent that can't resume its session, with `resume` false, starts a new session instead, and the
conversation is cleared). A reload during an active turn returns 409 because it would interrupt that
turn. Before a restart from the Explorer or `DELETE /messages`, AgentAPI updates Codex
(`codex update`) and Pi (`pi update --self`) when launching those CLIs directly. If an update
fails, AgentAPI restarts the installed version. Claude Code manages its own updates.

## Security

AgentAPI has no users or roles: whoever can reach the API can do what the agent can. Out of the box
it takes no token and accepts any connection whose `Host` header names the machine itself
(`localhost`, `127.0.0.1`, `[::1]`), on every network interface. The host check keeps web pages
from calling it through DNS rebinding; it doesn't stop a program on the network, which can send any
`Host` header. Protect the server before anyone else can reach the port. When it takes no token and
listens on an address other machines can reach, AgentAPI logs a warning at startup.

### API token authentication

All API endpoints can be protected with a Bearer token. Authentication is **disabled by default**
for backward compatibility.

```bash
# Auto-generate a random token (printed to stderr on startup)
agentapi server --api-token -- claude

# Use a specific token
agentapi server --api-token=my-secret -- claude

# Via environment variable
AGENTAPI_API_TOKEN=my-secret agentapi server -- claude
```

When enabled, every API request must include `Authorization: Bearer <token>` or the chat UI's
session cookie. Static file routes (`/`, `/chat/*`) are exempt so browsers can open the chat UI
without a token. Tokens are compared in constant time.

The chat UI signs in by trading the token for a cookie, since browsers can't add an `Authorization`
header to the event stream or to images:

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
(`agentapi_session_<hash>`, HttpOnly, SameSite=Strict, `Path=/`, and Secure over HTTPS or behind a
proxy that sends `X-Forwarded-Proto: https`). It holds an HMAC of the token, not the token, so
changing the token signs every browser out. Its name comes from the token, so servers with
different tokens on one host keep their own sign-ins. `DELETE /auth/session` signs out, and
`GET /auth/session` (no credentials needed) reports `auth_required` and `authenticated`. Because
the cookie is SameSite=Strict, pages on other sites can't make requests with it. A chat UI served
from another origin (e.g. `bun run dev` on port 3000) needs that origin in `--allowed-origins`;
`--allowed-origins '*'` doesn't allow requests with cookies. A different port is not a different
site, and the default allowed origins include `http://localhost:3000` and `http://localhost:3001`
for chat development. On a shared machine, set `--allowed-origins` to the origins you use, so a
signed-in browser's cookie only works for them.

### Bind address

By default, the server listens on all network interfaces. Use `--bind` (or `AGENTAPI_BIND`) to
listen on one address only, e.g. loopback so nothing else on the network can connect. It takes an IP
address or hostname without a port; the port stays `--port`.

```bash
agentapi server --bind 127.0.0.1 -- claude
agentapi server --bind ::1 -- claude          # IPv6 loopback
AGENTAPI_BIND=127.0.0.1 agentapi server -- claude
```

`--bind` decides who can connect; `--allowed-hosts` below decides which `Host` headers are accepted
from those who can.

### Allowed hosts

By default, the server only allows requests with the `Host` header set to `localhost`, `127.0.0.1`
or `[::1]`. To host AgentAPI elsewhere, set `AGENTAPI_ALLOWED_HOSTS` or `--allowed-hosts`.
Hosts must be hostnames only (no ports); the server ignores the port portion of incoming requests
when authorizing. This checks the `Host` header only; to keep the server off the network
altogether, also listen on loopback with `--bind 127.0.0.1` (see [Bind address](#bind-address)).

To allow requests from any host, use `*` as the allowed host.

```bash
agentapi server --allowed-hosts '*' -- claude
```

To allow a specific host, use:

```bash
agentapi server --allowed-hosts 'example.com' -- claude
```

To specify multiple hosts, use a comma-separated list when using the `--allowed-hosts` flag, or a
space-separated list when using the `AGENTAPI_ALLOWED_HOSTS` environment variable.

```bash
agentapi server --allowed-hosts 'example.com,example.org' -- claude
# or
AGENTAPI_ALLOWED_HOSTS='example.com example.org' agentapi server -- claude
```

### Allowed origins

By default, the server allows CORS requests from `http://localhost:3284`, `http://localhost:3000`,
and `http://localhost:3001`. Set `AGENTAPI_ALLOWED_ORIGINS` or `--allowed-origins` to change which
origins may make cross-origin requests.

To allow requests from any origin, use `*` as the allowed origin:

```bash
agentapi server --allowed-origins '*' -- claude
```

To allow a specific origin, use:

```bash
agentapi server --allowed-origins 'https://example.com' -- claude
```

To specify multiple origins, use a comma-separated list when using the `--allowed-origins` flag, or
a space-separated list when using the `AGENTAPI_ALLOWED_ORIGINS` environment variable. Origins must
include the protocol (`http://` or `https://`) and support wildcards (e.g.,
`https://*.example.com`):

```bash
agentapi server --allowed-origins 'https://example.com,http://localhost:3000' -- claude
# or
AGENTAPI_ALLOWED_ORIGINS='https://example.com http://localhost:3000' agentapi server -- claude
```

### What an API client can do

- Have the agent run anything it can. Native agents start in the bypass
  permission mode, where tools run without asking, unless the command line
  picks another mode or Claude Code runs as root outside a sandbox (it then
  starts in `default`). Claude Code gets `--allow-dangerously-skip-permissions`
  (except as root outside a sandbox), so a client can switch any session to
  bypass. Codex's default overrides the policies in `~/.codex/config.toml`
  unless the command line picks one, and the ACP transport approves every
  permission request.
- Run commands in the [terminal](#terminal) (native transport, on by default;
  `--terminal=false` turns it off).
- Read files in the agent's working directory with the file viewer endpoints;
  paths outside it, even through symlinks, are refused, and the diff only
  reads the git repository. An HTML preview's scripts run in a sandbox
  without access to the chat or the API. `GET /files/find` lists the paths of
  files below the working directory whose path ends with the one asked for.
- Read the conversations, subagent steps and costs of the agent's earlier
  sessions in its working directory from the agent's own logs (`~/.claude`,
  `~/.codex`, `~/.pi`), and open any of those sessions.
- Upload files of up to 10 MB; only images are served back.
- Change the MCP servers and the webhook, and restart the agent. The webhook can send the
  conversation (`message.created`, `tool.completed`) to any URL; `GET /webhook` never returns its
  secret.

Use `--api-token` together with `--bind` or a reverse proxy whenever someone else can reach the
server.

## Resource usage

AgentAPI itself is small: one Go process of about 30 MiB that uses about 0.1% of a CPU core while
the agent is idle. Most of the cost is the agent CLI it runs, at 100–350 MiB.

These numbers were measured on 2026-10-18 with AgentAPI v0.24.0 (a 29.5 MiB binary), Claude Code
2.1.302, codex-cli 0.163.0 and Pi 1.3.0, on Linux with a 4-core Intel Xeon Silver 4210 (2.2 GHz)
and one chat (an SSE client) connected. Each case had one short turn: a reply of about 200 words,
without tools. Memory is resident memory (RSS); CPU is a share of one core while idle, and CPU
seconds for a whole turn.

| Transport | Agent | AgentAPI memory | AgentAPI CPU, idle | AgentAPI CPU per turn | Agent memory | Agent CPU per turn |
| --- | --- | --- | --- | --- | --- | --- |
| Native | Claude Code | 27–30 MiB | 0.1% | 0.08 s | 230–290 MiB | 1.1 s |
| Native | Codex | 27–28 MiB | 0.1% | 0.11 s | 205–265 MiB | 0.43 s |
| Native | Pi | 27–28 MiB | 0.1% | 0.12 s | 100–120 MiB | 0.37 s |
| PTY | Claude Code | 29–34 MiB | 0.15% | 0.29 s | 265–300 MiB | 1.4 s |
| PTY | Codex | 34 MiB | 0.2–0.7% | 0.66 s | 245–345 MiB | 6.3 s |
| PTY | Pi | 28–35 MiB | 0.1% | 0.59 s | 120–130 MiB | 1.2 s |
| Native | `native-demo` (no agent process) | 27 MiB | 0.03% | 0.07 s | | |

- While the agent is idle, both transports cost AgentAPI next to nothing: polling the screen
  (PTY) costs about as little as waiting for the protocol (native). The exception was Codex with
  the PTY transport (0.2–0.7% in the table): to find the session's log, AgentAPI read Codex's
  recent session files every 500 ms, more of them before the first message. It now reads each file
  once, and AgentAPI idles at 0.1% with PTY Codex too (measured after the fix).
- A turn costs AgentAPI 3–6 times more CPU with the PTY transport (typing the message, reading the
  screen, following the session log), and the agent too: Codex's terminal UI took 6.3 CPU seconds
  for a turn its app-server answered in 0.4.
- An agent's own memory moves during a session: Claude Code grew to 314 MiB about a minute and a
  half after it started (its own background work, 0.1% CPU idle otherwise), and Codex gave back
  about 55 MiB after its first turn.
- AgentAPI's code (about 17 MiB of its memory) is shared by instances started from the same binary:
  with four running, each costs about 13–24 MiB (PSS).
- Not counted: MCP servers the agent starts, the commands it runs, the shell terminal (one shell
  while it is open) and the browser tab.

To size a machine, plan per session about 30 MiB for AgentAPI plus the agent (about 350 MiB for
Claude Code or Codex, 150 MiB for Pi) and whatever its MCP servers and commands need.

## CLI reference

### `agentapi server`

Start an HTTP server to control an agent. To pass startup arguments to the agent, place the full
agent command after the `--` separator.

```bash
agentapi server -- claude --allowedTools "Bash(git*) Edit Replace"
```

You may also use `agentapi` to run the Aider and Goose agents:

```bash
agentapi server -- aider --model sonnet --api-key anthropic=sk-ant-apio3-XXX
agentapi server -- goose
```

Claude Code, Codex and Pi run with the [native transport](#native-transport) unless you pass
`--transport pty` for their terminal UI (see [Pi coding agent](#pi-coding-agent)):

```bash
agentapi server -- pi
agentapi server --transport pty -- pi
```

Kimi Code can run through its interactive terminal UI:

```bash
agentapi server -- kimi
```

Kimi Code also provides a native ACP server. After logging in once with `kimi` and `/login`, you
can use AgentAPI's ACP transport:

```bash
agentapi server --type=kimi --experimental-acp -- kimi acp
```

#### Flags

Flags can also be set with environment variables: `AGENTAPI_` and the flag's name in capitals, with
`_` for `-` (`AGENTAPI_ALLOWED_HOSTS`). Lists are comma-separated in a flag and space-separated in a
variable.

They can also be set by the flag's name in `~/.agentapi/config.yaml` (`--config` names another
file) for every server the user starts; a flag or a variable overrides the file. A setting that
names no flag is reported in the log, and a file AgentAPI can't read is reported and ignored, so the
agent still starts. [`agentapi hub join`](#agentapi-hub) writes it; keep it readable by its owner
only (`chmod 600`) when it holds a secret.

```yaml
webhook-url: http://hub.vpn:3290/hooks
webhook-secret: s3cret
webhook-events: [instance.*, task.*, request.*]
public-url: http://dev-1.vpn:{port}
```

| Flag | Default | Description |
| --- | --- | --- |
| `-t`, `--type` | from the agent command | Agent type; see [Supported agents](#supported-agents) |
| `-p`, `--port` | `3284` | Port to listen on |
| `--bind` | all interfaces | Address to listen on; see [Bind address](#bind-address) |
| `--transport` | `native` for Claude Code, Codex and Pi, else `pty` | `native`, `pty` or `acp`; see [Transports](#transports) |
| `--experimental-acp` | `false` | The same as `--transport acp` |
| `--state-db` | | SQLite file that keeps the conversation (native and PTY) and resumes a native agent's session; see [Persistent sessions](#persistent-sessions) |
| `--terminal` | `true` | Shell in the agent's working directory for the chat (native); see [Terminal](#terminal) |
| `--auto-continue` | `false` | Continue a turn stopped by a usage limit once the limit resets (native); see [Usage limits](#usage-limits) |
| `--prices` | | JSON file of model prices per million tokens, which add to or replace the built-in ones; see [Session costs](#session-costs) |
| `-s`, `--state-file` | | Deprecated: imported once into a state database next to it (`--state-db` names another) |
| `--load-state`, `--save-state` | | Deprecated: `--load-state=false` skips the import; `--save-state` has no effect |
| `-I`, `--initial-prompt` | | First message for the agent; read from stdin when piped |
| `-W`, `--term-width` | `80` | Width of the emulated terminal, and of TTY mode's lines (PTY) |
| `-H`, `--term-height` | `1000` | Height of the emulated terminal (PTY) |
| `--api-token` | off | Require a Bearer token; without a value, a random token is generated and printed; see [API token authentication](#api-token-authentication) |
| `-a`, `--allowed-hosts` | `localhost`, `127.0.0.1`, `[::1]` | Accepted `Host` headers, `*` for any; see [Allowed hosts](#allowed-hosts) |
| `-o`, `--allowed-origins` | `http://localhost:3284`, `http://localhost:3000`, `http://localhost:3001` | Origins allowed to call the API from a browser, `*` for any; see [Allowed origins](#allowed-origins) |
| `--webhook-url` | | URL the webhook events are POSTed to; see [Webhooks](#webhooks) |
| `--webhook-events` | `run.status_changed` | Events to send: types, groups (`task.*`) or `*` |
| `--webhook-secret` | | Secret that signs every delivery (`X-AgentAPI-Signature-256`) |
| `--webhook-heartbeat` | `60s` | How often `instance.heartbeat` is sent; `0` turns it off |
| `--instance-id` | `<hostname>:<port>` | Identifies this AgentAPI in `GET /summary` and every webhook event; see [Watching many instances](#watching-many-instances) (`--webhook-instance-id` is its deprecated old name) |
| `--name` | the working directory's name | What clients that watch many AgentAPIs call this one |
| `--registry` | `true` | Keep an entry in the registry directory while serving; see [Watching many instances](#watching-many-instances) |
| `--registry-dir` | `~/.agentapi/instances` | Registry directory |
| `--public-url` | | URL this AgentAPI is reached at, in `GET /summary` and every webhook event; `{port}` and `{hostname}` are filled in |
| `--config` | `~/.agentapi/config.yaml` | Settings file: flags by name, below flags and variables |
| `--webhook-timeout` | `10s` | Timeout of each delivery attempt |
| `--webhook-max-attempts` | `3` | Delivery attempts |
| `--webhook-payload-template` | | Go `text/template` for the POST body |
| `-c`, `--chat-base-path` | `/chat` | Base path of the chat UI |
| `--pid-file` | | File to write the server's process ID to, for shutdown scripts |
| `-P`, `--print-openapi` | | Print the OpenAPI schema and exit |

### `agentapi update`

Update the AgentAPI binary to the latest release from GitHub.

```bash
agentapi update          # download and install the latest version
agentapi update --check  # only check if an update is available
agentapi update --force  # update even if already at the latest version
```

Release binaries are verified against the release's `checksums.txt` SHA-256 manifest before the
running executable is replaced. An update is rejected if the manifest is missing, malformed, or does
not match the download.

### `agentapi list`

List the AgentAPIs running on this machine from the [registry](#watching-many-instances), with
what each `GET /summary` says: idle, working, what it needs from you (`needs request`), and an
unread task.

```bash
agentapi list
NAME      AGENT   STATE                  URL                    TITLE
api       claude  needs request, unread  http://localhost:3284  Fix the failing test
frontend  codex   working                http://localhost:3285  Add the settings page
```

`--json` prints the entries and summaries as JSON, `--registry-dir` reads another directory, and
`--api-token` (or `AGENTAPI_API_TOKEN`) is sent to the instances that need a token.

### `agentapi hub`

Run a hub that watches many AgentAPIs, or connect this machine's AgentAPIs to one; see
[Running a hub](#running-a-hub) and [Joining a hub](#joining-a-hub).

```bash
agentapi hub serve --token "$HUB_TOKEN" --secret "$HUB_SECRET" --instance-token "$AGENTAPI_TOKEN"
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" --public-url 'http://dev-1.vpn:{port}'
agentapi hub status
agentapi hub leave
```

`serve` runs the hub: `--token` (or `AGENTAPI_HUB_TOKEN`) is what you sign in with (`--no-token`
instead skips signing in, for a hub behind a sign-in of its own such as a Coder workspace app),
`--secret` (or `AGENTAPI_HUB_SECRET`) checks the AgentAPIs' events, `--instance-token` (or
`AGENTAPI_HUB_INSTANCE_TOKEN`, else `AGENTAPI_API_TOKEN`) is sent to them, `--state` keeps the
instances, and `--port` and `--bind` set where it listens.

`join` writes the hub's webhook into `~/.agentapi/config.yaml` and applies it to the registered
AgentAPIs running now. `--secret` (or `AGENTAPI_HUB_SECRET`) signs the events, `--events` picks them
(default `instance.*`, `task.*`, `request.*`, `login.*`, `limit.*`, `agent.error`), `--public-url`
is where the hub reaches them, `--api-token` (or `AGENTAPI_API_TOKEN`) is sent to the ones that need
a token, and `--config` and `--registry-dir` name other files. An AgentAPI that `join` can't
update now applies the file when it restarts.

### `agentapi attach`

Attach to a running agent's terminal session (PTY transport; ACP and native agents have no terminal
to attach to).

```bash
agentapi attach --url localhost:3284
```

A server started with `--api-token` needs the token: pass `--api-token` or set
`AGENTAPI_API_TOKEN`. Press `Ctrl+C` to detach from the session.

## HTTP API reference

The server describes its API with OpenAPI: [openapi.json](openapi.json) in this repository,
`/openapi.json` on a running server, and a documentation UI at `/docs`. It listens on port 3284 by
default. Endpoints marked *native* need the native transport.

### Conversation

| Endpoint | Description |
| --- | --- |
| `GET /messages` | All messages of the conversation |
| `POST /message` | Send a message, or queue it while the agent is busy; `dispatch_mode` is `queue` (default), `steer` or `interrupt` (*native*) |
| `DELETE /messages` | Clear the conversation and the queue, and restart the agent (*native*: in a new session; 400 with ACP) |
| `GET /rich-messages` | The structured conversation: thinking, tool calls with results, usage |
| `GET /timeline` | Normalized session events, for export or another UI; `?subagents=true` adds the subagents' (*native*) |
| `GET /status` | Status, lifecycle, server ID, turn, epoch, session name, version, open dialog; *native*: the agent's session ID, capabilities, pending requests, settings, usage, usage limit stop, terminal, login |
| `GET /title` | The session's human-readable title |
| `GET /events` | Server-sent events; `?sync=1` for incremental replay on reconnect, and task and queue updates; `?only=summary` for the summary alone |
| `GET /summary` | What a client that watches many AgentAPIs shows of this one: instance, status, attention, title, latest task, requests, queue, usage; see [Watching many instances](#watching-many-instances) |
| `GET /tasks` | The conversation's [tasks](#tasks): each message of yours and the agent's turn on it |
| `POST /tasks/seen` | Mark the tasks seen for every client: the summary's `unread` is false until a task ends again |
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
| `POST /limit/continue`, `DELETE /limit/continue` | Continue a turn stopped by a usage limit, now or at the reset; cancel a scheduled continuation |
| `DELETE /limit` | Forget a usage limit stop, so the queued messages go out |
| `GET /sessions` | The agent's sessions in its working directory (Claude Code, Codex, Pi), with what each cost and the total |
| `GET /cost` | What the open session cost, and its model calls (Claude Code, Codex, Pi) |
| `GET /subagents`, `GET /subagents/{id}` | The subagents the agent ran in its session, and one's task and messages (Claude Code, Codex) |
| `POST /sessions/{id}/resume` | Open one of them, or a copy of it (`{"fork": true}`), in place of the conversation |

### Terminal and files

| Endpoint | Description |
| --- | --- |
| `GET /terminal` | The shell's output as server-sent events (*native*, unless `--terminal=false`) |
| `POST /terminal/input`, `POST /terminal/resize` | Type into the shell; set its size |
| `GET /files/content` | A file in the agent's working directory, cut at 1 MiB |
| `GET /files/diff` | The file's uncommitted changes against HEAD |
| `GET /files/preview` | An HTML file with the files it refers to inlined, for a sandboxed iframe |
| `GET /files/image` | An image file in the agent's working directory (raster images judged by content, and SVG) |
| `GET /files/find` | The files below the working directory whose path ends with the one given, ignoring case |

### Uploads

| Endpoint | Description |
| --- | --- |
| `POST /upload` | Store a file (up to 10 MB) and return its path, for `@"<path>"` in a message |
| `GET /uploads/{checksum}/{name}` | An uploaded image (PNG, JPEG, GIF, WebP or BMP, judged by content), for the chat |

### Integration endpoints

| Endpoint | Description |
| --- | --- |
| `GET /handoff`, `POST /handoff/reply` | The [human-reply API](#human-reply-api): what the agent waits on, and an answer to it |
| `GET /webhook`, `PUT /webhook` | Read or change the [webhook](#webhooks): its URL, events, secret, heartbeat and instance |
| `POST /webhook/test` | Send a `webhook.test` event to the webhook URL once, and return the receiver's answer |
| `GET /mcp`, `PUT /mcp` | The MCP servers and the managed config file; PUT replaces them, `?restart=true` restarts the agent (*native*: on its session if it can resume it; 400 with ACP) |
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

The chat itself is served at `/chat/` (and `/chat/embed`), and TTY mode reads the agent's screen
from `/internal/screen`, which isn't part of the public API.

## Architecture

AgentAPI is a single Go binary: an HTTP server (chi, with huma generating the OpenAPI schema) that
runs one agent process and serves the chat UI, a Next.js app embedded in the binary. Every transport
implements the same conversation interface and reports to the same event emitter, so the HTTP API,
the event stream, the queue and the chat work the same over all three. See
[docs/architecture.md](docs/architecture.md) for the packages, the data flow of each transport, the
event stream, persistence and the security model.

## Development

```bash
make build      # out/agentapi, with the chat UI embedded
make gen        # regenerate openapi.json and the version
make lint       # Go, TypeScript, shellcheck and actionlint
go test ./...   # Go tests, including the e2e smoke test
```

Tests run with `CGO_ENABLED=0`. Commit `openapi.json` with every API change, and regenerate it only
with `make gen` (see [AGENTS.md](AGENTS.md)). The chat UI lives in [chat/](chat/README.md):
`bun run dev` serves it against a running AgentAPI, and
`agentapi server --type custom -- native-demo` runs a simulated native agent for working on it
without a real one.

## Runtime reliability

The fork fixes wide-character cursor tracking, PTY lifecycle leaks, concurrent event delivery,
message tracking races, ACP shutdown and TUI redraw artifacts. It also fixes JSONL watcher flushing,
content-block merging during Claude delta streaming, and switching the watched file when Claude
Code parks a session in a new JSONL file. These fixes address long sessions, process replacement
and temporary browser or network interruptions.

## Roadmap

Pending feedback, we're considering the following features:

- [Support the MCP protocol](https://github.com/coder/agentapi/issues/1)
- [Support the Agent2Agent Protocol](https://github.com/coder/agentapi/issues/2)

## Long-term vision

AgentAPI currently lets developers control coding agents programmatically. We hope the major agents
will eventually offer official SDKs. Whether AgentAPI is still needed will depend on whether vendors
adopt a common API or continue to use their own formats.

If vendors adopt a common API, we'll deprecate AgentAPI and recommend the official SDKs. If their
formats remain different, we'll work toward a universal adapter so developers can switch agents
without changing their code.
