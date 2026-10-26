# CLAUDE.md

Use these commands and conventions when working on AgentAPI with Claude Code (claude.ai/code).

## Project overview

AgentAPI is a Go HTTP server for coding agents. Native mode, the default for Claude Code, Codex and
Pi, uses their structured protocols. PTY mode, the default for every other agent, translates API
calls into terminal keystrokes and parses screen output; experimental ACP mode uses the Agent Client
Protocol. The binary embeds a Next.js chat UI.

## Build & Run

```bash
make build              # Build binary to out/agentapi (includes chat UI build)
go build -o out/agentapi main.go  # Build with existing embedded chat assets
make embed              # Build chat UI and copy into lib/httpapi/chat/ for embedding
make fmt                # Format Go code with gofumpt
make gen                # Regenerate OpenAPI schema and version (go generate ./...)
make lint               # Run all linters (Go, TypeScript, shellcheck, actionlint)
```

From the repository root, start chat UI development with:

```bash
cd chat
bun install   # Install chat dependencies
bun run dev   # Start the Next.js development server with Turbopack
```

Run `bun lint` from `chat/` in another terminal to lint TypeScript.

## Testing

```bash
go test ./...                           # Run all Go tests
go test ./lib/httpapi/...               # Run tests in a specific package
go test -run TestOpenAPISchema ./lib/httpapi/...  # Run a single test
go test ./e2e                           # Run e2e tests (smoke test)
cd chat && bun test                     # Run the chat UI tests
```

Use `CGO_ENABLED=0` for tests. The project uses `testify` (assert/require) and `coder/quartz` for
deterministic time mocking. Tests are colocated with source files. E2E tests in `e2e/` use a
scripted echo agent that simulates real agent behavior.

## Architecture

See [docs/architecture.md](docs/architecture.md) for packages, transport data flows, events,
persistence and the security model.

### PTY message flow

1. User sends message via `POST /message`
2. Server takes a terminal snapshot, sends keystrokes to the agent process
3. A polling loop compares new terminal snapshots against the baseline
4. New content below the baseline becomes the agent's response message
5. SSE events (`GET /events`) stream message and status updates to clients

Native mode passes messages to an adapter and receives structured Sink events. ACP uses prompt and
response messages over stdio. All three transports update the shared event emitter.

### Key packages

- **`lib/httpapi/`** — HTTP server (chi router + huma for OpenAPI). Routes: `/messages`, `/message`, `/status`, `/events` (SSE), `/queue`, `/tasks`, `/summary`, `/upload`, `/rich-messages`. The chat UI is embedded via `//go:embed` from `lib/httpapi/chat/`. The `EventEmitter` keeps the conversation state clients read and derives the tasks (`tasks.go`: a user message and the agent's turn on it); `task_update` and `queue_update` go only to `/events?sync=1` clients. For monitoring multiple AgentAPIs, `summary.go` provides instance summaries through `/summary` and sends `summary_update` to `/events?only=summary`; `POST /tasks/seen` marks the tasks read for the summary's `unread`, and `registry.go` keeps the server's registry entry. Features outside the conversation's changes are small modules with narrow dependencies (`fileAPI`, `terminalAPI`, `uploadAPI`, `authAPI`, `webhookAPI`). Webhooks push events to a receiver: `webhook.go` delivers them (queue, retries, HMAC signatures, heartbeats), `webhook_events.go` subscribes to the emitter and turns its events into task, request, message and tool events.
- **`lib/conversation/`** — Shared types and interfaces, imported as `conv`: `conv.Message`, roles, status, error levels and validation errors. The server calls transports through `Conversation`; transports report changes through `Emitter`.
- **`lib/screentracker/`** — The PTY engine: `PTYConversation` (a `conv.Conversation`) manages terminal snapshots, screen diffing, message splitting, and status detection (stable vs. changing); `AgentIO` and `TerminalPart` type into the agent's terminal.
- **`lib/termexec/`** — Terminal process execution. Wraps PTY creation and process lifecycle.
- **`lib/msgfmt/`** — Agent-specific message formatting. Strips echoed user input and TUI elements (input boxes, borders) from terminal output. Each agent type has different formatting quirks.
- **`lib/transcript/`** — The types of the structured transcript the transports share: rich messages (content blocks, images, usage) and timeline events.
- **`lib/agentlog/`** — The agents' session-log formats, one package each (`claudelog/`, `codexlog/`, `pilog/`): record types, parsers into rich messages and timeline events (the native adapters use them too), prompt and injected-context classification, model calls, file layout and resolvers; plus line reading, the incremental file index and format-drift warnings.
- **`lib/sessions/`** — A server-held `Store` reads session logs incrementally for earlier sessions and history (`GET /sessions`, resume), subagents (`GET /subagents`, `GET /timeline?subagents=true`) and costs (`GET /sessions`, `GET /cost`).
- **`lib/jsonlwatcher/`** — Tails the session log of a PTY agent's process for rich structured messages (tool calls, thinking, usage), alongside the PTY transport.
- **`lib/pricing/`** — Model prices per million tokens, from the embedded `prices.json` and any additions or overrides in a `--prices` file. `Price.Cost` calculates a call's API-equivalent USD cost from its tokens.
- **`x/acpio/`** — Experimental ACP (Agent Client Protocol) transport, alternative to PTY.
- **`lib/native/`** — Native transport. `Conversation` (a `conv.Conversation`) turns the events of an `Adapter` (one per agent: `claude/`, `codex/`, `pi/`, plus the simulated `demo/`) into conversation state, requests (approvals, questions, plans) and login state. Shared pieces: `procutil/` (child process lifecycle), `IsAuthError`, `ReadImage`; `nativetest/` has a fake adapter. `lib/procenv` prepares the environment for agent processes on all transports.
- **`lib/statedb/`** — SQLite persistence of conversations, native and PTY (`--state-db`; a deprecated `--state-file` is imported into it).
- **`lib/hub/`** — `agentapi hub serve`: instances join with signed webhooks (`webhook.go`), the hub follows each one's `/events?only=summary` (`watch.go`), keeps them in a file (`store.go`), and serves a browser that signed in the list, their changes (`/api/events`) and a proxy that opens any instance in the chat with its token (`proxy.go`); the page is `chat/src/components/hub-view.tsx`.
- **`lib/userconfig/`** — The user's settings file, `~/.agentapi/config.yaml`: `agentapi server` flags by name, below flags and `AGENTAPI_*` variables (`cmd/server/userconfig.go`); `agentapi hub join` writes a hub's webhook into it.
- **`lib/registry/`** — The registry of the AgentAPIs running on a machine (`~/.agentapi/instances`): each server writes its entry while it serves (`--registry`, `--registry-dir`); `agentapi list` and clients that watch many read it.
- **`lib/shellterm/`** — The shared shell terminal of native sessions (`GET /terminal`).
- **`cmd/`** — CLI commands via cobra/viper. Includes `server`, `attach`, `list` (the AgentAPIs in the registry, with their summaries), `hub` (`serve`: the hub; `join`, `leave`, `status`: connecting a machine's AgentAPIs to a hub), and `update` subcommands.

### Three transport modes

- **PTY**: Runs the agent in a terminal emulator, parses screen output. The default for agents without a native adapter, and with `--state-file`.
- **ACP (experimental)**: Uses the Agent Client Protocol for structured communication (`--experimental-acp`).
- **Native** (the default for Claude Code, Codex and Pi; `--transport native`): Speaks Claude Code's stream-json, `codex app-server` JSON-RPC or `pi --mode rpc` directly. Adapters must follow the `Sink` contract in `lib/native/types.go` (no concurrent Sink calls; message IDs unique across adapters of one factory).

### Adding a new agent type

1. Add the `AgentType` constant in `lib/msgfmt/msgfmt.go`
2. Add its profile in `lib/msgfmt/profiles.go`: how to remove its input box, what it shows around the echoed message, when it is ready, its status lines (a field left zero takes the generic behavior)
3. Add alias mapping in `cmd/server/server.go` (`agentTypeAliases`)
4. Add display name in `chat/src/lib/agents.ts`

### Exhaustive switch/map enforcement

The `exhaustive` golangci-lint checker covers switches and maps. When adding an `AgentType` or enum
value, update every switch and map literal over that type to pass lint.

## Conventions

- Versioning: `internal/version.Version` stays `0.0.0-dev` in source; builds stamp the real version via `-ldflags -X` (`make build` uses the latest `vX.Y.Z` tag). Releases are cut by CI on push to `main` from Conventional Commit messages (`next-version.sh`), so use `feat:`/`fix:`/`feat!:` prefixes accordingly
- OpenAPI schema is auto-generated: run `make gen` and commit `openapi.json`. Don't redirect `server --print-openapi` into the file yourself: it escapes `<`/`>` as `\u003c`/`\u003e`, `make gen` doesn't, and the Release workflow fails on the difference. Then run `bun run gen:api` in `chat/` and commit `chat/src/lib/api-schema.ts`, the chat's types of the API (a chat test fails until they match)
- The chat UI build output goes to `lib/httpapi/chat/` with a base-path placeholder that gets replaced at runtime
- AgentAPI-specific environment variables use the `AGENTAPI_` prefix (e.g., `AGENTAPI_ALLOWED_HOSTS`)
- Server defaults: port 3284, chat at `/chat`, docs at `/docs`
