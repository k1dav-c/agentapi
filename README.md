# AgentAPI

English | [繁體中文](README.zh-TW.md)

Control [Claude Code](https://github.com/anthropics/claude-code), [AmazonQ](https://aws.amazon.com/developer/learning/q-developer-cli/), [Opencode](https://opencode.ai/), [Goose](https://github.com/block/goose), [Aider](https://github.com/Aider-AI/aider), [Gemini](https://github.com/google-gemini/gemini-cli), [GitHub Copilot](https://github.com/github/copilot-cli), [Sourcegraph Amp](https://ampcode.com/), [Codex](https://github.com/openai/codex), [Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started), [Pi](https://pi.dev/), [Auggie](https://docs.augmentcode.com/cli/overview), and [Cursor CLI](https://cursor.com/en/cli) with an HTTP API.

![agentapi-chat](https://github.com/user-attachments/assets/57032c9f-4146-4b66-b219-09e38ab7690d)

You can use AgentAPI:

- to build a unified chat interface for coding agents
- as a backend in an MCP server that lets one agent control another coding agent
- to create a tool that submits pull request reviews to an agent
- and much more!

## Changes in this fork

This fork started from the upstream v0.12.2 codebase. It keeps the original
terminal-emulation and HTTP API model as the default, adds an opt-in
[native transport](#native-transport) that drives Claude Code, Codex and Pi
through their own structured protocols, and extends AgentAPI into a more
complete workspace for operating long-running coding agents.

### Session workspace and chat UI

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

The Session Explorer provides:

- links and file paths discovered in the current conversation;
- an index for jumping directly to earlier tasks;
- Markdown preview and export for individual tasks;
- access to the live terminal when the parsed conversation is not sufficient;
- MCP server and Temporal/Discord configuration without leaving the chat UI;
- a Session tab with a two-step Restart (open the Explorer with ⌘K / Ctrl+K).

Each message has its own markdown/raw toggle so you can switch render
modes without affecting the rest of the conversation.

Images can be pasted straight into the composer (a pasted screenshot is
uploaded like an attachment; pasted text still pastes as text). Image
attachments and the images in sent messages show as thumbnails that open full
size; the agent gets the file path.

The UI also includes improved mobile layouts, attachment handling, searchable
tool activity, connection-state indicators, and more compact tool-call cards.
The header shows the agent's mark (Claude's orange square, Pi's logo) and the
AgentAPI version.

**TTY mode** is an escape hatch for when the chat view looks wrong: the
terminal button in the header swaps the conversation for the agent's terminal
screen (xterm.js) and sends every key straight to the agent, including arrows,
Ctrl/Alt keys, pastes and IME input. A key bar provides Esc, Tab, arrows,
Enter and Ctrl keys on phones. The font grows with the window (11–20px); the
mirror uses the agent's real terminal width, so start AgentAPI with
`--term-width 120` if you want longer lines.

**Background alerts**: while the tab is in the background, its icon shows an
amber dot when the agent needs you, a blue dot while it works, and a green dot
when a task finished. Browser notifications can be turned on from the status
menu.

**Native transport** (`--transport native`): what the agent asks for (a tool
approval, its questions, a plan to approve) docks above the composer as a
card, and while the agent works the send button can also steer the message
into the running turn or interrupt it. There is no terminal, so TTY mode and
the other screen views are hidden (also for ACP). See
[Native transport](#native-transport).

### Message queue and connection recovery

Messages submitted while an agent is busy are placed in a FIFO queue instead
of being rejected. Queued messages can be inspected, edited, or deleted before
delivery through both the API and chat UI.

Long-running browser sessions are protected by SSE heartbeats, automatic
reconnection, stale-connection detection, and local recovery of messages that
failed before reaching the server. The document title and session header expose
the current task, agent status, and connection state.

Reopening a long conversation is fast: the chat caches the transcript in the
browser (IndexedDB), shows it immediately, and then asks the server only for
what changed since.

Relevant APIs include:

- `GET /queue` and `PUT/DELETE /queue/{id}` for queue management;
- `GET /events` for live messages, status, errors, rich activity, and heartbeat
  events. With `?sync=1` the stream starts with a `session_sync` event and
  every update carries a `seq`; reconnecting with `&since=<seq>&epoch=<epoch>`
  replays only what changed;
- `GET /title` for the current human-readable session title.

With `?sync=1`, events also carry SSE ids (the end of the replay has the
replay's `seq`, later changes their own), so a plain `EventSource` that
reconnects by itself resumes from its `Last-Event-ID`; put the `epoch` from
`GET /status` in the URL.

### Native transport

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
  streams (at most ten snapshots a second per message). For Claude Code,
  AgentAPI asks for summarized adaptive thinking; every Codex turn asks for
  detailed reasoning summaries; Pi's thinking streams as Pi writes it.
- `GET /status` reports `transport` (`native`), the agent's `capabilities` and
  `pending_requests`, and `status_change` events carry the transport too.
  While the agent waits only on a request, `status` is `stable`, as it is for
  a terminal dialog.

#### Logging in

Native mode uses the agent CLI's own stored login. There is no terminal, so
`/login` from the chat's TTY mode isn't available: log in once with the CLI
itself, on the machine and as the user account that runs AgentAPI (or once in
PTY mode).

- Claude Code: `claude auth login` (Claude subscription by default, `--console`
  for Anthropic Console API billing). `claude auth status` shows the login.
- Codex: `codex login` (ChatGPT plan), or
  `printenv OPENAI_API_KEY | codex login --with-api-key` for an API key.
  `codex login status` shows the login.
- Pi: `/login` inside `pi`, or a provider key such as `ANTHROPIC_API_KEY` in
  AgentAPI's environment.

#### Requests: approvals, questions and plans

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
  expired), 400 when the answer doesn't fit the request.
- Each request has a `status` (`pending`, `resolved`, `withdrawn`, `expired`)
  and a `response_capability`: `live` (the agent is waiting), `message` (the
  agent stopped waiting; answer with a new message) or `not_resumable` (the
  agent that asked exited or was restarted). When a turn ends, its open
  questions become `message` and its approvals and plans `not_resumable`.
- Answers to secret questions are stored and reported as `[redacted]`; only the
  agent gets them.
- The Discord bridge's `/handoff` offers the oldest pending request with
  numbered choices: a number picks an option, other text answers a single
  question or goes with the deny option as feedback.

In the chat, requests dock above the composer as a decision card. Approvals
show the command and options styled by what they do (allow, always allow,
deny), with optional feedback sent along with a denial; plans render as
Markdown and can be approved or sent back with feedback; questions take one
step each (single or multiple choice, typed or hidden answers) and are
submitted together. Number keys pick options. The tool call's row says whether
it waits for approval, was approved or was denied, and `AskUserQuestion`,
`request_user_input` and `ExitPlanMode` calls are answered on the card only. A
request answered elsewhere or no longer pending leaves the card, and a question
the agent stopped waiting for points out that a normal message can still
answer it. The header, tab title, tab icon and notifications say "Needs you"
while a request is pending.

#### Steer, interrupt and the queue

- `POST /message` takes `dispatch_mode`: `queue` (the default, as on every
  transport: send the message once the agent is idle), `steer` (add it to the
  turn the agent is working on; the reply says `steered`) or `interrupt` (stop
  that turn and send the message next). When no turn is running, both send the
  message as usual. They need the native transport and the matching
  capability, otherwise the answer is 400.
- `POST /interrupt` stops the running turn (409 if there is none).
- `POST /queue/{id}/steer` moves a queued message into the running turn (409
  if no turn is running; the message stays queued).
- `PUT /queue` with `{"ids": [...]}` reorders the queue. It must list every
  queued message once (409 otherwise). This works on every transport.

`GET /status` reports `capabilities`: `steer`, `interrupt`, `resume` (the
session can be resumed when AgentAPI starts again, see
[Persistent sessions](#persistent-sessions)) and `images` (image attachments,
`@"<path>"` references to PNG, JPEG, GIF or WebP files up to 10 MB, are sent
to the agent as images as well as paths).

In the chat, while the agent works, the send button's menu adds **Steer**
(⌘/Ctrl+Enter) and **Interrupt & send** (⌘/Ctrl+Shift+Enter) next to queueing
(Enter), and Stop interrupts the turn (`POST /interrupt`). Queued tasks can be
steered in right away, and moved up and down on every transport.

#### Persistent sessions

`--state-db <file>` keeps the conversation in a SQLite file: messages, rich
messages (tool calls, thinking), the timeline, requests and the queue, plus
the agent's own session ID. When AgentAPI starts again with the same file, the
chat shows the conversation as it was, queued messages are still queued, and
the agent resumes its session, so it remembers the conversation too. Browser
caches stay valid, because the conversation keeps its epoch. Without
`--state-db`, every start of AgentAPI is a new agent session.

```bash
agentapi server --transport native --state-db ~/.agentapi/myproject.db -- claude
```

- Requests that were open when AgentAPI stopped are marked expired: the agent
  that asked is gone.
- Uploads are kept next to the file (`<file>.uploads`), because messages refer
  to them by path.
- A file belongs to one agent type, transport and working directory; AgentAPI
  refuses to start with another one's file.
- Restart (Explorer → Restart, `DELETE /messages`) clears the stored
  conversation and starts a new session.
- Writes are batched every 200 ms, so streaming doesn't wait for the disk.
- `--state-db` needs `--transport native`. With the PTY transport the agent
  can't resume its session; `--state-file` still saves the screen-derived
  messages there. The native transport doesn't take `--state-file`.

#### Claude Code

AgentAPI runs `claude -p` headless with stream-json on stdin and stdout. Tool
calls carry their input, result, status and images, and each assistant message
has its model, stop reason and token usage.

- Approvals offer Allow, Deny (with optional feedback for Claude) and, when
  Claude Code suggests a permission rule, "Allow always", which applies the
  rule to this session only. Commands Claude Code considers read-only run
  without asking.
- Questions list Claude's options; any other answer is accepted too.
- Plans show the plan and offer "Yes, and auto-accept edits", "Yes, and
  manually approve edits" and "No, keep planning". Feedback sent with No makes
  Claude revise the plan and ask again.

Claude Code supports `steer` (a steered message joins the running turn at
Claude's next step), `interrupt`, `resume` and `images`. The session name is
the title Claude Code generates after the first prompt. A turn ends when
Claude Code reports it is idle, so a background subagent and the turn it wakes
up when it finishes belong to the turn that started it; the subagent's own
steps are left out of the conversation, as in the session log, and its tool
call shows as running until it reports.

Command line and environment:

- AgentAPI adds `-p --output-format stream-json --input-format stream-json
  --verbose --include-partial-messages --replay-user-messages
  --permission-prompt-tool stdio`, and `--session-id` for a new session or
  `--resume` for a resumed one.
- Unless you choose otherwise it adds `--permission-mode bypassPermissions`,
  so tools run without asking; questions (AskUserQuestion) and plans
  (ExitPlanMode) still come as requests, and approving a plan offers to keep
  bypassing. Pass `--permission-mode default` (or `acceptEdits`, `plan`, …) to
  approve tool calls from the chat. It also adds `--thinking adaptive
  --thinking-display summarized` (without it thinking is empty). Your
  `--permission-mode`, `--dangerously-skip-permissions`, `--thinking`,
  `--max-thinking-tokens` or `--thinking-display` wins.
- Flags that conflict with the protocol or the session AgentAPI manages are
  rejected at startup: `-p`/`--print`, `--output-format`, `--input-format`,
  `-r`/`--resume`, `-c`/`--continue`, `--session-id`, `--fork-session`,
  `--from-pr`, `--teleport`, `--permission-prompt-tool`,
  `--permission-prompts`, `--bg`/`--background`, `--cloud`, `--desktop` and
  `--remote-control`. Your own copies of `--verbose`,
  `--include-partial-messages` and `--replay-user-messages` are dropped.
  Everything else (`--model`, `--allowedTools`, `--mcp-config`,
  `--append-system-prompt`, ...) is passed through.
- Started from inside a Claude Code session (for example by its Bash tool),
  the child would inherit that session's variables. AgentAPI removes
  `CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID`,
  `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_SESSION_ATTENDED`,
  `CLAUDE_CODE_EXECPATH`, `CLAUDE_CODE_MESSAGING_SOCKET`,
  `CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_CODE_SSE_PORT`, `CLAUDE_PID`,
  `CLAUDE_EFFORT`, `CLAUDE_AGENT_SDK_VERSION`, `AI_AGENT` and
  `CLAUDE_CODE_SDK_READS_SESSION_STATE`, and sets
  `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and
  `CLAUDE_CODE_ENTRYPOINT=sdk-cli`. Configuration such as `CLAUDE_CONFIG_DIR`
  and `ANTHROPIC_*` is kept.

Resuming a session keeps Claude's context; Claude Code doesn't replay the
earlier messages, so the chat shows them only from the `--state-db` file. A
session can be resumed once it had a turn; with `--no-session-persistence` it
can't, and `resume` is false. Requests still open when Claude Code exits or
restarts can't be answered anymore. Stopping closes Claude Code's input so it
finishes on its own, then sends SIGTERM after 3 s and SIGKILL 2 s later. Slash
commands that need the interactive UI don't work.

#### Codex

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
  danger-full-access`. Without them, Codex's config decides (including `-c
  sandbox_mode=...`). Codex 0.160 refuses `approval_policy = "untrusted"` in
  its config, so use `-a untrusted` for that policy.
- `--no-alt-screen`, `--no-daemon` and `--stdio` are ignored.
- A prompt, a subcommand (other than `app-server`), `-p`/`--profile`,
  `-i`/`--image`, `--oss`, `--local-provider`, `--search`, `--add-dir`,
  `--approve-for-me`, `--dangerously-bypass-hook-trust`, `--remote`,
  `--remote-auth-token-env`, `--worktree`, `--listen`, `--help`, `--version`
  and unknown flags are rejected at startup with an explanation. Use
  `--initial-prompt` for a first message and attach images to messages
  instead.

Codex supports `steer`, `interrupt`, `resume` and `images`. Command, file-edit
and MCP tool approvals, permission requests, MCP form elicitations and
`request_user_input` questions (plan mode) arrive as requests; their option
IDs are Codex's own decisions (`accept`, `acceptForSession`,
`acceptWithExecpolicyAmendment`, `decline`, `cancel`, …), nothing is approved
automatically, and requests AgentAPI can't show are declined. A steered message
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
starts, so restart the agent to apply changes.

Slash commands (`/model`, `/review`, …) are not available. Command output
streams as a preview that Codex may leave incomplete, and the full output
replaces it when the command ends. Variables a parent Codex session sets
(`CODEX_THREAD_ID`, `CODEX_SANDBOX`, …) are removed from app-server's
environment.

#### Pi

AgentAPI runs Pi in its RPC mode (`pi --mode rpc`, JSON over stdin and
stdout).

- **Flags.** AgentAPI adds `--mode rpc --session-id <id>` (a new
  `agentapi-<hex>` ID for every new session, including after a restart). It
  refuses options that pick the mode or the session or make Pi exit (`--mode`,
  `--print`/`-p`, `--session`, `--session-id`, `--continue`/`-c`,
  `--resume`/`-r`, `--fork`, `--no-session`, `--export`, `--list-models`,
  `--help`/`-h`, `--version`/`-v`), `@file` arguments, messages after `--` and
  subcommands (`pi install`, `remove`, `uninstall`, `update`, `list`,
  `config`, `auth`, `mcp`), with an error that names the option. It drops the
  TUI-only `--tui-mode` and `--use-theme`. Everything else is passed on
  (`--provider`, `--model`, `--thinking`, `--session-dir`, `-e`, `--approve`,
  extension flags); `--flag=value` is split for Pi's built-in options, which
  Pi only reads in the two-token form. Set the thinking level with
  `--thinking`.
- **Capabilities.** Steer (`dispatch_mode: "steer"` is sent as a prompt with
  `streamingBehavior: "steer"`), interrupt (`POST /interrupt` sends `abort`; the
  turn ends as interrupted, without the "This operation was aborted" error Pi
  adds when it aborts during a tool), resume, and images (attached images go to
  Pi as base64 `images`; reported only when the model accepts images).
- **Messages.** Text and thinking stream into the rich messages as they arrive,
  bash output streams into the tool result, and the final messages have the
  same shape as the ones parsed from Pi's session log. Images in tool results
  (an image file Pi reads) are kept.
- **Requests.** Stock Pi has no tool approvals. Dialogs that extensions open
  (`select`, `confirm`, `input`, `editor`) become requests: a `select` or
  `confirm` while a tool runs is an approval of that tool call (option IDs are
  the extension's labels), any `confirm` takes `yes`/`no`, and other dialogs
  are questions (`choice` or `value`; option `cancel` dismisses them). A dialog
  Pi times out is withdrawn. An extension's `notify` with level error shows as
  an error, and its `setTitle` stands in for the session name until Pi has one;
  Pi's own name (`--name`, renames) is the session name.
- **Resume.** Pi's session ID is the session reference. Resuming starts Pi with
  the same `--session-id` (keep the same directory and `--session-dir`), so Pi
  keeps the context; Pi doesn't replay earlier messages, so the chat shows them
  only from the `--state-db` file.
- **Shutdown.** Stopping AgentAPI (or a restart) aborts a running turn first,
  so the session log doesn't end in a tool call without result, then closes
  stdin, then sends SIGTERM and SIGKILL to Pi's process group if it doesn't
  exit.
- **Environment and commands.** Pi's TUI commands (`/settings`, `/login` and
  the like) are unavailable; extension slash commands work. Variables that
  mark a parent agent session (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`,
  `CODEX_THREAD_ID`, `PI_CODING_AGENT`, `AI_AGENT`) are removed from Pi's
  environment.

#### Limitations

- There is no terminal. The chat hides TTY mode, the terminal prompt card, the
  live screen tail and "Terminal output" (for ACP too), `raw` messages (keys
  for the terminal) are refused with 400, and slash commands that need the
  agent's interactive UI don't work.
- Restart (Explorer → Restart, `DELETE /messages`, or `?restart=true` on the
  MCP endpoints) replaces the agent with a new one in a new session and clears
  the conversation; requests still open become `not_resumable`. Unlike a PTY
  restart, it doesn't run `codex update` or `pi update --self` first.
- Without `--state-db`, the next start of AgentAPI is a new session; the
  agent's earlier session isn't resumed.
- Images reach the agent only as `@"<path>"` attachments of a message, not as
  command-line flags (`codex -i` and Pi's `@file` are refused).

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
online. The child agent receives a new process and session-log watcher, although
its previous in-memory conversation context is not retained. Before a PTY
restart, AgentAPI updates Codex (`codex update`) and Pi (`pi update --self`), so
the restarted agent is the latest release; Claude Code updates itself. A
native-transport restart doesn't run these updates.

### Run-status webhooks

AgentAPI can send an HTTP POST whenever a run changes between `running` and
`stable`. Webhooks can be initialized with CLI flags or `AGENTAPI_WEBHOOK_*`
environment variables, then inspected or changed through `GET/PUT /webhook` or
the Session Explorer.

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

### Discord replies through Temporal

The optional `agentapi discord` service lets you continue the same agent session
from Discord after closing the web UI. It starts a Temporal workflow for a pending
response, sends a bot notification, and waits for a `human_response` signal when
an allowed Discord user replies to that notification. A worker Activity then
submits the reply to AgentAPI. The web UI remains usable throughout.

This supports ordinary conversational follow-ups, recognized numbered PTY
confirmation dialogs and, with the native transport, pending
[requests](#requests-approvals-questions-and-plans). Notifications are independent of browser presence.
AgentAPI and the bridge must remain running; Temporal preserves workflow state,
not the agent's terminal process. See [setup, configuration and limitations](docs/discord-temporal.md).

### Self-update

AgentAPI can update itself from the command line:

```bash
agentapi update          # download and install the latest release
agentapi update --check  # check without downloading
agentapi update --force  # skip version comparison
```

Release binaries are verified against the release's `checksums.txt` SHA-256
manifest before the running executable is replaced. An update is rejected if
the manifest is missing, malformed, or does not match the download.

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
  log; the page exchanges it and removes it from the address bar.
- Or just open the chat: when the server answers 401 it asks for the token.

`POST /auth/session` with `Authorization: Bearer <token>` sets the cookie
(`agentapi_session`, HttpOnly, SameSite=Strict, `Path=/`, and Secure over
HTTPS or behind a proxy that sends `X-Forwarded-Proto: https`). It holds an
HMAC of the token, not the token, so changing the token signs every browser
out. `DELETE /auth/session` signs out, and `GET /auth/session` (no credentials
needed) reports `auth_required` and `authenticated`. Because the cookie is
SameSite=Strict, pages on other sites can't make requests with it. A chat UI
served from another origin (e.g. `bun run dev` on port 3000) needs that origin
in `--allowed-origins`; `--allowed-origins '*'` doesn't allow requests with
cookies.

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

### Runtime reliability

The fork also includes fixes for wide-character terminal cursor tracking,
PTY lifecycle leaks, concurrent event delivery, message tracking races, ACP
shutdown, TUI re-render artifacts, JSONL watcher flushing, rich message
content-block merging during Claude delta streaming, and runtime session-file
switching when Claude Code parks a session to a new JSONL. These changes are
intended to keep AgentAPI stable across long sessions, process replacement, and
temporary browser or network interruptions.

See the [full comparison with upstream](https://github.com/coder/agentapi/compare/main...k1dav-c:agentapi:main)
for the complete commit history.

## Quickstart

1. Install `agentapi`:

   ```bash
   OS=$(uname -s | tr "[:upper:]" "[:lower:]");
   ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/");
   curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" -o agentapi && chmod +x agentapi
   ```

   Alternatively, you can download this fork's latest binary from the
   [releases page](https://github.com/k1dav-c/agentapi/releases). Upstream
   `coder/agentapi` release binaries do not include the features documented in
   the **Changes in this fork** section.

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

## CLI Commands

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

> [!NOTE]
> When using Claude, Codex, Opencode, Copilot, Gemini, Amp or CursorCLI, always specify the agent type explicitly (eg: `agentapi server --type=codex -- codex`), or message formatting may break. Kimi and Pi are auto-detected when the executable name is `kimi` or `pi`; use `--type=kimi` / `--type=pi` for wrappers or ACP mode.

An OpenAPI schema is available in [openapi.json](openapi.json).

By default, the server runs on port 3284. Additionally, the server exposes the same OpenAPI schema at http://localhost:3284/openapi.json and the available endpoints in a documentation UI at http://localhost:3284/docs.

Endpoints:

- GET `/messages` - returns a list of all messages in the conversation with the agent
- POST `/message` - sends a message to the agent. When a 200 response is returned, AgentAPI has detected that the agent started processing the message, or queued it (`queued`) while the agent is busy. `dispatch_mode` chooses what happens while the agent is busy: `queue` (default), `steer` (add it to the running turn; `steered` in the reply) or `interrupt` (stop the turn and send it next); `steer` and `interrupt` need the native transport
- GET `/status` - returns the backward-compatible `stable`/`running` status,
  detailed lifecycle (`starting`, `ready`, `running`, `restarting`, `exited`, or
  `failed`), the transport (`pty`, `acp` or `native`), a session ID, a
  monotonically increasing run ID, the conversation's `epoch` (for
  `/events?sync=1`), the session name, the AgentAPI version, the agent's open
  dialog (`terminal_prompt`) and the terminal width (`terminal_columns`). With
  the native transport it also reports `capabilities` (`steer`, `interrupt`,
  `resume`, `images`) and `pending_requests`
- GET `/events` - an SSE stream of events from the agent: message and status updates, and `request_update` with the native transport. With `?sync=1` it replays incrementally on reconnect (`&since=<seq>&epoch=<epoch>`), and events carry SSE ids so a reconnecting `EventSource` resumes from its `Last-Event-ID`
- DELETE `/messages` - clears all conversation state (messages, rich messages, timeline, errors) and restarts the agent process (native transport: in a new session)
- GET `/queue` - returns the messages waiting to be sent to the agent
- PUT `/queue` - reorders the queue; the body (`{"ids": [...]}`) lists every queued message once
- PUT/DELETE `/queue/{id}` - edits or removes a queued message
- POST `/queue/{id}/steer` - moves a queued message into the turn the agent is working on (native transport)
- POST `/interrupt` - stops the turn the agent is working on (native transport)
- GET `/requests`, GET `/requests/{id}` - the requests (tool approvals, questions, plans) the agent is waiting on and the recently closed ones (native transport; empty otherwise)
- POST `/requests/{id}/respond` - answers a pending request (`option_id`, or `answers` keyed by question ID, with optional `text`); see [Requests](#requests-approvals-questions-and-plans)
- POST `/upload` - stores a file (up to 10 MB) in the server's temporary upload directory and returns its path, which a message references as `@"<path>"`
- GET `/uploads/{checksum}/{name}` - returns an uploaded image (PNG, JPEG, GIF, WebP or BMP, judged by content) for the chat to show; other files are refused
- GET/PUT `/webhook` - reads or updates run-status webhook delivery without restarting the agent
- GET `/mcp` - returns configured MCP servers and the managed config path for Claude, Codex or Pi
- PUT `/mcp` - replaces the complete MCP server set; pass `?restart=true` to restart the PTY agent and apply immediately
- POST `/mcp/check` - checks remote HTTP connectivity and resolves stdio executables
- POST `/mcp/servers`, PATCH/DELETE `/mcp/servers/{name}` - creates, updates, or removes one MCP server
- GET `/mcp/profiles` - exports project-scoped MCP configuration profiles
- PUT/DELETE `/mcp/profiles/{name}` - imports, replaces, or removes a profile
- POST `/mcp/profiles/{name}/apply` - replaces the active MCP configuration with a saved profile
- GET/POST/DELETE `/auth/session` - reports whether the API needs a token and whether the caller has one, signs a browser in with the token (session cookie), or signs it out; see [API token authentication](#api-token-authentication)

#### Transport and session state

`--transport` chooses how AgentAPI drives the agent:

- `pty` (default): runs the agent's terminal UI in a terminal emulator and
  parses its screen.
- `acp`: the experimental ACP transport, the same as `--experimental-acp`.
- `native`: the agent's own structured protocol, for Claude Code, Codex and
  Pi; see [Native transport](#native-transport).

```bash
agentapi server --transport native -- claude
agentapi server --transport native --state-db ~/.agentapi/myproject.db -- codex
```

`--state-db <file>` (native transport only) keeps the conversation in a SQLite
file and resumes the agent's session when AgentAPI starts again with the same
file; see [Persistent sessions](#persistent-sessions). `--state-file <file>`
(PTY transport only) saves the screen-derived messages on shutdown and loads
them on startup (`--save-state` and `--load-state` default to true when it is
set).

#### API token authentication

Set `--api-token` to require a Bearer token on all API requests (static chat
UI routes are exempt):

```bash
agentapi server --api-token -- claude             # auto-generate and print to stderr
agentapi server --api-token=my-secret -- claude    # use a specific token
```

The equivalent environment variable is `AGENTAPI_API_TOKEN`. When set, clients
must include `Authorization: Bearer <token>` on every API call; browsers can use
the session cookie from `POST /auth/session` instead (see
[API token authentication](#api-token-authentication) above). Without
`--api-token`, authentication is disabled (backward compatible).

#### Bind address

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

#### Allowed hosts

By default, the server only allows requests with the host header set to `localhost`. If you'd like to host AgentAPI elsewhere, you can change this by using the `AGENTAPI_ALLOWED_HOSTS` environment variable or the `--allowed-hosts` flag. Hosts must be hostnames only (no ports); the server ignores the port portion of incoming requests when authorizing. This checks the `Host` header only; to keep the server off the network altogether, also listen on loopback with `--bind 127.0.0.1` (see [Bind address](#bind-address)).

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

#### Allowed origins

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

#### Run status webhooks

Set `--webhook-url` to send an HTTP POST whenever the run status changes between
`running` and `stable`:

```bash
agentapi server \
  --webhook-url 'https://example.com/agentapi/events' \
  -- claude
```

The equivalent environment variables are `AGENTAPI_WEBHOOK_URL`,
`AGENTAPI_WEBHOOK_PAYLOAD_TEMPLATE`, `AGENTAPI_WEBHOOK_TIMEOUT`, and
`AGENTAPI_WEBHOOK_MAX_ATTEMPTS`. The timeout defaults to `10s`, and delivery is
attempted up to 3 times.

The initial webhook values can still be changed while AgentAPI is running with
`GET/PUT /webhook`. Session Explorer now uses its Temporal tab for Temporal and
Discord bridge settings. The legacy webhook API remains available for
compatibility, but is no longer the Session Explorer integration.

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

### `agentapi update`

Update the agentapi binary to the latest release from GitHub.

```bash
agentapi update          # download and install the latest version
agentapi update --check  # only check if an update is available
agentapi update --force  # update even if already at the latest version
```

### `agentapi attach`

Attach to a running agent's terminal session.

```bash
agentapi attach --url localhost:3284
```

Press `ctrl+c` to detach from the session.

## How it works

AgentAPI runs an in-memory terminal emulator. It translates API calls into appropriate terminal keystrokes and parses the agent's outputs into individual messages.

### Splitting terminal output into messages

There are 2 types of messages:

- User messages: sent by the user to the agent
- Agent messages: sent by the agent to the user

To parse individual messages from the terminal output, we take the following steps:

1. The initial terminal output, before any user messages are sent, is treated as the agent's first message.
2. When the user sends a message through the API, a snapshot of the terminal is taken before any keystrokes are sent.
3. The user message is then submitted to the agent. From this point on, any time the terminal output changes, a new snapshot is taken. It's diffed against the initial snapshot, and any new text that appears below the initial content is treated as the agent's next message.
4. If the terminal output changes again before a new user message is sent, the agent message is updated.

This lets us split the terminal output into a sequence of messages.

### Removing TUI elements from agent messages

Each agent message contains some extra bits that aren't useful to the end user:

- The user's input at the beginning of the message. Coding agents often echo the input back to the user to make it visible in the terminal.
- An input box at the end of the message. This is where the user usually types their input.

AgentAPI automatically removes these.

- For user input, we strip the lines that contain the text from the user's last message.
- For the input box, we look for lines at the end of the message that contain common TUI elements, like `>` or `------`.

### What will happen when Claude Code, Goose, Aider, or Codex update their TUI?

Splitting the terminal output into a sequence of messages should still work, since it doesn't depend on the TUI structure. The logic for removing extra bits may need to be updated to account for new elements. AgentAPI will still be usable, but some extra TUI elements may become visible in the agent messages.

## Roadmap

Pending feedback, we're considering the following features:

- [Support the MCP protocol](https://github.com/coder/agentapi/issues/1)
- [Support the Agent2Agent Protocol](https://github.com/coder/agentapi/issues/2)

## Long-term vision

In the short term, AgentAPI solves the problem of how to programmatically control coding agents. As time passes, we hope to see the major agents release proper SDKs. One might wonder whether AgentAPI will still be needed then. We think that depends on whether agent vendors decide to standardize on a common API, or each sticks with a proprietary format.

In the former case, we'll deprecate AgentAPI in favor of the official SDKs. In the latter case, our goal will be to make AgentAPI a universal adapter to control any coding agent, so a developer using AgentAPI can switch between agents without changing their code.
