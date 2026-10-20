# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

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

[docs/architecture.md](docs/architecture.md) describes the packages, the data flow of each
transport, events, persistence and the security model in full.

### PTY message flow

1. User sends message via `POST /message`
2. Server takes a terminal snapshot, sends keystrokes to the agent process
3. A polling loop compares new terminal snapshots against the baseline
4. New content below the baseline becomes the agent's response message
5. SSE events (`GET /events`) stream message and status updates to clients

Native mode passes messages to an adapter and receives structured Sink events. ACP uses prompt and
response messages over stdio. All three transports update the shared event emitter.

### Key packages

- **`lib/httpapi/`** — HTTP server (chi router + huma for OpenAPI). Routes: `/messages`, `/message`, `/status`, `/events` (SSE), `/queue`, `/upload`, `/rich-messages`. The chat UI is embedded via `//go:embed` from `lib/httpapi/chat/`.
- **`lib/screentracker/`** — Core conversation engine. `Conversation` interface with `PTYConversation` implementation. Manages terminal snapshots, screen diffing, message splitting, and status detection (stable vs. changing).
- **`lib/termexec/`** — Terminal process execution. Wraps PTY creation and process lifecycle.
- **`lib/msgfmt/`** — Agent-specific message formatting. Strips echoed user input and TUI elements (input boxes, borders) from terminal output. Each agent type has different formatting quirks.
- **`lib/transcript/`** — The types of the structured transcript the transports share: rich messages (content blocks, images, usage) and timeline events.
- **`lib/jsonlwatcher/`** — Watches agent JSONL session logs (Claude, Codex, Pi) for rich structured messages (tool calls, thinking, usage data). Runs alongside the PTY transport. For the native transport it also lists the agent's earlier sessions and reads their conversations (`GET /sessions`), and reads the logs of the subagents a session ran (`GET /subagents`, `GET /timeline?subagents=true`).
- **`x/acpio/`** — Experimental ACP (Agent Client Protocol) transport, alternative to PTY.
- **`lib/native/`** — Native transport. `Conversation` (an `st.Conversation`) turns the events of an `Adapter` (one per agent: `claude/`, `codex/`, `pi/`, plus the simulated `demo/`) into conversation state, requests (approvals, questions, plans) and login state. Shared pieces: `procutil/` (child process lifecycle), `IsAuthError`, `ReadImage`; `nativetest/` has a fake adapter. `lib/procenv` prepares the environment for agent processes on all transports.
- **`lib/statedb/`** — SQLite persistence of native sessions (`--state-db`).
- **`lib/shellterm/`** — The shared shell terminal of native sessions (`GET /terminal`).
- **`cmd/`** — CLI commands via cobra/viper. Includes `server`, `attach`, and `update` subcommands.

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

The `exhaustive` golangci-lint checker is enabled for both switches and maps. When adding a new
`AgentType` or enum value, all switch statements and map literals over that type must be updated or
the linter will fail.

## Conventions

- Versioning: `internal/version.Version` stays `0.0.0-dev` in source; builds stamp the real version via `-ldflags -X` (`make build` uses the latest `vX.Y.Z` tag). Releases are cut by CI on push to `main` from Conventional Commit messages (`next-version.sh`), so use `feat:`/`fix:`/`feat!:` prefixes accordingly
- OpenAPI schema is auto-generated: run `make gen` and commit `openapi.json`. Don't redirect `server --print-openapi` into the file yourself: it escapes `<`/`>` as `\u003c`/`\u003e`, `make gen` doesn't, and the Release workflow fails on the difference. Then run `bun run gen:api` in `chat/` and commit `chat/src/lib/api-schema.ts`, the chat's types of the API (a chat test fails until they match)
- The chat UI build output goes to `lib/httpapi/chat/` with a base-path placeholder that gets replaced at runtime
- AgentAPI-specific environment variables use the `AGENTAPI_` prefix (e.g., `AGENTAPI_ALLOWED_HOSTS`)
- Server defaults: port 3284, chat at `/chat`, docs at `/docs`
