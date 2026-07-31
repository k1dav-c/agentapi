# AgentAPI

Control [Claude Code](https://github.com/anthropics/claude-code), [AmazonQ](https://aws.amazon.com/developer/learning/q-developer-cli/), [Opencode](https://opencode.ai/), [Goose](https://github.com/block/goose), [Aider](https://github.com/Aider-AI/aider), [Gemini](https://github.com/google-gemini/gemini-cli), [GitHub Copilot](https://github.com/github/copilot-cli), [Sourcegraph Amp](https://ampcode.com/), [Codex](https://github.com/openai/codex), [Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started), [Auggie](https://docs.augmentcode.com/cli/overview), and [Cursor CLI](https://cursor.com/en/cli) with an HTTP API.

![agentapi-chat](https://github.com/user-attachments/assets/57032c9f-4146-4b66-b219-09e38ab7690d)

You can use AgentAPI:

- to build a unified chat interface for coding agents
- as a backend in an MCP server that lets one agent control another coding agent
- to create a tool that submits pull request reviews to an agent
- and much more!

## Changes in this fork

This fork started from the upstream v0.12.2 codebase. It keeps the original
terminal-emulation and HTTP API model, while extending AgentAPI into a more
complete workspace for operating long-running coding agents.

### Session workspace and chat UI

The embedded chat UI is organized around tasks instead of a single flat
transcript. Each user request becomes a navigable task with its associated
response, thinking blocks, tool calls, and background activity.

The Session Explorer provides:

- links and file paths discovered in the current conversation;
- an index for jumping directly to earlier tasks;
- Markdown preview and export for individual tasks;
- access to the live terminal when the parsed conversation is not sufficient;
- MCP server and webhook configuration without leaving the chat UI.

The UI also includes improved mobile layouts, attachment handling, searchable
tool activity, connection-state indicators, and more compact tool-call cards.

### Message queue and connection recovery

Messages submitted while an agent is busy are placed in a FIFO queue instead
of being rejected. Queued messages can be inspected, edited, or deleted before
delivery through both the API and chat UI.

Long-running browser sessions are protected by SSE heartbeats, automatic
reconnection, stale-connection detection, and local recovery of messages that
failed before reaching the server. The document title and session header expose
the current task, agent status, and connection state.

Relevant APIs include:

- `GET /queue` and `PUT/DELETE /queue/{id}` for queue management;
- `GET /events` for live messages, status, errors, rich activity, and heartbeat
  events;
- `GET /title` for the current human-readable session title.

### Structured Claude and Codex activity

In addition to parsing terminal snapshots, this fork can watch Claude and Codex
session logs. This provides structured thinking blocks, tool invocations, tool
results, usage information, and stable message identifiers that cannot always
be reconstructed reliably from terminal output alone.

- `GET /rich-messages` returns the structured conversation.
- `GET /timeline` returns normalized session events suitable for export,
  auditing, or building another UI.
- Background and delegated tasks remain visible with their running, completed,
  or failed state and output details.

Terminal parsing remains the fallback for other agents and for environments
where a session log is unavailable.

### MCP management

Claude and Codex MCP servers can be managed while AgentAPI is running. The
implementation preserves unrelated settings in `.mcp.json` or
`$CODEX_HOME/config.toml`.

The API and Session Explorer support:

- reading or replacing the complete MCP server map;
- creating, updating, and deleting individual servers;
- checking remote HTTP connectivity and resolving local stdio executables;
- saving, importing, exporting, and applying reusable MCP profiles;
- optionally restarting the PTY agent to apply changes immediately.

When an agent is restarted, AgentAPI itself and its HTTP/SSE clients remain
online. The child agent receives a new process and session-log watcher, although
its previous in-memory conversation context is not retained.

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

When enabled, every API request must include `Authorization: Bearer <token>`.
Static file routes (`/`, `/chat/*`) are exempt so browsers can open the chat
UI without a token.

### Rate limit usage

`GET /usage` returns real-time rate limit utilization from the upstream API
provider. The endpoint dispatches automatically based on the running agent
type:

- **Claude** — reads the OAuth token from `~/.claude/.credentials.json` and
  extracts Anthropic's unified rate limit headers (5-hour / 7-day / overage
  utilization, subscription type, reset times).
- **Codex** — reads `OPENAI_API_KEY` and extracts OpenAI's `x-ratelimit-*`
  headers (request and token limits, remaining quota, reset durations).

```bash
curl http://localhost:3284/usage
```

### Interactive prompt support

The chat UI detects interactive TUI prompts — such as Claude Code's plan
approval dialog or permission confirmation — and renders them as clickable
buttons. Previously these prompts were invisible when structured JSONL
messages were available, causing the agent to appear stuck.

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

### Runtime reliability

The fork also includes fixes for wide-character terminal cursor tracking,
PTY lifecycle leaks, concurrent event delivery, message tracking races, ACP
shutdown, TUI re-render artifacts, and JSONL watcher flushing. These changes are
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
> When using Claude, Codex, Opencode, Copilot, Gemini, Amp or CursorCLI, always specify the agent type explicitly (eg: `agentapi server --type=codex -- codex`), or message formatting may break. Kimi is auto-detected when the executable name is `kimi`; use `--type=kimi` for wrappers or ACP mode.

An OpenAPI schema is available in [openapi.json](openapi.json).

By default, the server runs on port 3284. Additionally, the server exposes the same OpenAPI schema at http://localhost:3284/openapi.json and the available endpoints in a documentation UI at http://localhost:3284/docs.

Endpoints:

- GET `/messages` - returns a list of all messages in the conversation with the agent
- POST `/message` - sends a message to the agent. When a 200 response is returned, AgentAPI has detected that the agent started processing the message
- GET `/status` - returns the backward-compatible `stable`/`running` status,
  detailed lifecycle (`starting`, `ready`, `running`, `restarting`, `exited`, or
  `failed`), a session ID, and a monotonically increasing run ID
- GET `/events` - an SSE stream of events from the agent: message and status updates
- POST `/restart` - restarts the agent PTY process; AgentAPI and its clients stay connected
- GET `/usage` - returns real-time rate limit utilization from the upstream API (Anthropic or OpenAI)
- GET/PUT `/webhook` - reads or updates run-status webhook delivery without restarting the agent
- GET `/mcp` - returns configured MCP servers and the managed config path for Claude or Codex
- PUT `/mcp` - replaces the complete MCP server set; pass `?restart=true` to restart the PTY agent and apply immediately
- POST `/mcp/check` - checks remote HTTP connectivity and resolves stdio executables
- POST `/mcp/servers`, PATCH/DELETE `/mcp/servers/{name}` - creates, updates, or removes one MCP server
- GET `/mcp/profiles` - exports project-scoped MCP configuration profiles
- PUT/DELETE `/mcp/profiles/{name}` - imports, replaces, or removes a profile
- POST `/mcp/profiles/{name}/apply` - replaces the active MCP configuration with a saved profile

#### API token authentication

Set `--api-token` to require a Bearer token on all API requests (static chat
UI routes are exempt):

```bash
agentapi server --api-token -- claude             # auto-generate and print to stderr
agentapi server --api-token=my-secret -- claude    # use a specific token
```

The equivalent environment variable is `AGENTAPI_API_TOKEN`. When set, clients
must include `Authorization: Bearer <token>` on every API call. Without
`--api-token`, authentication is disabled (backward compatible).

#### Allowed hosts

By default, the server only allows requests with the host header set to `localhost`. If you'd like to host AgentAPI elsewhere, you can change this by using the `AGENTAPI_ALLOWED_HOSTS` environment variable or the `--allowed-hosts` flag. Hosts must be hostnames only (no ports); the server ignores the port portion of incoming requests when authorizing.

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

The initial values can also be changed while AgentAPI is running from the
Webhook tab in the chat UI's Session Explorer. Saving an empty URL disables
delivery.

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
