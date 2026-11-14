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
- `go test ./internal/docscheck` - Check the docs: relative links, images and anchors resolve, and each zh-TW twin has its English page's structure
- `cd chat && bun test` - Run the chat UI tests
- Tests are located alongside source files (e.g., `lib/httpapi/server_test.go`)
- CI releases on every push to `main` from Conventional Commit messages, but only after these tests
  pass on that commit: the Release workflow runs `.github/workflows/go-test.yml` first. Every job
  runs on the repository's self-hosted Linux runner; nothing tests macOS or Windows

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
- `lib/convstate/` - The conversation state clients read, without HTTP: the `EventEmitter`, the tasks and summary it derives, the window it holds (with a state database, the latest 100 tasks' rich messages and timeline) and the pages of the lists, the queue it reports, the `Persister` interface
- `lib/webhook/` - Webhooks that push events (tasks, requests, messages, heartbeats) to a receiver: delivery, and the observer that reads the emitter
- `lib/workspacefs/` - The file viewer's reads of the working directory: content, diff through git, HTML preview, images, find
- `lib/conversation/` - Shared messages, status, and the `Emitter` interface (imported as `conv`)
- `lib/native/` - Native transport: one adapter per agent (`claude/`, `codex/`, `pi/`, and the simulated `demo/`) driving its structured protocol, a conversation that starts an agent that exits on its own again and holds the queue for a usage limit stop or a failed turn it tries again (`hold.go`), `agentio/` for the process, its loop and the JSON-lines plumbing they share, `procutil/` for the child process, `nativetest/` for tests; `ClassifyFailure` orders a failed turn's signals (limit, login, transient) and `Policy` holds the continue, retry and restart settings; an adapter that implements `ProviderRunner` switches model providers (`SwitchProvider`), or asks for a new agent with a `RelaunchError`, which `PUT /settings` starts through `ApplySettingsAndRelaunch`, keeping apply, prepare and start in one settings-mutation sequence; `RelaunchError.Recovery` restores the last known provider/model after an uncertain switch, by the conversation itself once a turn ends if the server couldn't relaunch then (`provider.go`); `Config.ProviderUsed` reports which provider the session runs on from when (`ProviderUse`), which the server appends to the provider ledger
- `lib/agentstate/` - The values native agents report (capabilities, login, settings, usage, requests, limit stop, retry wait) and `ConversationState`, the conversation's state the native `Observer` gets whole (`StateChanged`), with the `StoredState` that survives a restart; a leaf package so `lib/convstate`, `lib/instanceapi`, `lib/webhook` and `lib/statedb` don't depend on `lib/native`
- `lib/provider/` - The model providers Claude Code and Codex can switch to (an Anthropic- or OpenAI-compatible endpoint such as a LiteLLM gateway): `Provider`, `Source` (the `providers` section of `~/.agentapi/config.yaml`, read again when it changes, then the hub's; the file wins on an ID; the models `<base_url>/models` lists when none are defined, kept 10 minutes), its models' prices (`prices.go`: `Provider.Prices` from the file's `prices` or the hub's sync, `Source.Prices`), and `HubSync`, which gets the hub's every minute from `POST /api/providers/sync`, signed with the hub's secret, sealed with it (AES-256-GCM, `Seal`/`Open`) and bound to the request nonce so an old answer can't be replayed; model-list redirects drop credentials on a cross-origin hop
- `lib/procenv/` - The environment of the agent and the terminal's shell, without the variables of an agent session AgentAPI runs in and AgentAPI's secrets
- `lib/transcript/` - The types of the structured transcript: rich messages and timeline events
- `lib/agenthome/` - The directories the agents keep their state in (`$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`, `$PI_CODING_AGENT_DIR` or their defaults)
- `lib/agentlog/` - The agents' session-log formats, one package per agent (`claudelog`, `codexlog`, `pilog`), shared by the native adapters and `lib/sessions`
- `lib/sessions/` - Earlier sessions, subagents and their costs, read incrementally from the logs; each call counts toward its model provider (`ModelCall.Provider`, `SessionCost.Providers`): Codex's from its log, Claude Code's from the provider ledger (`Config.Providers`: the session's latest entry at or before the call, the agent's own without one); a call to a provider with a price of its model is priced at it before `lib/pricing` (`Config.ProviderPrices`, `atProviderPrices`; a change of the prices reprices every session), and `SessionCost.WithProviderNames` names the providers for `GET /sessions` and `GET /cost`
- `lib/providerlog/` - The provider ledger, `provider-ledger.jsonl` beside the config file: which provider each Claude Code or Codex session ran on, from when (JSON lines appended under a lock file, entries older than 90 days dropped at most daily, read incrementally); `lib/httpapi` appends what `native.Config.ProviderUsed` reports
- `lib/pricing/` - Model prices per million tokens (built-in `prices.json`, `--prices` file) and per-call cost calculation; `scripts/prices.mjs` compares `prices.json` with Pi's model registry
- `lib/statedb/` - SQLite persistence of conversations (`--state-db`), which loads the latest tasks' rich messages and timeline and reads earlier pages on demand; the stored state is one versioned (currently 2) meta value, `conversation_state` (`state.go`), with newer or unreadable envelopes preserved through later writes; the per-part keys of 3.7.2 and earlier aren't read
- `lib/instanceapi/` - The contract with clients that watch many AgentAPIs: who an instance is (with its `AgentType`), its summary (its `spend` split by provider in `providers`), the webhook envelope and signature, and the state a hub shows; it imports only `lib/agentstate` and `lib/util`
- `lib/hub/` - `agentapi hub serve`: a server that watches many AgentAPIs, built on `lib/instanceapi` and `lib/httpmw` rather than `lib/httpapi` (signed webhooks, summary streams, a proxy that opens any of them in the chat, the addresses machines join it by, the activity of the last year hour by hour (`activity.json`, `GET /api/activity`, the overview's heatmap), the favorite prompts among the settings its page keeps, the model providers (`providers.go`, `providers.json`, `GET`/`PUT /api/providers`, `POST /api/providers/check` (the editor's Check connection) with write-only keys inherited only at unchanged endpoints and conditional whole-list revisions (a stale PUT is 409), `POST /api/providers/sync` for the AgentAPIs, with their models' prices from OpenRouter (`prices.go`, `openrouter.go`: the public list fetched daily and on `POST /api/providers/prices`, kept in `openrouter.json`, matched by normalized name unless `providers.json` picks a model or `none`; `GET /api/providers/openrouter` searches it); the page's Providers tab, `chat/src/components/hub/providers-settings.tsx` with its logic in `chat/src/lib/hub-providers.ts` and the prices in `hub/provider-prices.tsx`), and `hub-schema.json`, the generated schema of its wire types)
- `lib/sse/` - Server-sent events as the HTML standard defines them (`Reader`, `Write`), for the hub's streams
- `lib/netaddr/` - The machine's IPv4 addresses: those other machines may reach (the hub's join URLs), and the one on the route to a host (`{ip}` in `--public-url`)
- `lib/userconfig/` - The user's settings file (`~/.agentapi/config.yaml`), which `agentapi server` reads and `agentapi hub join` writes
- `lib/registry/` - The registry of the AgentAPIs running on a machine (`~/.agentapi/instances`), which `agentapi list` reads
- `lib/dispatch/` - The hub's issue dispatch (`docs/specs/issue-dispatch.zh-TW.md`): the machines whose launchers report with signed `host.*` events (each name pinned to the launcher token it first came with; `DELETE /api/hosts/{name}` forgets one; the hub calls a launcher only at an address that isn't loopback (unless `--allow-loopback-launchers`), link-local or unspecified, checked at every connection in `launcher.go`), `POST /api/dispatch` (start, send, link_pr, stop; a signed-in browser or `--dispatch-token`; a start answers once its job is `starting` and the launcher starts it in the background, the hub adopting a start it lost the answer of from the machine's next report), the jobs, the cost of each run from `task.*` events whose instance and `server_id` both match and whose instance the hub reads at an address of the run's machine (failed background state writes stay pending for retry), and the last 50 messages their agents were sent, the prompts, the GitHub App the hub registers from a manifest (`/github/connect`) and hands out per-repository tokens of to the launchers (`/api/github/token`, signed), and `Subscribe`/`Board`, the machines and jobs the hub's stream sends after each change (`dispatch` event). The hub's page for it is `chat/src/components/hub/issues/` (Issues), with its model in `chat/src/lib/hub-issues.ts`
- `examples/issue-dispatch/` - The GitHub side of the issue dispatch: the reusable workflow a self-hosted runner in the hub's network runs (`agent-dispatch.yml`), the caller each repository adds, and how to set it all up (README, in Traditional Chinese)
- `lib/github/` - The GitHub App's JWT, installation tokens for one repository, and the REST calls the dispatch makes as the app; standard library only
- `lib/jsonapi/` - The JSON plumbing the hub, the dispatch and the launcher share: reading a body (1 MiB at most), writing an answer, and `Error`, a refusal with its HTTP status and a code, answered as `{"code", "message", "error"}` (every error the hub answers); standard library only
- `lib/host/` - `agentapi host serve`: the launcher that starts an AgentAPI for each GitHub issue a hub dispatches to the machine, in a git worktree on the issue's branch (`docs/specs/issue-dispatch.zh-TW.md`): its API for the hub behind the host token, the jobs it keeps in `~/.agentapi/jobs.json` and takes over after a restart (an AgentAPI that exited starts again after a growing wait, 5 s to 5 min, and the job fails after 5 restarts in 30 minutes; a failure to save a new child's initial record stops it before Start can succeed, and its process `server_id` tags the run), and the unix socket the jobs' git credential helper and gh shim get a GitHub token from
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

- GET `/messages` - Get the messages of the latest 20 tasks; `before` and `limit` page back (so do `/rich-messages`, `/timeline` and `/tasks`)
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

- User docs: `README.md` is the overview (features, quickstart, agents, an index of the guides); the guides are in `docs/`: `chat-ui.md`, `agents.md` (the native transport and each agent), `providers.md`, `api.md` (the conversation API, integrations and the endpoint reference), `security.md`, `cli.md`, `hub.md` and `architecture.md`. The Resource usage tables in `docs/cli.md` come from `bun scripts/measure-resources.mjs` (Linux; `native-demo` unless `--agents` names real agents). Each has a `.zh-TW.md` twin that mirrors it section by section: change both together, and keep headings, which other pages link to by anchor. Docs links and twins are checked by `go test ./internal/docscheck`: every relative link, image and `#anchor` in the tracked Markdown resolves (GitHub's heading slugs), and each twin has the same heading levels in order, fenced code blocks per section, images in order and linked files
- Chat UI in `chat/` directory (Next.js + TypeScript)
- OpenAPI schema auto-generated to `openapi.json`
- Release versions determined by `next-version.sh`; builds stamp the binary version, and `version.sh` synchronizes generated metadata
