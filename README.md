# AgentAPI

English | [繁體中文](README.zh-TW.md)

Use AgentAPI to control [Claude Code](https://github.com/anthropics/claude-code),
[Codex](https://github.com/openai/codex) and [Pi](https://pi.dev/) with an HTTP API.

![The AgentAPI chat with two tasks: prompts, tool calls, thinking notes and the composer](docs/screenshots/chat-desktop.png)

The screenshots show AgentAPI v3.1.0 running the built-in `native-demo` agent. Its replies, tool
results, models and usage are simulated. See the [chat UI guide](docs/chat-ui.md) for features and
animations, or the [screenshot guide](docs/screenshots/README.md) to reproduce the captures.

Use AgentAPI to:

- Build a unified chat interface for coding agents.
- Provide an MCP server backend that lets one agent control another coding agent.
- Create tools that delegate pull request reviews to an agent.
- Watch many agents from one dashboard. Each AgentAPI pushes its tasks, the requests that need
  you, its messages and heartbeats there with [webhooks](docs/api.md#webhooks), and a client that connects
  to many follows each through its [summary](docs/api.md#watching-many-instances). The built-in
  [hub](docs/api.md#running-a-hub) is such a dashboard, on a desktop or a phone.
- Hand GitHub issues to agents on your machines from the hub's [Issues](docs/hub.md#issues) page:
  each works on its own branch and opens a pull request.

## About this fork

[`k1dav-c/agentapi`](https://github.com/k1dav-c/agentapi) is an independently maintained fork of
[`coder/agentapi`](https://github.com/coder/agentapi), with its own versioned releases. Download
binaries from [this fork's releases](https://github.com/k1dav-c/agentapi/releases) and report
issues involving its added features in [this repository](https://github.com/k1dav-c/agentapi/issues).
Upstream changes should be reviewed and tested against the native transports, chat UI and hub
before integration; upstream and fork version numbers are separate release lines.

This fork builds on upstream v0.12.2. Since 3.0 it drives only Claude Code, Codex and Pi, through
their own protocols (the [native transport](docs/agents.md)); the PTY transport, which emulated
a terminal for other agents, ended with v2.5.1 (see [Agents](#agents)). Session management and
chat features support long-running tasks, and [webhooks](docs/api.md#webhooks) report them to a central
platform. [CHANGELOG.md](CHANGELOG.md) lists what
changed, [docs/architecture.md](docs/architecture.md) describes how it is built,
[docs/hub.md](docs/hub.md) how to watch many AgentAPIs from one page, and the
[full comparison with upstream](https://github.com/coder/agentapi/compare/main...k1dav-c:agentapi:main)
has the complete commit history.

## Contents

- [Features at a glance](#features-at-a-glance)
- [Quickstart](#quickstart)
- [Agents](#agents)
- [Documentation](#documentation)
- [Architecture](#architecture)
- [Development](#development)
- [Runtime reliability](#runtime-reliability)
- [Roadmap](#roadmap) and [long-term vision](#long-term-vision)

## Features at a glance

**Drive coding agents over HTTP**

- Drive Claude Code, Codex and Pi through their own protocols with the
  [native transport](docs/agents.md).
- Read structured activity (thinking, tool calls and their results, token
  usage) from `GET /rich-messages` and `GET /timeline`.
- Messages sent while the agent works wait in a [queue](docs/api.md#message-queue) that
  you can edit and reorder; they can also
  [steer or interrupt](docs/agents.md#steer-interrupt-and-the-queue) the running turn.
- The server tracks each message and the agent's turn on it as a
  [task](docs/api.md#tasks): running, completed, failed, or stopped by a usage limit.
- Follow everything live over [server-sent events](docs/api.md#events-and-incremental-sync),
  with heartbeats and an incremental replay after a reconnect.
- A conversation that runs for months stays light: clients get the latest tasks and read earlier
  ones a page at a time, and with `--state-db` the server holds only the latest in memory
  ([Long conversations](docs/api.md#long-conversations)).

**Agents**

- Tool approvals, questions, and plans arrive as
  [requests](docs/agents.md#requests-approvals-questions-and-plans), answered over HTTP or
  in the chat.
- Switch the [permission mode, model and reasoning effort](docs/agents.md#settings-permission-mode-model-and-effort)
  while the agent runs: bypass (the default), auto, ask or plan.
- Switch Claude Code or Codex to [another model provider](docs/providers.md), such as a LiteLLM
  gateway, while it runs, and back; the conversation goes on. Define providers per machine or once
  on the hub.
- See how full the [context and the rate limits](docs/agents.md#context-and-rate-limits)
  are, and [compact](docs/agents.md#slash-commands-and-compaction) the context; the chat
  suggests it once Claude Code's prompt cache has expired.
- When a [usage limit](docs/agents.md#usage-limits) stops a turn, the queue waits until the
  limit resets, and the turn can continue by itself then.
- A turn that fails on a [transient API error](docs/agents.md#transient-api-errors) (a 502
  from a gateway, an overloaded API) is tried again after 30 seconds, 1 and 2
  minutes, then every 5 minutes, 15 times, before the queue goes on.
- AgentAPI reports a missing or expired [login](docs/agents.md#logging-in) with the command
  to run, and starts the agent by itself once it is logged in.
- A shared [terminal](docs/agents.md#terminal) in the agent's working directory, for logins
  and quick commands.
- [Persistent sessions](docs/agents.md#persistent-sessions): with `--state-db`, the
  conversation survives an AgentAPI restart, and the agent resumes its own
  session.
- Open one of the agent's [earlier sessions](docs/agents.md#earlier-sessions) in its working
  directory, or start a copy of one.
- See what each [session and task cost](docs/agents.md#session-costs) at the model
  providers' list prices (the API-equivalent cost), subagents included, with
  totals for today and the last 7 and 30 days.

**Chat UI** (`/chat`)

- [Tasks](docs/chat-ui.md#tasks-and-the-transcript) read as a transcript, with tool calls as
  rows, thinking as margin notes and what each task cost, and can be exported
  as Markdown; the
  [Session Explorer](docs/chat-ui.md#session-explorer) has links, files, a task index, the
  agent's [earlier sessions](docs/agents.md#earlier-sessions), Restart and the
  [webhook](docs/api.md#webhooks).
- [Lineage](docs/chat-ui.md#lineage): the subagents the agent ran, and what each did, step by
  step, even while it works.
- [Decision cards](docs/chat-ui.md#requests-and-decision-cards) for requests,
  menus for the agent's settings, a ring for its context, and slash commands.
- A [file viewer](docs/chat-ui.md#files) with the file, its git diff and a preview of
  HTML, Markdown and images, which finds a file the agent named relative to a
  repository below its working directory; the [terminal](docs/agents.md#terminal), also on
  phones.
- [Background alerts](docs/chat-ui.md#session-details-and-alerts), browser
  notifications, a transcript cache, and recovery from dropped connections.

**Operations and integrations**

- [A hub](docs/api.md#running-a-hub) (`agentapi hub serve`) watches the AgentAPIs of many
  machines, lists what needs you first, and opens any of them in the chat on a
  desktop or a phone, with favorite prompts it keeps for every chat it opens.
  [`agentapi hub join`](docs/api.md#joining-a-hub) connects a machine's AgentAPIs to it,
  and they stay connected after a restart.
- The hub's [issue dispatch](docs/hub.md#issues): its Issues page lists your repositories' open
  issues through the hub's own GitHub App, and **Assign** hands one to Claude Code, Codex or Pi on
  a machine whose launcher ([`agentapi host`](docs/cli.md#agentapi-host)) starts an AgentAPI for it in a git
  worktree. The agent pushes a branch and opens a pull request; a review on it goes back to the
  same agent, and the page shows each job's state and what its runs cost.
- [Webhooks](docs/api.md#webhooks) push HMAC-signed tasks, requests, messages and
  heartbeats to a dashboard that watches many agents. A client that connects
  to many follows each through one [summary stream](docs/api.md#watching-many-instances).
  With the [human-reply API](docs/api.md#human-reply-api), a bot can tell you when the
  agent stops and pass your answer back.
- [Self-update](docs/cli.md#agentapi-update), verified against the release's checksums,
  and [restarts in place](docs/cli.md#agentapi-restart) into the new binary. Machines
  that opt in can also be [updated through the API](docs/api.md#updating-through-the-api),
  such as from the hub.
- [Security](docs/security.md): an API token (the chat signs in with a cookie), the
  listen address, and the allowed hosts and origins.

## Quickstart

1. Install `agentapi`:

   ```bash
   OS=$(uname -s | tr "[:upper:]" "[:lower:]");
   ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/");
   curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" -o agentapi && chmod +x agentapi
   ```

   Or download this fork's latest binary from the
   [releases page](https://github.com/k1dav-c/agentapi/releases). Upstream
   `coder/agentapi` release binaries do not include this fork's features.

1. Verify the installation:

   ```bash
   agentapi --help
   ```

   > On macOS, if the system says it can't verify the binary, go to `System Settings -> Privacy & Security`, click "Open Anyway", and run the command again.

1. Run a Claude Code server (assumes `claude` is installed on your system and in the `PATH`):

   ```bash
   agentapi server -- claude
   ```

   > If you're getting an error that `claude` is not in the `PATH` but you can run it from your shell, try `which claude` to get the full path and use that instead.

   This drives Claude Code through its own protocol, the
   [native transport](docs/agents.md): approvals arrive as structured
   requests, its settings can be switched and the chat has a terminal.

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

## Agents

Since 3.0, AgentAPI runs Claude Code, Codex and Pi through their own protocols, the
[native transport](docs/agents.md):

| Agent | Type | Protocol |
| --- | --- | --- |
| [Claude Code](https://github.com/anthropics/claude-code) | `claude` | `claude -p` stream-json |
| [Codex](https://github.com/openai/codex) | `codex` | `codex app-server` |
| [Pi](https://pi.dev/) | `pi` | `pi --mode rpc` |

`--type` sets the agent type; without it, AgentAPI takes it from the command's name, so
`agentapi server -- /usr/local/bin/codex` needs none. A wrapper script needs `--type`. The built-in
`native-demo` simulates an agent (see [Development](#development)).

```bash
agentapi server -- claude
agentapi server --state-db ~/.agentapi/myproject.db -- codex
agentapi server --type pi -- ./my-pi-wrapper
```

Any other agent stops AgentAPI at startup with an error that names v2.5.1. To run Aider, Goose,
Gemini CLI, Amp, Cursor CLI, Auggie, Amazon Q, Opencode, Copilot, Kimi Code or another terminal
program, use AgentAPI v2.5.1, the last release with the PTY transport:
download its binary from the [v2.5.1 release](https://github.com/k1dav-c/agentapi/releases/tag/v2.5.1)
and keep it. `agentapi update` only installs the latest release. An AgentAPI 2 that runs such an
agent with `--allow-update` checks the new program before it restarts into it, refuses AgentAPI 3
or later and keeps serving; but the update already replaced the program on disk, so put v2.5.1
back before that AgentAPI restarts some other way.

AgentAPI 4 has no `--transport` flag: drop it from command lines and config files. The
experimental ACP transport (`--experimental-acp`) was removed earlier; that flag stops AgentAPI with
an error.

## Documentation

- [Chat UI](docs/chat-ui.md): the chat at `/chat`, on a desktop and a phone.
- [Agents and the native transport](docs/agents.md): logging in, requests, steering and the queue,
  settings, limits, the terminal, sessions and their costs, and what Claude Code, Codex and Pi each
  do.
- [Model providers](docs/providers.md): Claude Code and Codex on another endpoint, such as a
  LiteLLM gateway, and what each provider was used for.
- [HTTP API](docs/api.md): the conversation API, watching many instances, webhooks, the human-reply
  API and the endpoint reference.
- [Security](docs/security.md): the API token, the bind address, allowed hosts and origins, and what
  an API client can do.
- [CLI reference](docs/cli.md): `agentapi server`, `update`, `restart`, `list`, `hub` and `host`,
  and resource usage.
- [The hub](docs/hub.md): many AgentAPIs on one page, and GitHub issues handed to agents.
- [Architecture](docs/architecture.md): packages, the data flow, persistence and the security
  model.

## Architecture

AgentAPI is a single Go binary: an HTTP server (chi, with huma generating the OpenAPI schema) that
runs one agent process and serves the chat UI, a Next.js app embedded in the binary. The agent's
native adapter reports to an event emitter, which the HTTP API, the event stream, the queue and the
chat read. See [docs/architecture.md](docs/architecture.md) for the packages, the data flow, the
event stream, persistence and the security model.

## Development

```bash
make build      # out/agentapi, with the chat UI embedded
make gen        # regenerate openapi.json and the version
make lint       # Go, TypeScript, shellcheck and actionlint
go test ./...   # Go tests, including the e2e smoke test
cd chat && bun test  # the chat UI's tests
```

AgentAPI builds with Go 1.26 (`go.mod`; the `go` command fetches it) and the chat with Bun. Tests
run with `CGO_ENABLED=0`. Commit `openapi.json` with every API change, and regenerate it only
with `make gen` (see [AGENTS.md](AGENTS.md)). The chat UI lives in [chat/](chat/README.md):
`bun run dev` serves it against a running AgentAPI, and
`agentapi server -- native-demo` runs a simulated agent for working on it
without a real one. [MAINTAINERS.md](MAINTAINERS.md) describes how CI tests and releases a commit.

## Runtime reliability

The fork fixes concurrent event delivery, message tracking races and content-block merging during
Claude delta streaming. These fixes address long sessions, process replacement and temporary
browser or network interruptions.

## Roadmap

Pending feedback, we're considering the following features:

- [Support the MCP protocol](https://github.com/coder/agentapi/issues/1)
- [Support the Agent2Agent Protocol](https://github.com/coder/agentapi/issues/2)

## Long-term vision

AgentAPI lets developers control coding agents programmatically. We hope the major agents
will eventually offer official SDKs. Whether AgentAPI is still needed will depend on whether vendors
adopt a common API or continue to use their own formats.

If vendors adopt a common API, we'll deprecate AgentAPI and recommend the official SDKs. If their
formats remain different, we'll work toward a universal adapter so developers can switch agents
without changing their code.
