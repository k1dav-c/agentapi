# CLAUDE.md

Use these commands and conventions when working on AgentAPI with Claude Code (claude.ai/code).

## Project overview

AgentAPI is a Go HTTP server for Claude Code, Codex and Pi, driven through their structured
protocols (the native transport); it refuses any other agent at startup. The binary embeds a
Next.js chat UI.

## Build & Run

```bash
make build              # Build binary to out/agentapi (includes chat UI build)
go build -o out/agentapi main.go  # Build with existing embedded chat assets
make embed              # Build chat UI and copy into lib/httpmw/chat/ for embedding
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
deterministic time mocking. Tests are colocated with source files. E2E tests in `e2e/` use
`e2e/fakeclaude`, a scripted Claude Code. CI also runs `CGO_ENABLED=1 go test -race ./lib/...
./cmd/...` and every test on macOS. On Windows it builds every test and runs those of the packages
with Windows code. Releases wait only for the Linux jobs.

## Architecture

See [docs/architecture.md](docs/architecture.md) for packages, the data flow, events,
persistence and the security model.

### Message flow

1. User sends message via `POST /message`
2. The server passes it to the agent's adapter (`lib/native`)
3. The adapter reports structured events (messages, requests, settings, usage) through its Sink
4. The conversation reports them to the event emitter (`lib/convstate`)
5. SSE events (`GET /events`) stream message and status updates to clients

### Key packages

- **`lib/httpapi/`** — HTTP server (chi router + huma for OpenAPI). Routes: `/messages`, `/message`, `/status`, `/events` (SSE), `/queue`, `/tasks`, `/summary`, `/upload`, `/rich-messages`, `/update`. It keeps routing, the huma operations, auth, CORS, SSE writing and the message queue, and wires `lib/convstate`, `lib/webhook` and `lib/workspacefs`, whose types it uses under their own package names: huma names a schema after the Go type, whichever package declares it, so no aliases are needed. `task_update` and `queue_update` go only to `/events?sync=1` clients; `summary_update` only to `/events?only=summary`, and `POST /tasks/seen` marks the tasks read for the summary's `unread`; `registry.go` keeps the server's registry entry. `update.go` updates the server through its API with `--allow-update` (`POST`/`DELETE /update`: install the latest release, wait for the agent to be idle, restart in place) and reports it in the summary's `update`. Features that don't change the conversation are small modules with narrow dependencies (`fileAPI`, `terminalAPI`, `uploadAPI`, `authAPI`, `webhookAPI`).
- **`lib/httpmw/`** — The HTTP pieces the server and the hub share: the Host check and `--allowed-hosts`/`--allowed-origins`/`--bind` parsing (`hosts.go`), `CrossOriginProtection`, `Compress`, and `FileServerWithIndexFallback`, which serves the chat UI embedded via `//go:embed` from `lib/httpmw/chat/`.
- **`lib/convstate/`** — The conversation state clients read, without HTTP: the `EventEmitter` (messages, rich messages, timeline, requests, status; seq and epoch, subscriptions and replay), the tasks it derives (`tasks.go`: a user message and the agent's turn on it), the summary (`summary.go`), the queue as the server reported it, and the `Persister` (`lib/statedb`) it hands every change to.
- **`lib/webhook/`** — Webhooks push events to a receiver: `Dispatcher` delivers them (queue, retries, HMAC signatures, heartbeats, `{ip}` in the public URL); `Observe` subscribes to the emitter (`Source`) and turns its events into task, request, message and tool events.
- **`lib/workspacefs/`** — What the file viewer reads of the working directory (`Dir`: content, diff through git, HTML preview, images, find). A refusal is an `Error` whose `Kind` `lib/httpapi` maps to 400, 403 or 404.
- **`lib/conversation/`** — Shared types, imported as `conv`: `conv.Message`, roles, status, error levels and validation errors, and the `Emitter` the native conversation reports changes to.
- **`lib/transcript/`** — The types of the structured transcript: rich messages (content blocks, images, usage) and timeline events.
- **`lib/agentlog/`** — The agents' session-log formats, one package each (`claudelog/`, `codexlog/`, `pilog/`): record types, parsers into rich messages and timeline events (the native adapters use them too), prompt and injected-context classification, model calls and file layout; plus line reading, the incremental file index and format-drift warnings.
- **`lib/sessions/`** — A server-held `Store` reads session logs incrementally for earlier sessions and history (`GET /sessions`, resume), subagents (`GET /subagents`, `GET /timeline?subagents=true`) and costs (`GET /sessions`, `GET /cost`).
- **`lib/pricing/`** — Model prices per million tokens, from the embedded `prices.json` and any additions or overrides in a `--prices` file. `Price.Cost` calculates a call's API-equivalent USD cost from its tokens. `bun scripts/prices.mjs <pi-ai>` compares `prices.json` with Pi's model registry (`--write` updates it); its header lists the prices added by hand.
- **`lib/native/`** — Native transport. `Conversation` turns the events of an `Adapter` (one per agent: `claude/`, `codex/`, `pi/`, plus the simulated `demo/`) into conversation state, requests (approvals, questions, plans) and login state, holds the queue for a usage limit stop or a failed turn it tries again (`hold.go`), and starts an agent that exits on its own again. Shared pieces: `agentio/` (the agent process and its loop, stdin writes, response matching, lenient decoding with drift warnings, the snapshot throttle), `procutil/` (child process lifecycle), `IsAuthError`, `IsLimitError` and `IsTransientError` with `ClassifyFailure`, the order they count in for a failed turn, `ReadImage`; `nativetest/` has a fake adapter. `Policy` (`policy.go`) holds `--auto-continue`, `--retry-delays` and the restart delays with their defaults; `httpapi.NativeConfig` embeds it.
- **`lib/agentstate/`** — The values that describe a native agent's state and what it waits on: `Capabilities`, `LoginRequired`, `Settings`, `AgentUsage`, `Request`/`RequestState`, `LimitStop`, `RetryWait`, and `ConversationState`, the conversation's state the native `Observer` gets whole (`StateChanged`), with the `StoredState` that survives a restart. A new piece of state follows the checklist in `docs/architecture.md` ("Adding a piece of conversation state"). `lib/native` produces them; `lib/convstate`, `lib/instanceapi`, `lib/webhook`, `lib/statedb` and `lib/httpapi` use them without depending on `lib/native`. It imports only huma and `lib/util`, for the enums' `Schema` methods.
- **`lib/procenv/`** — The environment of the processes AgentAPI starts: the agent's, without the variables of an agent session AgentAPI runs in and AgentAPI's secrets (`ForAgent`), and the terminal shell's, which also drops AgentAPI's other variables (`WithoutAgentAPI`).
- **`lib/agenthome/`** — The directories the agents keep their state in (`$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`, `$PI_CODING_AGENT_DIR` or their defaults), for the session logs.
- **`lib/statedb/`** — SQLite persistence of conversations (`--state-db`; a deprecated `--state-file` is imported into it). The stored state is one versioned meta value, `conversation_state` (`state.go`), moved there from its old keys on first open; the old keys are still written for one release. A database AgentAPI 2 kept for a PTY agent opens: the agent starts a new session below its conversation.
- **`lib/instanceapi/`** — The contract between an AgentAPI and the clients that watch many: `WebhookInstance` (who it is, in its webhook events, summary and registry entry), `SummaryBody`, the webhook envelope with `Sign`/`Verify`, and `StateOf`, the state a hub shows of an instance. It imports only `lib/agentstate` and `lib/util`; `lib/hub`, `lib/registry`, `cmd/hub` and `cmd/list` use it instead of `lib/httpapi`. It also holds `AgentType`.
- **`lib/hub/`** — `agentapi hub serve`, which depends on `lib/instanceapi` and `lib/httpmw`, not on the server: instances join with signed webhooks (`webhook.go`), the hub follows each one's `/events?only=summary` (`watch.go`), keeps them in a file (`store.go`, with when the hub saw each working in `work.go`; `persist.go` writes it in the background, a versioned envelope with a `.bak`), and serves signed-in browsers the list with each instance's `state`, their changes (`/api/events`), where machines join it (`join.go`, `/api/join`: the hub's addresses, and its secret on a hub with a token) and a proxy that opens any instance in the chat with its token (`proxy.go`); the page is `chat/src/components/hub-view.tsx` with its parts and hooks in `chat/src/components/hub/`, its logic in `chat/src/lib/hub.ts` and the changes it makes through `chat/src/lib/hub-api.ts`; the chat it opens in a frame draws the instance's bar (`hub/frame-bar.tsx`, from window messages in `chat/src/lib/hub-frame.ts`). The favorite prompts are kept in the same file (`favorites.go`, `GET`/`PUT /api/favorites`), managed in the page's settings (`hub/settings.tsx`, `hub/favorites-settings.tsx`) and sent to the framed chat, whose composer inserts one (`message-input/favorites-menu.tsx`).
- **`lib/sse/`** — Server-sent events as the HTML standard defines them (`Reader`, `Write`), for the hub's streams; `lib/httpapi` writes its own with huma's `sse`.
- **`lib/netaddr/`** — The machine's IPv4 addresses: those other machines may reach it at (the hub's join URLs, `Reachable`), and `Resolver`, the source address of the route to a host (kept 30 s, each lookup bounded by `DialTimeout`). It fills in `{ip}` in `--public-url` for every webhook event, summary and registry entry; the webhook dispatcher looks it up in the background and the events read the last answer.
- **`lib/userconfig/`** — The user's settings file, `~/.agentapi/config.yaml`: `agentapi server` flags by name, below flags and `AGENTAPI_*` variables (`cmd/server/userconfig.go`); `agentapi hub join` writes a hub's webhook into it.
- **`lib/registry/`** — The registry of the AgentAPIs running on a machine (`~/.agentapi/instances`): each server writes its entry while it serves (`--registry`, `--registry-dir`); `agentapi list` and clients that watch many read it.
- **`lib/atomicfile/`** — `Write` replaces a file at once (0600): a temporary file, synced before it is renamed into place and the directory synced after, so a crash leaves the old data or the new. `lib/userconfig`, `lib/registry` and `lib/hub` write their files with it.
- **`lib/selfupdate/`** — Installs the latest release of k1dav-c/agentapi in place of the program (`agentapi update`, `POST /update`): the GitHub lookup, the binary for the platform, its check against the release's `checksums.txt` and that it runs as that version, and the rename under a lock file next to the program (`Install`); `Program.Blocked` says why the user can't replace the program, with the one-time fix. `selfupdatetest` serves a fake release for tests.
- **`lib/shellterm/`** — The shared shell terminal (`GET /terminal`).
- **`lib/htmlpreview/`** — Turns an HTML file into a page that shows by itself (its stylesheets, scripts and images inlined as `data:` URLs), for the file viewer's preview.
- **`lib/handoff/`** — The types of the human reply protocol (`GET /handoff`, `POST /handoff/reply`).
- **`lib/filelock/`** — An exclusive lock on a file, which AgentAPIs on one machine take in turn: updating the program, an agent CLI updating itself.
- **`lib/logctx/`** — The logger in a `context.Context`, and `--log-level` parsing.
- **`lib/util/`** — `OpenAPISchema`, which registers a string type's values as an enum in the OpenAPI schema.
- **`skills/`** — Agent skills for users of AgentAPI (`agentapi-hub-join`: an agent joins its machine to a hub, documented in `docs/hub.md`); they install into `~/.claude/skills/` and aren't for working on this repository.
- **`cmd/`** — CLI commands via cobra/viper. Includes `server` (which restarts in place on SIGUSR2, and after an update through its API with `--allow-update`), `list` (the AgentAPIs in the registry, with their summaries), `hub` (`serve`: the hub; `join`, `leave`, `status`: connecting a machine's AgentAPIs to a hub), `restart` (has the registered AgentAPIs restart in place, e.g. into an updated binary), and `update` (`--restart` restarts them after it) subcommands.

### The native transport

AgentAPI speaks Claude Code's stream-json, `codex app-server` JSON-RPC or `pi --mode rpc` directly. Adapters must follow the `Sink` contract in `lib/native/types.go` (no concurrent Sink calls; message IDs unique across adapters of one factory). `--transport` is deprecated and takes only `native`.

### Adding a new agent type

1. Write its native adapter under `lib/native/<agent>`: a `native.Factory` and an `Adapter` that follows the `Sink` contract
2. Add the `AgentType` constant in `lib/instanceapi/instance.go`
3. Add its case in `nativeFactory` (`cmd/server/native.go`) and its alias in `agentTypeAliases` (`cmd/server/server.go`)
4. If it keeps session logs, add their format in `lib/agentlog/<agent>log` and read them in `lib/sessions`
5. Add display name in `chat/src/lib/agents.ts`

### Exhaustive switch/map enforcement

The `exhaustive` golangci-lint checker covers switches and maps. When adding an `AgentType` or enum
value, update every switch and map literal over that type to pass lint.

## Conventions

- Versioning: `internal/version.Version` stays `0.0.0-dev` in source; builds stamp the real version via `-ldflags -X` (`make build` uses the latest `vX.Y.Z` tag). Releases are cut by CI on push to `main` from Conventional Commit messages (`next-version.sh`), so use `feat:`/`fix:`/`feat!:` prefixes. The Release workflow runs the tests (`go-test.yml`) on the commit first and releases only if the Linux jobs pass, so a `main` that fails them doesn't release
- OpenAPI schema is auto-generated: run `make gen` and commit `openapi.json`. Don't redirect `server --print-openapi` into the file yourself: it escapes `<`/`>` as `\u003c`/`\u003e`, `make gen` doesn't, and the Release workflow fails on the difference. Then run `bun run gen:api` in `chat/` and commit `chat/src/lib/api-schema.ts`, the chat's API types (a chat test fails until they match)
- The chat UI build output goes to `lib/httpmw/chat/` with a base-path placeholder that gets replaced at runtime
- AgentAPI-specific environment variables use the `AGENTAPI_` prefix (e.g., `AGENTAPI_ALLOWED_HOSTS`)
- Server defaults: port 3284, chat at `/chat`, docs at `/docs`
