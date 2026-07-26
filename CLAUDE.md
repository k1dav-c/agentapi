# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

AgentAPI is a Go HTTP server that controls coding agents (Claude Code, Aider, Goose, Codex, Gemini, Copilot, Amp, Cursor, Auggie, AmazonQ, Opencode) through terminal emulation. It runs agents in an in-memory terminal emulator, translates HTTP API calls into terminal keystrokes, and parses terminal output into structured messages. It also embeds a Next.js chat web UI.

## Build & Run

```bash
make build              # Build binary to out/agentapi (includes chat UI build)
go build -o out/agentapi main.go  # Go-only build without chat UI
make embed              # Build chat UI and copy into lib/httpapi/chat/ for embedding
make fmt                # Format Go code with gofumpt
make gen                # Regenerate OpenAPI schema and version (go generate ./...)
make lint               # Run all linters (Go, TypeScript, shellcheck, actionlint)
```

Chat UI development:
```bash
cd chat && bun install   # Install chat dependencies
cd chat && bun run dev   # Start Next.js dev server with Turbopack
cd chat && bun lint      # Lint TypeScript
```

## Testing

```bash
go test ./...                           # Run all Go tests
go test ./lib/httpapi/...               # Run tests in a specific package
go test -run TestOpenAPISchema ./lib/httpapi/...  # Run a single test
go test ./e2e                           # Run e2e tests (smoke test)
```

Tests use `CGO_ENABLED=0`. The project uses `testify` (assert/require) and `coder/quartz` for deterministic time mocking. Tests are colocated with source files. E2e tests in `e2e/` use a scripted echo agent that simulates real agent behavior.

## Architecture

### Message Flow
1. User sends message via `POST /message`
2. Server takes a terminal snapshot, sends keystrokes to the agent process
3. A polling loop compares new terminal snapshots against the baseline
4. New content below the baseline becomes the agent's response message
5. SSE events (`GET /events`) stream message and status updates to clients

### Key Packages
- **`lib/httpapi/`** — HTTP server (chi router + huma for OpenAPI). Routes: `/messages`, `/message`, `/status`, `/events` (SSE), `/queue`, `/upload`, `/rich-messages`. The chat UI is embedded via `//go:embed` from `lib/httpapi/chat/`.
- **`lib/screentracker/`** — Core conversation engine. `Conversation` interface with `PTYConversation` implementation. Manages terminal snapshots, screen diffing, message splitting, and status detection (stable vs. changing).
- **`lib/termexec/`** — Terminal process execution. Wraps PTY creation and process lifecycle.
- **`lib/msgfmt/`** — Agent-specific message formatting. Strips echoed user input and TUI elements (input boxes, borders) from terminal output. Each agent type has different formatting quirks.
- **`lib/jsonlwatcher/`** — Watches agent JSONL session logs (Claude, Codex) for rich structured messages (tool calls, thinking, usage data). Runs as a sidecar alongside PTY.
- **`x/acpio/`** — Experimental ACP (Agent Communication Protocol) transport, alternative to PTY.
- **`cmd/`** — CLI commands via cobra/viper. `server` and `attach` subcommands.

### Two Transport Modes
- **PTY (default)**: Runs the agent in a terminal emulator, parses screen output.
- **ACP (experimental)**: Uses the Agent Communication Protocol for structured communication (`--experimental-acp`).

### Adding a New Agent Type
1. Add the `AgentType` constant in `lib/msgfmt/msgfmt.go`
2. Add formatting logic in `lib/msgfmt/` (message box removal, user input stripping)
3. Add readiness detection in `lib/msgfmt/agent_readiness.go`
4. Add alias mapping in `cmd/server/server.go` (`agentTypeAliases`)
5. Add display name in `chat/src/components/chat-provider.tsx`

### Exhaustive Switch/Map Enforcement
The `exhaustive` golangci-lint checker is enabled for both switches and maps. When adding a new `AgentType` or enum value, all switch statements and map literals over that type must be updated or the linter will fail.

## Conventions

- OpenAPI schema is auto-generated: `go run main.go server --print-openapi dummy > openapi.json` (via `go generate`)
- The chat UI build output goes to `lib/httpapi/chat/` with a magic base path placeholder that gets replaced at runtime
- Environment variables use `AGENTAPI_` prefix (e.g., `AGENTAPI_ALLOWED_HOSTS`)
- Server defaults: port 3284, chat at `/chat`, docs at `/docs`
