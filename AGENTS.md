# Repository guidance for AI agents

Use these commands and conventions when changing AgentAPI.

## Build commands

- `make build` - Build the binary to `out/agentapi` (includes chat UI build)
- `make embed` - Build the chat UI and embed it into Go
- `go build -o out/agentapi main.go` - Build the Go binary with existing embedded assets, without rebuilding the chat UI
- `make gen` (`go generate ./...`) - Generate the OpenAPI schema, the hub's schema (`lib/hub/hub-schema.json`) and version info
- `make fmt` - Format Go code with gofumpt
- `make lint` - Run all linters (Go, TypeScript, shellcheck, actionlint)

## Generated files

- Regenerate `openapi.json` only with `make gen`, and commit its output after
  changing an API model or huma documentation string.
- After `make gen`, run `bun run gen:api` in `chat/` and commit
  `chat/src/lib/api-schema.ts`, which contains the chat's API types. A chat test
  checks that they match `openapi.json`.
- The hub's wire types (its API is plain chi, not huma) are generated too: `make gen` writes
  `lib/hub/hub-schema.json` from the Go types (`hub.Schema`; `TestHubSchema` checks it), and
  `bun run gen:api` writes `chat/src/lib/hub-schema.ts` from it, with `hubLimits`, the limits the
  hub page checks. Commit both; a chat test checks the second.
- Don't write the schema with `go run main.go server --print-openapi dummy > openapi.json`:
  that escapes `<` and `>` in descriptions as `\u003c` / `\u003e`, while
  `make gen` doesn't. The Release workflow runs `make gen` and fails with
  "Unstaged changes" on any difference, which prevents the release.

## Testing

- `CGO_ENABLED=0 go test ./...` - Run all Go tests (CI runs them with `CGO_ENABLED=0`)
- `CGO_ENABLED=1 go test -race ./lib/... ./cmd/...` - Run them with the race detector, as CI does
- `go test ./e2e` - Run the end-to-end tests: the server binary with `e2e/fakeclaude`, a scripted Claude Code
- `cd chat && bun test` - Run the chat UI tests
- Tests are located alongside source files (e.g., `lib/httpapi/server_test.go`)
- CI releases on every push to `main` from Conventional Commit messages, but only after these tests
  pass on that commit: the Release workflow runs `.github/workflows/go-test.yml` first. It doesn't
  wait for the macOS and Windows jobs, which run alongside

## Development commands

- `agentapi server -- claude` - Start server with Claude Code agent
- `agentapi server --type=codex -- codex` - Start server with Codex (explicit type shown)
- `agentapi server -- pi` - Start server with Pi
- `agentapi server -- native-demo` - Start server with the simulated agent, for working on the chat
- Server runs on port 3284 by default
- Chat UI available at http://localhost:3284/chat
- API documentation at http://localhost:3284/docs

## Architecture

AgentAPI is a Go HTTP server for controlling Claude Code, Codex and Pi through their structured
protocols (the native transport).

### Core components

- `main.go` - Entry point using the Cobra CLI framework
- `cmd/` - CLI command definitions, including server, list, hub, host (the launcher, and the systemd user service `agentapi host install` writes), restart (registered AgentAPIs restart in place, e.g. into an updated binary), and update
- `lib/httpapi/` - HTTP server: routes, OpenAPI schema, auth, event streams, instance summaries for monitoring multiple AgentAPIs (`GET /summary`, `/events?only=summary`), the webhook's configuration, and updates through the API (`POST /update`, with `--allow-update`); it wires the three packages below and uses their types under their own names (huma names a schema after the Go type)
- `lib/httpmw/` - The HTTP pieces the server and the hub share: the Host and cross-origin checks, gzip, the host/origin/bind flag parsing, and the file server of the chat UI embedded from `lib/httpmw/chat/`
- `lib/convstate/` - The conversation state clients read, without HTTP: the `EventEmitter`, the tasks and summary it derives, the queue it reports, the `Persister` interface
- `lib/webhook/` - Webhooks that push events (tasks, requests, messages, heartbeats) to a receiver: delivery, and the observer that reads the emitter
- `lib/workspacefs/` - The file viewer's reads of the working directory: content, diff through git, HTML preview, images, find
- `lib/conversation/` - Shared messages, status, and the `Emitter` interface (imported as `conv`)
- `lib/native/` - Native transport: one adapter per agent (`claude/`, `codex/`, `pi/`, and the simulated `demo/`) driving its structured protocol, a conversation that starts an agent that exits on its own again and holds the queue for a usage limit stop or a failed turn it tries again (`hold.go`), `agentio/` for the process, its loop and the JSON-lines plumbing they share, `procutil/` for the child process, `nativetest/` for tests; `ClassifyFailure` orders a failed turn's signals (limit, login, transient) and `Policy` holds the continue, retry and restart settings
- `lib/agentstate/` - The values native agents report (capabilities, login, settings, usage, requests, limit stop, retry wait) and `ConversationState`, the conversation's state the native `Observer` gets whole (`StateChanged`), with the `StoredState` that survives a restart; a leaf package so `lib/convstate`, `lib/instanceapi`, `lib/webhook` and `lib/statedb` don't depend on `lib/native`
- `lib/procenv/` - The environment of the agent and the terminal's shell, without the variables of an agent session AgentAPI runs in and AgentAPI's secrets
- `lib/transcript/` - The types of the structured transcript: rich messages and timeline events
- `lib/agenthome/` - The directories the agents keep their state in (`$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`, `$PI_CODING_AGENT_DIR` or their defaults)
- `lib/agentlog/` - The agents' session-log formats, one package per agent (`claudelog`, `codexlog`, `pilog`), shared by the native adapters and `lib/sessions`
- `lib/sessions/` - Earlier sessions, subagents and their costs, read incrementally from the logs
- `lib/pricing/` - Model prices per million tokens (built-in `prices.json`, `--prices` file) and per-call cost calculation; `scripts/prices.mjs` compares `prices.json` with Pi's model registry
- `lib/statedb/` - SQLite persistence of conversations (`--state-db`; a deprecated `--state-file` is imported into it); the stored state is one versioned meta value, `conversation_state` (`state.go`)
- `lib/instanceapi/` - The contract with clients that watch many AgentAPIs: who an instance is (with its `AgentType`), its summary, the webhook envelope and signature, and the state a hub shows; it imports only `lib/agentstate` and `lib/util`
- `lib/hub/` - `agentapi hub serve`: a server that watches many AgentAPIs, built on `lib/instanceapi` and `lib/httpmw` rather than `lib/httpapi` (signed webhooks, summary streams, a proxy that opens any of them in the chat, the addresses machines join it by, the favorite prompts among the settings its page keeps, and `hub-schema.json`, the generated schema of its wire types)
- `lib/sse/` - Server-sent events as the HTML standard defines them (`Reader`, `Write`), for the hub's streams
- `lib/netaddr/` - The machine's IPv4 addresses: those other machines may reach (the hub's join URLs), and the one on the route to a host (`{ip}` in `--public-url`)
- `lib/userconfig/` - The user's settings file (`~/.agentapi/config.yaml`), which `agentapi server` reads and `agentapi hub join` writes
- `lib/registry/` - The registry of the AgentAPIs running on a machine (`~/.agentapi/instances`), which `agentapi list` reads
- `lib/dispatch/` - The hub's issue dispatch (`docs/specs/issue-dispatch.zh-TW.md`): the machines whose launchers report with signed `host.*` events, `POST /api/dispatch` (start, send, link_pr, stop; a signed-in browser or `--dispatch-token`), the jobs, the cost of each run from their `task.*` events and the last 50 messages their agents were sent, the prompts, the GitHub App the hub registers from a manifest (`/github/connect`) and hands out per-repository tokens of to the launchers (`/api/github/token`, signed), and `Subscribe`/`Board`, the machines and jobs the hub's stream sends after each change (`dispatch` event). The hub's page for it is `chat/src/components/hub/issues/` (Issues), with its model in `chat/src/lib/hub-issues.ts`
- `examples/issue-dispatch/` - The GitHub side of the issue dispatch: the reusable workflow a self-hosted runner in the hub's network runs (`agent-dispatch.yml`), the caller each repository adds, and how to set it all up (README, in Traditional Chinese)
- `lib/github/` - The GitHub App's JWT, installation tokens for one repository, and the REST calls the dispatch makes as the app; standard library only
- `lib/host/` - `agentapi host serve`: the launcher that starts an AgentAPI for each GitHub issue a hub dispatches to the machine, in a git worktree on the issue's branch (`docs/specs/issue-dispatch.zh-TW.md`): its API for the hub behind the host token, the jobs it keeps in `~/.agentapi/jobs.json` and takes over after a restart, and the unix socket the jobs' git credential helper and gh shim get a GitHub token from
- `lib/atomicfile/` - Replaces a file at once, synced so a crash leaves the old data or the new (settings, registry entries, the hub's file)
- `lib/selfupdate/` - Installs the latest release in place of the program (`agentapi update`, `POST /update`): the GitHub lookup, the binary for the platform, checksum verification and the rename under a lock file; whether the user can replace the program (`Program.Blocked`)
- `lib/shellterm/` - The shared shell terminal (`GET /terminal`)
- `lib/htmlpreview/` - An HTML file as a page that shows by itself, for the file viewer's preview
- `lib/handoff/` - The types of the human reply protocol (`GET /handoff`, `POST /handoff/reply`)
- `lib/filelock/` - An exclusive file lock, taken in turn by AgentAPIs on one machine that update the program or an agent CLI
- `lib/logctx/` - The logger in a `context.Context`, and `--log-level` parsing
- `lib/util/` - `OpenAPISchema`, which registers a string type's values as an enum in the OpenAPI schema
- `chat/` - Next.js web UI (embedded into Go binary)
- `skills/` - Agent skills for users of AgentAPI, such as `agentapi-hub-join` (an agent joins its machine to a hub); installed into `~/.claude/skills/`, not used to work on this repository

### Message flow

The server passes messages to the agent's adapter (`lib/native`) and receives structured events
(messages, requests, settings, usage) through its Sink; the conversation reports them to the event
emitter, which streams them to clients. See [the architecture guide](docs/architecture.md).

## Main API endpoints

- GET `/messages` - Get all messages in conversation
- POST `/message` - Send message to agent (content, type fields)
- GET `/status` - Get status, lifecycle details, and session metadata
- GET `/events` - SSE stream of agent events
- GET `/openapi.json` - OpenAPI schema
- GET `/docs` - API documentation UI
- GET `/chat` - Web chat interface

## Supported agents

`--type` takes `claude`, `codex`, `pi` or `native-demo`; without it, the agent command's name picks
the type. Use `--type` for wrapper scripts. Any other agent is refused at startup. `native-demo` is
a simulated agent for development; with `--type claude`, `codex` or `pi` it poses as that agent.

### Adding an agent

1. Write its native adapter under `lib/native/<agent>`: a `native.Factory` and an `Adapter` that
   follows the `Sink` contract in `lib/native/types.go`
2. Add its `AgentType` constant in `lib/instanceapi/instance.go`
3. Add its case in `nativeFactory` (`cmd/server/native.go`) and its alias in `agentTypeAliases`
   (`cmd/server/server.go`)
4. If it keeps session logs, add their format in `lib/agentlog/<agent>log` and read them in
   `lib/sessions`
5. Add its display name in `chat/src/lib/agents.ts`

The `exhaustive` linter checks every switch and map over `AgentType`.

## Project structure

- Chat UI in `chat/` directory (Next.js + TypeScript)
- OpenAPI schema auto-generated to `openapi.json`
- Release versions determined by `next-version.sh`; builds stamp the binary version, and `version.sh` synchronizes generated metadata
