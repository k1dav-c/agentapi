# Repository guidance for AI agents

Use these commands and conventions when changing AgentAPI.

## Build commands

- `make build` - Build the binary to `out/agentapi` (includes chat UI build)
- `make embed` - Build the chat UI and embed it into Go
- `go build -o out/agentapi main.go` - Build the Go binary with existing embedded assets, without rebuilding the chat UI
- `make gen` (`go generate ./...`) - Generate OpenAPI schema and version info
- `make fmt` - Format Go code with gofumpt
- `make lint` - Run all linters (Go, TypeScript, shellcheck, actionlint)

## Generated files

- Regenerate `openapi.json` only with `make gen`, and commit its output after
  changing an API model or huma documentation string.
- After `make gen`, run `bun run gen:api` in `chat/` and commit
  `chat/src/lib/api-schema.ts`, which contains the chat's API types. A chat test
  checks that they match `openapi.json`.
- Don't write it with `go run main.go server --print-openapi dummy > openapi.json`:
  that escapes `<` and `>` in descriptions as `\u003c` / `\u003e`, while
  `make gen` doesn't. The Release workflow runs `make gen` and fails with
  "Unstaged changes" on any difference, which prevents the release.

## Testing

- `CGO_ENABLED=0 go test ./...` - Run all Go tests (CI runs them with `CGO_ENABLED=0`)
- `cd chat && bun test` - Run the chat UI tests
- Tests are located alongside source files (e.g., `lib/httpapi/server_test.go`)

## Development commands

- `agentapi server -- claude` - Start server with Claude Code agent
- `agentapi server -- aider --model sonnet` - Start server with Aider agent
- `agentapi server -- goose` - Start server with Goose agent
- `agentapi server --type=codex -- codex` - Start server with Codex (explicit type shown)
- `agentapi server --type=gemini -- gemini` - Start server with Gemini (explicit type shown)
- `agentapi attach --url localhost:3284` - Attach to running agent terminal
- Server runs on port 3284 by default
- Chat UI available at http://localhost:3284/chat
- API documentation at http://localhost:3284/docs

## Architecture

AgentAPI is a Go HTTP server with three transports for controlling coding agents. Claude Code,
Codex and Pi default to native mode, which uses their structured protocols. Other agents default
to PTY mode, which parses terminal output. Experimental ACP mode uses the Agent Client Protocol.

### Core components

- `main.go` - Entry point using the Cobra CLI framework
- `cmd/` - CLI command definitions, including server, attach, list, hub, and update
- `lib/httpapi/` - HTTP server, routes, OpenAPI schema, instance summaries for monitoring multiple AgentAPIs (`GET /summary`, `/events?only=summary`), and webhooks that push events (tasks, requests, messages, heartbeats) to a receiver
- `lib/conversation/` - Shared messages, status, and the `Conversation` and `Emitter` interfaces (imported as `conv`)
- `lib/screentracker/` - The PTY engine: terminal output parsing and message splitting
- `lib/termexec/` - Terminal process execution and management
- `lib/msgfmt/` - Message formatting for different agent types (claude, goose, aider, codex, gemini, copilot, amp, cursor, auggie, amazonq, opencode, kimi, pi, custom)
- `lib/native/` - Native transport (the default for Claude Code, Codex and Pi): one adapter per agent (`claude/`, `codex/`, `pi/`) driving its structured protocol, `procutil/` for the child process, `nativetest/` for tests
- `lib/agentlog/` - The agents' session-log formats, one package per agent (`claudelog`, `codexlog`, `pilog`), shared by the native adapters, `lib/sessions` and the PTY watcher
- `lib/sessions/` - Earlier sessions, subagents and their costs, read incrementally from the logs
- `lib/jsonlwatcher/` - Tails a PTY agent's session log for rich messages
- `lib/pricing/` - Model prices per million tokens (built-in `prices.json`, `--prices` file) and per-call cost calculation
- `lib/statedb/` - SQLite persistence of conversations, native and PTY (`--state-db`; a deprecated `--state-file` is imported into it)
- `lib/userconfig/` - The user's settings file (`~/.agentapi/config.yaml`), which agentapi server reads and agentapi hub join writes
- `lib/registry/` - The registry of the AgentAPIs running on a machine (`~/.agentapi/instances`), which `agentapi list` reads
- `chat/` - Next.js web UI (embedded into Go binary)

### Shared architecture

- All transports implement the same conversation interface and report to the event emitter.
- PTY mode translates API calls into keystrokes and parses the terminal screen.
- Native mode reports structured messages, requests, settings, and usage directly.
- The Go binary embeds the Next.js chat UI as static assets.

### PTY message flow

1. User sends message via HTTP API
2. Server takes terminal snapshot
3. Message sent to agent as terminal input
4. Terminal output changes tracked and parsed
5. New content becomes agent response message
6. SSE events stream updates to clients

Native mode delivers messages through an adapter and receives structured events through its Sink.
See [the architecture guide](docs/architecture.md) for all three transport flows.

## Main API endpoints

- GET `/messages` - Get all messages in conversation
- POST `/message` - Send message to agent (content, type fields)
- GET `/status` - Get status, lifecycle details, and transport-specific session metadata
- GET `/events` - SSE stream of agent events
- GET `/openapi.json` - OpenAPI schema
- GET `/docs` - API documentation UI
- GET `/chat` - Web chat interface

## Supported agents

Use `--type=<agent>` to select a type explicitly. AgentAPI can detect the type when the command
exactly matches a known alias. Use an explicit type for full executable paths or wrapper scripts;
unrecognized commands use `custom`.

See the [agent and transport table](README.md#supported-agents) for the full list. Native mode
supports Claude Code, Codex, and Pi, plus `native-demo` with `--type custom` for development.

## Project structure

- Go module with standard layout
- Chat UI in `chat/` directory (Next.js + TypeScript)
- OpenAPI schema auto-generated to `openapi.json`
- Release versions determined by `next-version.sh`; builds stamp the binary version, and `version.sh` synchronizes generated metadata
