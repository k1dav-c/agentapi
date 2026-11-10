# Changelog

## v4.1.0

### Features

- A chime tells you an agent needs you or finished while you look away from the page (its tab in
  the background, or another window in front): **Play a sound** in the chat's status menu, and
  **Sound** in the hub's bell menu, which now also has **Notifications**. It plays over plain http,
  where browsers have no notifications. In a chat the hub opens, the hub chimes for an agent that
  needs you and the chat only when it finishes. The hub now says why it can't turn on
  notifications (an http page, or blocked for the site) where it did nothing.

## v4.0.1

### Fixes

- On a wide screen, the thinking notes beside a step are no taller than the step: long thinking
  beside a short step opened a gap in the transcript, the step's row as tall as the notes. What
  doesn't fit fades out above **Show all**, which shows every note.

### Internal

- The chat builds with Next.js 15.5.29 (from 15.4.10) and the latest React 19, Radix, Tailwind and
  eslint within their ranges, and overrides PostCSS to 8.5.32: `bun audit` went from 79 advisories
  (3 critical) to one moderate, in a build tool. None reached the binary, which embeds the chat
  as static files with no Next.js server, only `bun run dev` and the build.
- CI runs actions/checkout v7.0.1 and actions/setup-go v7.0.0 (Node 24), pinned to their commits,
  where v4 and v5 targeted the deprecated Node 20.

## v4.0.0

### Breaking changes

- `agentapi server` no longer takes the flags AgentAPI 3 kept for compatibility; each now stops
  it at startup (`unknown flag`), and in `--validate`, so an AgentAPI 3 that runs with one and
  updates through its API keeps serving and says why. The settings file and `AGENTAPI_*`
  variables of the same names are ignored (the file's with a warning). `--term-width`/`-W` and
  `--term-height`/`-H` stay, still without effect, since older Coder modules start AgentAPI with
  them.
  - `--transport` (only `native`, the default): drop it.
  - `--webhook-instance-id`: use `--instance-id`.
  - `--state-file`/`-s`, `--load-state` and `--save-state`: AgentAPI 3 imported the state file
    into a state database next to it (the same name with `.db`) the first time it started with
    it; start with `--state-db` on that database. A state file never imported can be, by running
    AgentAPI 3.16 once with it.
- A state database only AgentAPI 3.7.2 or earlier opened starts without its stored settings,
  usage limit stop and retry wait, which AgentAPI 3.7.3 to 3.16 moved into `conversation_state`
  on the first open; the conversation is kept. Open such a database with AgentAPI 3.16 once first
  to keep them.

## v3.16.4

### Changes

- The hub's session cookie holds the time it was issued, signed with the hub token, and the hub
  refuses one older than 30 days itself, where only the browser dropped it. A browser signed in to
  an older hub is signed out once and signs in again.

### Fixes

- The server gives a client 10 seconds to send a request's headers, and closes a keep-alive
  connection after 2 idle minutes, where a client that sent nothing held its connection open
  forever; the hub and the launcher close idle connections after 2 minutes too.
- `POST /terminal/input` no longer waits forever when the terminal's program doesn't read its
  input (a paste into a busy full-screen program): it gives up after 10 seconds with 503, part of
  the input perhaps typed, or when the client leaves. Two inputs no longer interleave.
- The hub ends a browser's stream (`/api/events`) whose browser takes no event for 30 seconds,
  where one that stopped reading kept its handler and subscriptions for good.
- The HTML preview (`GET /files/preview`) reads each file once, however often stylesheets
  `@import` it, stops reading once its 16 MiB are spent, and stops when the request ends, where
  stylesheets importing each other many times over could keep it reading for a long time.
- Pi's adapter remembers the last 256 message IDs it gave out, which tell apart messages of the
  same millisecond, where it kept every one for the life of the agent.
- The registry entry of an AgentAPI that crashed is removed when another process got its PID: the
  entry has when its process started (`process_start`, on Linux and macOS), where it stayed listed
  as long as the PID ran.
- A webhook delivery reads the receiver's answer (up to 4 KiB) before it closes it, so the next
  delivery uses the same connection, where each delivery with an answer opened a new one.
- The pipe of an agent's output is closed when the output ends, where each start of the agent
  again left one open until AgentAPI exited.
- An interrupt Claude Code or Codex accepts after `POST /interrupt` stopped waiting for the answer
  still ends the turn within 10 seconds when the agent doesn't, where the turn could stay running.

### Internal

- The launcher's tests give their fake AgentAPIs ports of their own, picked at random below the
  ephemeral ports, where every test process used 47300-47399: two test runs at once on one machine
  (the CI runners share one) took each other's ports, and a fake that couldn't listen exited. A
  fake that can't listen says so in its log. An agent host test no longer fails on a busy machine
  when the fake agent takes over 10 ms to exit.
- `agentio.Writer.Writing`, a hook only tests used, is gone (the tests' fake agents count their
  blocked writes), and `webhook.Dispatcher.SetResolver` takes the lookups' lock.
- The Claude, Codex and Pi adapters share where an answer to the agent's request stands
  (`agentio.Answer`), and write their fire-and-forget answers with `agentio.WriteAsync`;
  `statedb` runs its transactions through one helper.
- `lib/workspacefs` tests its diffs in the package: each status, a working directory below the
  repository's top, a file too large to compare, a symlink out, and that a repository's filters,
  textconv and external diff don't run.

## v3.16.3

### Changes

- `POST /upload` answers 413 (`request body is too large`) for a body over 101 MiB, where it
  answered 400; a file over 100 MB in a smaller body still gets 400 and
  `file size exceeds 100MB limit`. An empty file part sent without a `Content-Type` gets 422.
- An escaped dot segment or slash in a session id (`/sessions/%2E%2E/resume`) gets 400, an
  invalid id, where it got 404.
- An IPv6 address without brackets in `--allowed-hosts` is refused as not a valid host, where the
  error said it must not include a port.

### Internal

- AgentAPI builds with Go 1.26.10, huma v2.40.0 and the latest golang.org/x modules and
  modernc.org/sqlite; lint runs golangci-lint v2.15.0.
- `POST /upload` is a huma handler again (huma v2.37 fixed the race on its operation), uploads are
  renamed into place with `os.Root.Rename`, and the cross-origin check asks net/http's
  `CrossOriginProtection` instead of a copy of it.

## v3.16.2

### Fixes

- An issue card's ring in Issues ends after its 1.6 s again: every event of the hub's stream
  cancelled the timer that ends it, so a card's ring could stay on for good.
- The chat no longer reads `GET /queue` every minute while the queue sits idle: it reads it on a
  timer only from an AgentAPI older than 1.0.0 (or one that doesn't say its version), which sends
  no queue events, and only until one comes.
- The notifications closed in the chat are remembered for the 50 AgentAPIs one was closed on last,
  and forgotten for the others: the browser kept them for every AgentAPI it ever opened, one per
  issue the hub dispatched.
- The chat and the hub say token counts, amounts and ages the same way, and round them the same:
  the chat said 999,600 tokens as "1000k", where the hub said "1M"; an amount just under $1,000 as
  "$1000.00", and the hub one just under $10 as "$10.00".
- Adding an AgentAPI by its URL on the hub says why the hub refused it, whatever the shape of its
  answer; signing in to the hub says a refusal other than a wrong token as the hub said it, not as
  a hub that didn't answer.

### Performance

- The hub's list renders a row or a machine's card again only when it changed: every event of the
  issue dispatch rendered every row and card, and every event of an instance every machine's card.

### Internal

- CI: a release runs on a self-hosted runner of its own, apart from the one that runs the tests,
  and a push that releases nothing (such as `docs:` commits alone) runs no tests. The release
  builds its five binaries side by side; the jobs no longer restore setup-go's cache, pin each
  action to a commit and Bun to a version, install the chat's dependencies from the lockfile, and
  check that the chat's generated API types match. A new push to a pull request cancels the jobs of
  its last, and a manual release runs on `main` only. The unused PR preview workflows are gone.
- One module has the chat's and the hub's formatters (`lib/format.ts`), and one helper keeps a
  hook's latest value in a ref (`useLatest`).
- Tests for the chat's event stream (the cache against the 400 ms start, a reconnect after a
  401), the hub's stream (signed out against unreachable), the drafts and the queue's reads.
- The chat no longer has the `gh-pages` package and the `deploy-gh-pages` and `serve-static`
  scripts, which nothing ran.

## v3.16.1

### Fixes

- The hub's version no longer runs under the buttons at the top of the hub's list, where the name
  leaves the status line too little room: the status line (live, how many agents, the version, the
  spend) has a row of its own under the name.

### Internal

- CI runs on the repository's self-hosted Linux runner, and no longer tests macOS or Windows. A
  pull request from a fork runs no job.

## v3.16.0

### Features

- The machines in Issues have **×** on an offline one, which forgets it after a dialog, as
  `DELETE /api/hosts/{name}` does: a launcher set up again, with a new token, can then join under
  its name. It is off, with the reason, while one of the machine's jobs still works.

## v3.15.2

### Changes

- The state database no longer keeps the stored settings, limit stop and retry wait under their
  old keys (`settings`, `limit_stop`, `retry_wait`) next to `conversation_state`: the first open
  moves them into it, or drops the copies 3.7.3 to 3.15.1 wrote, and deletes them. AgentAPI 3.7.2
  or earlier opened on the file afterwards starts without those settings, limit stop and retry
  wait; the conversation is kept.
- The hub no longer writes its favorites a second time at the top level of `instances.json`, where
  hubs of 3.5 to 3.7 read them; it still reads them there when the file has no `settings`. Going
  back to such a hub starts it without favorites.
- Every error the hub answers is `{"code", "message", "error"}`, as the issue dispatch's already
  were: the hub's own routes answered `{"error"}` alone, and now add a `code` (the HTTP status in
  snake case, such as `not_found`) and the same text in `message`.

### Internal

- `lib/dispatch` and `lib/host` use `lib/jsonapi`'s errors directly, the dispatch's enums are named
  in the hub's schema (`DispatchJobState`, `LauncherJobState`, …), dead code is gone, and
  `lib/github`, the launcher's credential helper, `agentapi host uninstall` and the dispatch's job
  states have more tests.
- The dispatch drops jobs that ended more than 30 days ago and old delivery IDs whether or not it
  keeps a state file (`agentapi hub serve` always gives it one).

### Documentation

- `agentapi host` has a CLI reference (README), and `agentapi hub serve`'s `--dispatch-token` and
  `--github-app-file` are in its flag tables. The architecture guide lists `lib/host`,
  `lib/github`, `lib/jsonapi` and `cmd/host`.
- The changelog has the v3.11.0 entry it missed.

## v3.15.1

### Fixes

- An issue's detail on the hub's Issues page starts fresh for each issue opened: a **Stop** being
  confirmed showed for the next issue's job, and a message half written to one agent was sent to
  the next one's.
- The Issues page's jobs come from the hub's stream only; what you do shows at once, as the hub
  answers, until the stream has the job as new. A job's answer no longer stays on the board in
  place of newer news, and the page no longer reads the machines and jobs every 10 seconds.
- Issues and the settings' **GitHub** tab read the hub's GitHub App once, together, and again when
  you come back to the page: the **Install** card on Issues goes once the app is installed, and a
  disconnect shows at once. A read from before a later one no longer lands after it, for the app
  and for the issues.
- **Forward** into an agent opened from Issues, or a reload of it, keeps Issues open below it, as
  Back does.
- **Esc** on a **Stop** or **Switch agent** being confirmed cancels it, instead of closing Issues
  (or the issue's sheet on a phone), and the focus goes back to the button.

### Internal

- The hub's page routes from one router over the browser's history (`lib/hub-route.ts`), with
  its transitions tested on a fake history.
- The chat's tests that render with effects run on happy-dom's document (`withDOM`).
- The Issues page's and GitHub settings' parts are split into smaller files and share their
  looks, the read error and the reduced-motion check; the dispatch API checks the hub's answers;
  the Issues cards' memo holds again.

## v3.15.0

### Features

- `DELETE /api/hosts/{name}` forgets a machine of the issue dispatch, and the launcher token the hub
  pinned its name to, so a launcher with a new token (its `host.token` deleted, or the machine set
  up again) can join under the name. It refuses a machine that is online (409 `host_online`) or has
  a job that works (409 `host_busy`). See [Troubleshooting](docs/hub.md#troubleshooting).

### Fixes

- The hub pins each machine's name to the launcher token it first came with, and refuses a
  `host.*` event with another token, or none, whether the machine is online or not, `host.stopped`
  included. A machine that had the hub's secret could send `host.stopped` with its own token, then
  heartbeats as another machine, get that machine's jobs' GitHub tokens and have the hub send it
  that machine's dispatches; a machine offline long enough could be claimed outright.
- A dispatch's start answers once its job is `starting` on a machine, and the hub calls the
  launcher in the background, so the first dispatch of a big repository no longer fails on the
  workflow's 60-second `curl`. A start that fails there leaves the job failed with the launcher's
  error, comments it on the issue (with the GitHub App) and forgets its delivery ID, so the
  workflow run again starts it again. Sending to a starting job is refused (409 `job_starting`).
- A launcher finishes a start when the hub's request ends, within its own limits (10 minutes of
  git, then a minute for the AgentAPI), rather than killing the job it had just started. The hub
  keeps each start until it hears how it ended: one whose answer it lost (the call failed, the hub
  restarted) is adopted from the machine's next report, or failed if the machine doesn't have it,
  and a job a machine runs that no start asked for is stopped there, its worktree kept.
- Concurrent starts no longer fill a machine past its `max-jobs`: the hub holds the place when it
  picks the machine, and a launcher that says it is full has another machine take a job the hub
  picked it for.
- A job's AgentAPI that exits starts again after 5 s, 15 s, 45 s, 2 min, then 5 min, rather than
  at the next 5-second check, and the job fails when it exits again after 5 restarts in 30 minutes
  (it failed after 5 exits in about 30 seconds). The restarts are kept in `jobs.json`, so a
  restarted launcher counts on.

## v3.14.0

### Features

- Hub settings has a **GitHub** tab beside Favorites, which the Issues page opens too (the app's
  name beside Issues, or **GitHub settings…** in the repository menu). It shows the hub's GitHub
  App, its owner, and each account it is installed on with its picture and repositories, and
  links to GitHub in a new tab: **Choose repositories**, each account's installation, and the
  app's settings. **Reconnect…** registers a new app, for your account or an organization; the
  hub keeps its app until GitHub hands the new one back. **Disconnect…** asks first and says what
  stops. The tab reads the app again when you come back from GitHub. See
  [GitHub settings](docs/hub.md#github-settings).
- `GET /api/github` says whether the app's owner is a user or an organization, and has the app's
  settings page and each installation's page and avatar.

### Fixes

- A new GitHub App connected in place of the hub's drops the old app's cached repository tokens,
  which the hub went on handing to jobs until they expired. The connect page names the app it
  would replace.

## v3.13.0

### Features

- The hub's page says which AgentAPI version the hub runs, beside how many agents it follows
  (`dev` for a build of no release).

## v3.12.1

### Fixes

- The hub's Issues page is as wide as the overview it takes the place of, so the page keeps its
  edges when Issues opens.

## v3.12.0

### Features

- The hub's Issues dialog is a page now, in the overview's place (◎ in the header, or `i`). Its
  board puts the issues by stage, with what needs you first: a failed job, an agent that waits on
  you, a pull request to review; then the agents at work, the issues to do and those done. A bar
  counts the stages, and the machines show their room and agents. **Assign** hands an issue to the
  agent and machine the button shows, one tap; its arrow lists the others, with why a machine
  can't take it. An issue opens into its detail: its state and actions, **Switch agent** and
  **Stop** (both ask first), its spend, and a timeline of its runs and the messages its agents were
  sent. It works on a phone. See [Issues](docs/hub.md#issues).
- The hub keeps the last 50 messages sent to each job's agents, from its page or from a review or
  `/agent` comment on GitHub, with where each came from (`messages` on `GET /api/jobs`, kept in
  `dispatch.json`; older files read as before).
- The hub's stream (`GET /api/events`) sends the dispatch's machines and jobs at first and after
  each change (`dispatch`), so the page no longer reads them every 10 seconds; with a hub before
  this one, it still does. ◎ counts the issues that need you, and the list's row of an agent that
  works on an issue shows the issue and its pull request, which open it in Issues.
- The board shows the issues of the 20 repositories pushed to or changed last
  (`GET /api/github/repos` lists them so, with `pushed_at` and `updated_at`); its menu finds any
  other.

## v3.11.0

### Features

- Issue dispatch: the hub hands GitHub issues to agents on your machines. See
  [Issues](docs/hub.md#issues) and [examples/issue-dispatch](examples/issue-dispatch/README.md).
  - `agentapi host` is the launcher on each machine: it gives each issue a worktree on
    `agent/issue-<n>` and an AgentAPI of its own, reports the machine to the hub in signed `host.*`
    events, and hands the jobs' `git` and `gh` a token for their repository. `install` and
    `uninstall` manage its systemd user service.
  - The hub starts, messages, links and stops the jobs on the launchers (`POST /api/dispatch`, also
    with `--dispatch-token` for a workflow's runner), and keeps their runs' cost. Switching agents
    keeps the worktree.
  - The hub registers a GitHub App from the browser through the manifest flow
    (`/github/connect`), gives the jobs tokens for one repository, comments progress and spend on
    the issue, and deletes the branch when the pull request closes.
  - The hub's page has an Issues dialog: the machines, the app, a repository's open issues to
    assign, each job's state, cost and pull request, a message to its agent, Retry, Switch, Stop
    and New issue.
  - `examples/issue-dispatch` has the GitHub workflow a self-hosted runner in the hub's network
    runs to turn a repository's issues, reviews and `/agent` comments into dispatches.

### Fixes

- `agentapi server --print-openapi` (which `make gen` runs) no longer replaces the PID file of the
  server it runs beside, and on Linux a PID file of another install of agentapi (another path, a
  build from source) counts as one that runs rather than stale.

## v3.10.1

### Fixes

- A restart in place (`agentapi update --restart`, an update from the hub or `POST /update`,
  `agentapi restart`, SIGUSR2) of an AgentAPI without `--state-db` keeps the agent's session. It
  started a new one, and the conversation was gone. The restart now hands the session to the new
  program, which resumes it and reads the conversation back from the session's log, as opening it
  from the Sessions does; Claude Code, Codex and Pi only. The queue, open requests, chosen settings
  and a usage-limit stop or retry wait still start anew; `--state-db` keeps those too. A session
  whose log is gone starts anew, with a warning in the chat. The summary's `update` says so in
  `keeps_session`, and the hub's update dialog warns only about agents that start a new session.
  The restart that updates from an earlier release still starts a new session, since the earlier
  release doesn't hand it over.

## v3.10.0

### Features

- The hub forgets every stopped agent in one go: **Forget N stopped** under the overview's Offline
  tile, or **Forget all** atop the list under the Offline filter (on a phone too). A dialog lists
  them by name and machine first. Unreachable agents stay. The new `POST /api/instances/forget`
  forgets those of the IDs sent that are stopped at that moment, and answers with those it forgot.

## v3.9.0

### Features

- On the hub, an idle agent that finished in the last 30 minutes, seen or not, shows a green ring
  with a check in the list, and a green ring as its tab's dot and in the bar's switch, so the agents
  that just ran stand out from those idle for long. It turns grey after half an hour.

## v3.8.0

### Features

- `agentapi update --allow-update` also writes `allow-update: true` into the config file before
  `--restart`, so the restarted AgentAPIs take updates from a hub from then on. Under `sudo` it
  writes the file of the user who ran `sudo` and leaves it theirs; `--config` names another file.

## v3.7.5

### Changes

- The chat's task filter names the states as the tasks' badges do: Working and Done, not Running
  and Completed.

### Internal

- The chat's conversation (messages, rich messages, requests, tasks and the point they are at) is
  one reducer, `conversationReducer` in `chat/src/lib/conversation-state.ts`, with tests for its
  epoch, replay, restore and reset rules; the event stream, the cache restore, starting over, the
  request REST calls and the drafts dispatch to it.
- The chat's API types are aliases of the generated schema, and its requests name their route in
  the generated `paths` (`route()`), so a route the server drops fails to typecheck. `lib/` no
  longer imports from `components/`.
- The composer's phone and desktop buttons are one `ComposerActions`; the composer's placeholder,
  the limit card's content (`LimitStopCard`), `remarkLinkify` and the byte formats are tested
  modules. Shared hooks (`useLoaded`, `useAction`, `useCopy`) and `DecisionCardFooter` replace
  copies. The file viewer's short size has one decimal (12.1 KB).
- Storybook, whose one story couldn't render, is gone; `eslint-config-next` matches `next`
  (15.4.10); `lib/ui-copy.ts` is inlined; the tests' shared helpers are in `chat/src/test/`.

## v3.7.4

### Internal

- The hub page's types come from the hub's own schema instead of hand-written copies of the Go
  types: `make gen` writes `lib/hub/hub-schema.json` (`hub.Schema`, from the Go types through
  huma's registry, with `HubLimits`) and `bun run gen:api` writes `chat/src/lib/hub-schema.ts`
  from it; a Go test and a chat test fail while either is stale. One `parseHubEvent` reads the
  event stream, and the frame's messages are checked field by field in both directions.
- The hub keeps its favorites as the first of its settings, `"settings": {"favorites": [...]}` in
  `instances.json`, still version 1. It writes the top-level `"favorites"` too for this release,
  which hubs of 3.5 to 3.7 read, and reads `settings` first, else those, so going back and forth
  keeps them. The store's events say their kind. The stream's event keeps its name, `favorites`.
- `chat/src/lib/hub.ts` is split by topic (groups, machines, filters, what a row says, spend,
  work time, versions); `HubView` is split into hooks and parts, and the rows take what they share
  from a context. The page's browser settings go through one typed module, in the formats they
  had. The stored list order "machines", which no release wrote, is no longer migrated. The tests
  share their builders, in the chat and in `lib/hub`.

## v3.7.3

### Internal

- A native conversation reports its state (capabilities, login, settings, usage, the settings
  chosen, a limit stop or retry wait) whole through one observer method,
  `Observer.StateChanged(agentstate.ConversationState)`, instead of one method for each part, and
  the state reaches the state database only through the emitter. `native.Config.State` restores
  it. Adding a piece of state takes the files the checklist in
  [docs/architecture.md](docs/architecture.md#adding-a-piece-of-conversation-state) lists.
- The state database keeps the settings chosen, the limit stop and the retry wait in one
  versioned JSON value, `conversation_state`. The first open of a database an earlier AgentAPI
  wrote moves its `settings`, `limit_stop` and `retry_wait` keys there. This release writes those
  keys too, so going back to an earlier AgentAPI keeps the limit stop, retry wait and settings, and
  what it changes there wins when this one opens the file again.

## v3.7.2

### Internal

- The values a native agent reports (capabilities, login, settings, usage, requests, limit stop,
  retry wait) move to the leaf package `lib/agentstate`: `lib/instanceapi`, `lib/convstate`,
  `lib/webhook` and `lib/statedb` no longer depend on `lib/native`.
- The HTTP pieces the hub shares with the server (host and cross-origin checks, compression, the
  chat's file server and its embedded build, now in `lib/httpmw/chat/`) move to `lib/httpmw`, so
  `lib/hub` and `cmd/hub` no longer build `lib/httpapi`. A checkout from before keeps an untracked
  `lib/httpapi/chat/` that can be deleted; `make embed` builds the chat into the new place.
- The 118 type, constant and variable re-exports in `lib/httpapi`, `lib/convstate`, `lib/hub`,
  `lib/webhook` and `cmd/server` are gone: huma names a schema after the Go type, so
  `openapi.json` is unchanged.
- `native.ClassifyFailure` holds the order of a failed turn's signals (usage limit, then login,
  then transient) for all three adapters; `native.Policy` holds `--auto-continue`,
  `--retry-delays` and the restart delays, and `--retry-delays`' default comes from it.
- `TurnResult.StopReason` is a named type the exhaustive linter checks, and the rate limit
  statuses are constants. Code only tests reached is removed or moved into test files.

## v3.7.1

### Fixes

- A message that doesn't reach the agent (the agent refused it, or the connection failed) no longer
  ends a usage limit stop or a wait to retry a failed turn: the stop or the retry shows again, and the
  queued messages still wait for it. The same goes for a `POST /retry` that fails.
- While an update waits for the agent to be idle, a continuation after a usage limit or a retry that
  falls due waits too, like the queued messages, and goes out after the restart. It could start a
  turn that kept the update waiting.
- An update no longer waits forever for an agent that exited in the middle of a turn and isn't
  started again: it waited for the agent to report that the turn ended.
- When the agent restarted or exited while it worked on a continuation or a retry, the next turn no
  longer counts as one: a usage limit that stopped it scheduled a continuation without
  `--auto-continue`, and a transient failure counted as the next retry rather than the first.

## v3.7.0

### Features

- A turn that failed on a transient API error is tried again 15 times by default, about an hour in
  all: after 30 seconds, 1 and 2 minutes, then every 5 minutes (`--retry-delays 30s,1m,2m,5m*12`;
  it was `30s,2m,5m`). `--retry-delays` takes `delay*n` for a delay repeated n times, up to 100
  retries.

## v3.6.1

### Fixes

- A Markdown link in the agent's reply whose target is a path, such as
  `[the table](/home/me/research/對照.csv)`, opens the file in the file viewer (at its line with
  `#L12` or `:12`). The browser resolved it against the page's address and missed it, more so in the
  hub, where the chat sits in a frame on the hub's address.

## v3.6.0

### Features

- On the hub, Back (a phone's back gesture) returns to the agent you had open before, one at a time,
  and from the first to the list. Moving to another agent used to replace the page, so Back went
  straight to the list. **×** and **Esc** still return to the list at once, also from a page opened
  at an agent's address.
- Every agent tab on the hub has a state dot, also the ones collapsed to their mark: an idle agent's
  is grey, a grey ring until it has run (a task, or work the hub saw this week). The bar's switch
  shows the same dots.

## v3.5.0

### Features

- The hub keeps your favorite prompts. The gear in the hub page's header opens **Hub settings**,
  whose **Favorites** section adds, edits, deletes (with **Undo**) and reorders them: a prompt of up
  to 20,000 characters and an optional title, up to 100 of them. They are saved on the hub, so every
  browser signed in to it has the same, and an open page shows a change at once.
- In the chat of an agent opened from the hub, **☆** in the composer lists the favorites (with a
  search box past seven) and inserts the one you pick in the message, without sending it: as the
  message, or as a paragraph after what you typed. Ctrl/⌘+Z takes it back out. **Manage
  favorites…** opens them in the hub's settings. A chat opened on its own has no ☆.
- The hub's API has `GET` and `PUT /api/favorites`, and its event stream sends `favorites`. They
  are kept in the hub's file of instances, still version 1: a hub before 3.5 reads it, and drops the
  favorites when it writes it.
- `POST /upload` and the chat's attachments take files of up to 100 MB (10 MB before). The server
  writes an upload to disk as it reads it instead of holding it in memory. An image over 10 MB
  still reaches the agent as its path rather than as an image.

## v3.4.0

### Features

- While two or more notifications show, **Clear all** below them closes them at once. Each counts as
  closed, so a reload doesn't bring it back.

## v3.3.0

### Features

- A turn that fails on an API error that may pass is tried again instead of letting the queue move
  on: a 502 from a gateway ("API Error: 502 Bad Gateway. This is a server-side issue"), another 5xx
  or 429, an overloaded API, a dropped connection. AgentAPI waits 30 seconds, then 2 and 5 minutes
  between retries (`--retry-delays`, `off` to turn them off), and each retry sends "Continue from
  where you left off." so your message doesn't go twice. The failure is a warning while retries
  remain, and an error after the last. `GET /status`, `status_change`, the summary and the
  `instance.*` webhook data report `retry_wait` (the agent's message, the API status, which try,
  `retry_at`); `POST /retry` tries now and `DELETE /retry` gives up and lets the queue go. The wait
  survives a restart with `--state-db`. Usage limits and login problems are told apart first.
- The chat shows the wait as a card above the composer, counting down to the retry ("Retrying in
  0:28 (1 of 3)") with **Retry now** and **Cancel**, and queued tasks say they wait for it. The
  agent counts as working meanwhile, in the chat and on a hub, whose row reads **Retrying**.
- The demo agent fails a turn with a 502 on the word `gateway`.

## v3.2.2

### Fixes

- A Markdown link whose label has Chinese punctuation, such as `[mITXPC：Dynatron A26](https://…)`,
  opens its URL again. The chat cut its label at the punctuation and took the rest of the label as
  the URL, a relative one. Only a bare URL that runs on into the punctuation after it is cut now,
  and a bare `www.` address cut so keeps its `http://`.

## v3.2.1

### Fixes

- The hub's machine cards read more clearly, on a phone especially. The host name has the first line
  to itself, so it no longer wraps into three, and the start most machines' names share (such as
  `coder-<user>-`) is dimmed. The version and spend sit beside the tabs, and only the picked tab
  carries its agent's name. A card with one agent shown, such as with an agent filter, has a label
  instead of a lone tab and shows its spend once. The directory's name moved from the card's header to
  the agent below it.

## v3.2.0

### Features

- The hub's list has a **By machine** toggle beside the order's switch: in either order, each
  machine's agents share one card. The card names the machine once with what its agents share
  (their directory, AgentAPI version and spend of the last 24 hours), then a tab per agent with a
  dot in its state's color, and below them one agent with its menu. A click on a tab opens that
  agent; `j` and `k` step between the cards, ← and → switch the agent a card shows. In Groups the
  machines come what-needs-you-first within each group; in Recent changes, without group headers,
  the machine of the latest change first, its card showing the agent that changed last unless you
  picked another since. Pinned agents stay one row each at the top. The browser keeps the toggle
  (`agentapi.hub.by-machine`) and the tab picked on each card.
- The bar above an agent opened from the hub has a button for each agent of its machine, with the
  same state dots, in every list order: one click switches between, say, Claude Code and Codex on
  one machine.
- The hub overview's Spend, Tokens and Work time cards have a **By machine** table, the most this
  week first, at most eight rows (the rest in one). Spend and tokens count each scope once, as the
  totals do. The top-five tables name each agent's machine next to its name.

## v3.1.1

### Fixes

- Concurrent `PUT /webhook` requests no longer drop each other's fields: each one reads, changes
  and writes the configuration in turn.
- The human reply protocol drops only its oldest receipt past 256; it cleared all of them, and a
  reply retried right after that was delivered twice.
- The chat keeps the 50 closed requests the server keeps (it kept 100), so a chat that resumes with
  `?since=` holds the same requests as one that loads the full state.
- Without `--state-db`, a restart in place (SIGUSR2, `POST /update`, `agentapi restart`) no longer
  sends `--initial-prompt` again into the new session once the agent got it; a restart before then
  still sends it.
- Claude Code costs: an unitemized call now holds only tokens Claude Code counted that its log
  didn't list, priced at AgentAPI's prices. A difference in price alone (the same tokens) no longer
  shows as an unitemized call; the server logs it once per model.

### Changes

- The chat no longer handles the PTY transport, which AgentAPI 3 doesn't have.

## v3.1.0

### Features

- The hub's list filters by agent too: a row of chips for Claude Code, Codex, Pi and Other (any
  number at once), with the state filter and the search. The browser keeps the choice.

## v3.0.1

### Fixes

- The hub moves a joined instance to the URL an event names only once its old URL is dead to the
  hub: unreachable past the 10 s grace, stopped after missed heartbeats, or answered by another
  AgentAPI. A machine with the hub secret could take over another's instance while the hub
  connected to it, after a restart or a dropped stream, and get its token, messages and approvals.
  An instance stopped by `instance.stopped` is tried at its old URL first. A machine whose address
  changed moves at most a minute later than before, about two minutes after the change.
- The hub's proxy forwards an instance's requests, the update included, only while it is connected
  (reads its summary at its URL); otherwise it answers 503 and sends nothing.
- `instances.json` keeps the URL where the hub last read each instance's summary
  (`confirmed_url`); a restarted hub follows the instance there first. A hub before 3.0.1 ignores
  the field.

### Changes

- The hub no longer reads the PTY-era fields of an AgentAPI 2: a 2.x PTY agent's terminal dialog
  no longer shows as needing you on a 3.0.1 hub (its row shows it idle or working; answer the
  dialog in that AgentAPI's own chat). `terminal_prompt` and the `dialog` attention leave the
  summary schema, `pty` the `Transport` enum; AgentAPI 3 never sent them. The hub still follows a
  2.x instance and shows the rest of its summary.

### Internal

- A release waits only for the Linux tests (test, race, lint). macOS and Windows still run on every
  push to `main` and every PR, but no longer hold a release up: a macOS job that got no runner
  skipped the v3.0.0 release twice.

## v3.0.0

### Breaking changes

- MCP management is removed: the Explorer's MCP tab and the `/mcp` endpoints (`GET`/`PUT /mcp`,
  `POST /mcp/check`, `POST /mcp/servers`, `PATCH`/`DELETE /mcp/servers/{name}`,
  `GET /mcp/profiles`, `PUT`/`DELETE /mcp/profiles/{name}`, `POST /mcp/profiles/{name}/apply`),
  which now answer 404. Configure MCP servers in the agent: `claude mcp` or `.mcp.json` for Claude
  Code, `codex mcp` or `$CODEX_HOME/config.toml` for Codex, `~/.pi/agent/mcp.json` for Pi; then
  restart the agent (Explorer → Session → Restart, which starts a new session). The servers
  AgentAPI wrote stay in those files, and the agents keep loading them; the saved profiles stay in
  `.agentapi/mcp-profiles.json` in the working directory, unread. MCP tool calls and Codex's MCP
  elicitations still show in the chat.
- The PTY transport is removed: AgentAPI runs Claude Code, Codex and Pi natively, and
  `native-demo`. Other agents (aider, goose, gemini, amp, cursor, auggie, amazonq, opencode,
  copilot, kimi, a custom program) and `--transport pty` fail at startup and in `--validate`, which
  name v2.5.1. To keep them, install the v2.5.1 binary from its GitHub release: `agentapi update`
  installs only the latest. An instance with `--allow-update` that runs a PTY agent refuses the
  restart into 3.0 and keeps serving, but `POST /update` replaced the program on disk first: put
  v2.5.1 back before it restarts.
- Gone with it: `agentapi attach`; the chat's TTY mode and its terminal-prompt card;
  `GET /internal/screen`; `POST /message` with `type: "raw"` (now 422); `terminal_prompt` and
  `terminal_columns` in `GET /status` and `status_change`; the handoff's `terminal` kind;
  `request.opened` and `request.closed` webhook events of kind `terminal_prompt`. Summaries and
  webhook events report `transport: native`; a hub still shows an AgentAPI 2's `dialog`.
- `--transport` is deprecated and takes only `native`; `--term-width` and `--term-height` have no
  effect. Without `--type`, the agent command's name picks the agent (`-- /usr/local/bin/codex`,
  `-- native-demo`).
- A `--state-db` that AgentAPI 2 kept for a PTY agent opens: the agent starts a new session below
  the conversation, which goes on natively. A `--state-file` is imported the same way. AgentAPI
  2.5.1 opens a 3.0 database.

## v2.5.1

### Fixes

- A Codex fork of a fork reads every thread it continues, up to 16 forks back: its history, first
  prompt and prompt count start with the first thread's. Only the thread it forked was read.
- `~/.agentapi/config.yaml` (which holds the webhook secret) and the registry entries are synced to
  disk before and after they are renamed into place, as the hub's file is: a crash or power loss
  could leave them empty.
- Pi: an interrupt whose abort Pi didn't confirm in time still ends the turn. The adapter forgot the
  abort, so a late answer didn't cancel an extension command's dialog and the 10-second deadline
  never started: a turn waiting on a dialog could hang.
- `agentapi update --force --check` says `Already up to date.` when it is; it said an update was
  available. `--check` only reports, with or without `--force`.
- The check of the new program before a restart in place retries for about a second while Linux
  says the program is busy (`text file busy`), as the update's own check does: a process forked
  while the program was written holds it until it execs, and the restart failed.

## v2.5.0

### Features

- The hub shows how long the agents worked: a **Work time** card on the overview with the last 24
  hours and 7 days, each group's share and the agents that worked the most, and each row's 24 hours
  next to its spend (hidden while the row shows a usage-limit bar). Only `working` counts, and only
  while the hub follows the agent: time before this version, while the hub was down or while it
  couldn't reach the agent isn't counted. The hub keeps the spans of the last 7 days in its file of
  instances; `GET /api/instances` and the `/api/events` stream carry them as `work`
  (`last_24h_seconds`, `last_7d_seconds`, `working_since`). A hub of 2.4.0 or older reads the file
  and drops the spans the next time it writes it.
- The summary's `spend` has the tokens of the same calls, over the last 24 hours (`day_tokens`) and
  7 days (`week_tokens`): `input`, `output`, `cache_write`, `cache_read` and their `total`, each
  call once as for its cost (a fork's copied calls count in the earlier session), models without a
  price included. Each hour of `GET /sessions` and `GET /cost` has its `tokens` too. The hub shows
  them in a **Tokens** card below Spend (by kind, the agents that used the most, each group's
  share, and how many agents report them) and in the title of a row's spend; counts read `950`,
  `12k`, `3.4M`. AgentAPI 2.4.0 and older send no tokens, and don't count.

## v2.4.0

### Features

- A restart in place (SIGUSR2, `POST /update`, `agentapi restart`) first runs the new program with
  the same arguments, variables and config file under a hidden `agentapi server --validate`. It
  checks the flags, values, placeholders, port, agent and transport, the prices file and the server
  settings without listening or starting anything; the running server checks that the new program
  can write its state database. If the check fails, the server keeps serving and reports why, as for
  a program that doesn't run: in its log, as `failed` in `POST /update`, and in `agentapi restart`.
  A new program that predates `--validate` is checked with `--version` only. A `--port` outside
  0–65535 is refused at startup.
- Uploads the conversation no longer refers to are removed once they are 7 days old: at start, every
  hour, and after the conversation is cleared or another session is opened. An upload stays while
  any message, rich message, request or queued message names it. Only the folders AgentAPI creates
  in the uploads directory are removed, never through a symlink.

### Performance

- A streamed rich message is stored without copying it on every update: 2000 updates of one message
  took 550 ms and 830 MB, now 3 ms and 2 MB.
- The shell terminal keeps its scrollback in a ring, so output into a full scrollback no longer
  copies 256 KiB per chunk.

### Internal

- A release runs the tests of its commit first (`release.yml` calls `go-test.yml`) and publishes
  only if they pass, on a push to `main`, a manual run or a release published by hand. A push that a
  later one already released, or that is no longer on `main`, isn't released. On `main`, the tests
  appear as **Release / Go Tests**.

## v2.3.0

### Features

- The hub's file of instances has a version: `instances.json` is now `{"version": 1, "instances":
  [...]}` (the old list is read as before), and `instances.json.bak` keeps the file
  read at startup. A hub of 2.2.0 or older sets the new file aside and starts empty; to go back,
  restore the `.bak` or run `jq .instances`. The hub writes it in the background, at most once a
  second and when it stops, instead of on every change while holding its lock.

### Fixes

- A lookup of `{ip}` no longer holds up the server. AgentAPI looks the address up in the
  background, before `instance.started`, after a webhook change, before each heartbeat and every 30
  seconds, each lookup giving up after 3 seconds; events, summaries and the `Host` check read the
  last answer. Before, the lookup ran while the conversation state was locked, so a slow DNS (a VPN
  or Wi-Fi drop) stalled every event stream, `/status` and the agent's own events.
- Updating the agent's CLI before a restart (`codex update`, `pi update --self`) runs in its own
  process group, killed with whatever it started after 3 minutes. A child of npm that kept the
  output open made the restart, and the queue behind it, wait forever. Updates of the same CLI from
  several AgentAPIs take turns.
- A token generated by a bare `--api-token` survives a restart in place (SIGUSR2, `POST /update`,
  `agentapi restart`). Every restart generated another, and signed-in browsers, the hub and
  `agentapi attach` were locked out.
- What the agent started (a dev server run in the background, MCP servers) is stopped with it when
  the agent exits, on its own or in a reload, with SIGTERM and, 2 seconds later, SIGKILL. It
  outlived the agent and piled up with every restart.
- The agent no longer gets AgentAPI's secrets in its environment: every `AGENTAPI_*_TOKEN` and
  `AGENTAPI_*_SECRET` (webhook and hub secrets, hub tokens) is removed, not only the API token.
- `agentapi update` syncs the new binary and its directory before and after the rename, so a power
  loss can't leave a cut-off `agentapi`. `--restart` restarts the servers even when the binary was
  already up to date; the lookup has 1 minute and the download 10.
- SIGUSR2 is caught from the start: one that comes while the server starts restarts it once it
  serves, instead of ending it.
- The lifecycle stays `restarting`, `exited` or `failed` until the agent is back; the agent's
  status (`starting`, a PTY screen that changes) no longer replaces it, so the hub shows a failed
  restart as failed.
- A PTY snapshot taken while the conversation is reset can no longer put the old conversation into
  the new one (and its webhook events out again).
- A queued message is stored as the queue changes, not on the next tick, so a crash right after it
  was sent can't send it again after the restart.
- A PTY send has a context: it ends after 1 minute or when AgentAPI stops, and a message still
  waiting goes back to the queue. A screen that never settled kept `Stop` waiting.
- Shutdown waits at most 2 seconds, and never more than half of its time, for `instance.stopped`,
  leaving the rest to the open requests and the state database.
- Pi: a prompt while a turn runs is refused before it reaches Pi, and a turn whose `agent_settled`
  doesn't come ends 10 seconds after `agent_end`, with a format-drift warning.
- Hub: connecting to an instance times out after 30 seconds, as Go's default client does; a host
  that dropped packets kept an instance connecting for about 2 minutes. An instance added by hand
  ignores `instance.stopped`. The delivery IDs the hub took are kept across a restart until their
  signature expires, so an event can't be replayed right after one. A summary sent on several
  `data:` lines is read whole. Reading a webhook event is cut after 10 seconds, and a bad signature
  is logged at most once a minute with a count.
- Costs: Claude Code calls without a message or request ID each count (only the first did); a
  Codex call whose running total started over counts its own tokens; a model is unpriced when any
  of its calls is; on macOS, a fork's copied calls stay with the session it was forked from. Claude
  Opus 4 and 4.1, Sonnet 4, Haiku 3.5, Sonnet 4 and 4.5 above 200K input tokens,
  `codex-mini-latest` and the GPT-5 Codex models now have prices; a variant (`-mini`, `-nano`,
  `-pro`) no longer takes its base model's price, and a model without one is logged once.
- Earlier sessions: a Claude Code log whose first lines belong to no session is listed. `POST
  /sessions/{id}/resume` answers 400 to an id that would name another file, and `POST /upload`
  answers 400 to the names `.` and `..`; control characters are dropped from uploaded names, and
  long names are cut to 255 bytes.
- The PTY transport's session-log watcher follows a log that was truncated or replaced, skips lines
  over the length limit, and retries after a read error instead of stopping.
- Windows: `agentapi list` and the hub drop the registry entry of an AgentAPI whose process is gone,
  as on Linux and macOS; every entry was kept.
- `agentapi update` and `POST /update` no longer fail now and then with "the downloaded binary
  doesn't run: text file busy" on Linux, and no longer say a program reached through a symlinked
  directory (macOS's `/var`) is linked to from its own path.
- Chat: a hub frame that reloads shows its bar again; a queued send that fails keeps **Retry**; hub
  actions the hub refuses (pin, group, mute, forget) show why, and **Forget** closes the agent only
  once it's forgotten; the hub keeps the cached chats of up to 100 instances; the list's keys work
  only in the list; a replay of only `session_sync` ends at once; a terminal asks for the token when
  its sign-in ended; an HTML preview can't fetch or post anything.

### Performance

- The hub page re-renders only its times every second while an agent works, not every row.
- The chat skips the layout of tasks out of sight in long conversations.
- `GET /timeline?subagents=true` and `GET /subagents/{id}` keep each subagent's events until its
  log changes (409 ms to 3 ms for 20 logs of 1 MB); costs keep about a third less per call.

### Internal

- CI runs the tests with the race detector, on macOS, and the Windows code's tests on Windows; the
  release checks each binary's platform. e2e runs the native transport against a scripted Claude
  Code through a restart in place.
- `lib/sse` reads and writes server-sent events for the hub; `lib/filelock`; Pi matches responses
  with `agentio.Calls`; `agentlog.Follower` tails a log for the PTY watcher; the hub page and the
  chat provider are split into hooks.
- `lib/httpapi` is split: the conversation state (`EventEmitter`, tasks, summary) is
  `lib/convstate`, webhook delivery and its observer `lib/webhook`, and the file API's reads,
  diffs and search `lib/workspacefs`. The HTTP API and its schema are unchanged.

## v2.2.0

### Features

- `{ip}` in `--public-url` is this machine's address on the route to the hub, looked up again at
  most every 30 seconds while AgentAPI runs, so a machine that got a new address from DHCP reports
  it with its next heartbeat and the hub follows once the old one stops answering. A machine joined
  with a fixed address showed as unreachable after its workspace restarted. `agentapi hub join` and
  the hub's join command and prompt now use `--public-url 'http://{ip}:{port}'`; join again with it
  to fix a machine joined with a fixed address. An AgentAPI accepts the address `{ip}` stands for as
  a `Host`.

## v2.1.2

### Fixes

- The hub's **Recent changes** order puts the agents that have had a task (one runs, finished, or
  ran before) first, and those with no task yet and the muted ones last, each part by its latest
  change. In 2.1.1 an idle or stopped agent that had done work fell below the ones that needed you or
  worked, and an unused one that needed a login came first.

## v2.1.1

### Fixes

- The hub's **Recent changes** order puts the agents with something going on first (they need you,
  work, or finished a task you haven't seen), then the idle, muted, stopped and unreachable ones,
  each part by its latest change. An idle agent that reconnected or stopped came before the busy
  ones.

## v2.1.0

### Features

- A native agent that crashes or fails to start is started again on its session after 2, 10 and 30
  seconds, instead of AgentAPI stopping with it. The lifecycle reads `restarting` meanwhile and
  `failed` after the last attempt, while the server keeps serving; **Restart** (`DELETE /messages`)
  tries again. The count starts over once an agent has run for 5 minutes. An agent that lost its
  login waits for the login instead. The PTY transport is unchanged. `native-demo` crashes on
  `crash` and `crashloop`.
- `agentapi restart [port…|--all]` restarts running servers in place: each stops as on SIGTERM,
  then runs its program again with the same PID, arguments and environment, so its PID file and
  the process that started it stay valid. `agentapi update --restart` restarts the servers running
  the binary it replaced; under sudo, those of the user who ran it (`SUDO_USER`). SIGUSR2 restarts
  one server; if the new program doesn't run (`--version` fails), the server keeps serving. With
  `--state-db` the conversation and the agent's session carry over. Servers of 2.0.0 and earlier
  don't take the signal: restart those yourself once.
- `--state-db` and `--pid-file` fill in `{port}` and `{hostname}`, and paths may start with `~`, so
  the config file every AgentAPI on a machine reads can keep each one's conversation:
  `state-db: ~/.agentapi/state/{port}.db`. A `state-db` or `pid-file` in the config file without
  `{port}` is ignored with a warning, since the second server would fail to start.
- `--log-level` (`debug`, `info`, `warn` or `error`; `AGENTAPI_LOG_LEVEL`, or `log-level` in the
  config file) for `agentapi server`, and for `agentapi hub serve` (`AGENTAPI_HUB_LOG_LEVEL`).
- The hub shows the AgentAPI version of each instance: next to its name in the list, marked
  `older` when it is older than the hub's, in the overview's cards and the open instance's bar, and
  in a **Versions** card on the overview with how many connected instances are older and how to
  update them. Search finds instances by version. `GET /api/instances` and the `instances` event
  carry the hub's own `hub_version`.
- On a desktop, drag the edge of the hub's list to change its width (280 px up to 640 px or half the
  window), or focus the edge and use the arrow keys; double-click or Enter resets it to 360 px. The
  browser keeps the width.
- Update an AgentAPI through its API, and from the hub. A machine opts in: `allow-update: true` in
  `~/.agentapi/config.yaml`, `--allow-update`, or `agentapi hub join --allow-update`. `POST /update`
  installs the latest release of k1dav-c/agentapi, checked against its `checksums.txt` and that it
  runs as that version, and restarts in place once the agent is idle (`{"when": "now"}` doesn't
  wait); `DELETE /update` cancels. The request can't choose a URL or version, and AgentAPI never runs
  sudo: a program in a directory the user can't write, such as `/usr/local/bin`, reports in the
  summary's `update.blocked` the one-time move to `~/.local/bin` behind a symlink. The AgentAPIs of
  one machine take turns on a lock next to the program, and the second only restarts. The summary's
  `update` reports how far an update got.
- The hub's row menu offers **Update AgentAPI…** (once idle, or now), with its progress in the row
  and the open instance's bar, and says what a machine needs when it can't; the Versions card
  updates every older instance that can at once.
- The hub's **Add agents** dialog, and the guide of an empty hub, join a machine: pick the hub's
  address (the hub lists its own: the `--bind` address, or its network interfaces, LAN first),
  choose whether to allow updates from the hub, and copy the `agentapi hub join` command or a prompt
  for an agent on that machine. A hub with a token fills in its secret, masked until shown; a hub
  without one never shows it. `GET /api/join` returns the addresses, and the secret on a hub with a
  token. `agentapi hub serve` logs the join command for each address when it starts, without the
  secret.
- The `agentapi-hub-join` skill covers updates from the hub: `--allow-update`, the one-time move of
  a program in `/usr/local/bin`, and a state database to keep conversations across updates.
- The hub's list can also show every agent in one list, the most recent change first, whatever its
  group: a switch next to the search box picks **Groups** or **Recent changes**, and the browser
  keeps the choice. A change is the hub losing, finding or stopping an agent, a new state, a task
  that starts or ends, what the agent waits for, and its title, queue, version or update; opening an
  agent, its messages, spend and edits to groups or mutes don't move it. The hub keeps each agent's
  `changed_at` in its file and sends it with the instances.
- Pin agents to the top of the hub's list, as on GitHub: **Pin to top** in a row's menu (or `p`)
  puts it in a **Pinned** section above the rest, in either order, and **Move up** and **Move down**
  arrange the pinned ones. The hub keeps the pins, so every browser and phone shows the same; they
  don't count as changes. `PATCH /api/instances/{id}` takes `{"pinned": …}`, and `PUT /api/pins`
  sets their order.

### Fixes

- The hub's copy buttons work on a hub served over plain HTTP, where browsers offer no clipboard API.
- A signed event can no longer move an instance the hub follows. An `instance.*` event without
  `server_id` is refused (400); no event moves an instance the hub is connected to, or one added by
  hand; moving one drops the token stored for its old URL. The hub also refuses events without a
  delivery ID, which a copy could replay for 10 minutes. All joined machines still share the hub
  secret, so any of them can announce an instance the hub isn't connected to.
- Webhook deliveries, heartbeats and `POST /webhook/test` don't follow redirects: a 3xx is a failed
  attempt, retried, and the log names where it pointed. Behind a sign-in page, every event counted
  as delivered.
- A hub whose `instances.json` doesn't parse moves it aside (`instances.json.corrupt-<time>`), says
  what was lost and starts empty; joined instances come back with their next heartbeat. It refused
  to start, and under systemd restarted forever. The file is synced to disk before it replaces the
  old one.
- The webhook observer runs only when the webhook takes events it makes (`task.*`, `request.*`,
  `message.created`, `tool.completed`, `login.*`, `limit.*`, `agent.error`). A machine joined to a
  hub sends only `instance.*`, yet kept a subscription to the whole conversation and replayed it on
  every resubscribe. The observer now resumes where it stopped reading, and what it keeps of
  returned tool calls is bounded.
- A message sent in the 3 seconds after an answer or an interrupt starts a task on PTY too: the send
  path says that a turn starts, instead of the order of the events.
- A full replay that takes longer than 1.5 seconds keeps the tasks and requests on screen until its
  own arrive. Steered messages showed as tasks of their own for a moment, and decision cards
  disappeared.
- A handoff reply to the agent's last message goes as `POST /message` would: it waits in the queue
  while the agent works (outcome `queued`). It was sent at once, and dropped if a turn was running.
- Reads that worked (`GET`, `HEAD` and `OPTIONS` below 400) log at Debug. A client polling
  `GET /status` every 3 seconds filled the log: 19,961 of 20,000 lines.
- A call record without a model or usage, or whose usage reads zero tokens, is logged as format
  drift, once per log (Claude Code, Codex, Pi). A renamed usage field priced calls at $0 without a
  word.
- The hub counts each spend once. The summary's `spend` gains `scope` (host, agent, directory and
  log root) and `updated_at`, and the hub page adds each scope once, at its latest figure. Instances
  the hub isn't connected to are left out of the totals and show their last figure dimmed. Two
  instances of one agent in one directory counted twice, and stopped ones kept counting.

### Performance

- A cost pass reads only the logs that changed, and only what is new in them; a pass with nothing
  new returns the last result. Past the index's 128 MB bound, every pass read every log again: for
  600 sessions an unchanged pass took 640–950 ms and now takes 6 ms; a call appended to a 111 MB log
  costs 8 ms instead of 41 ms. Logs a pass reads are no longer evicted, so past that bound memory
  grows with the logs.

## v2.0.0

### Breaking changes

- The experimental ACP transport is removed: it approved every permission request and couldn't
  restart its agent or keep its conversation. `--transport acp` and `--experimental-acp` stop
  AgentAPI with an error; run such an agent with `--transport pty`. `acp` is no longer a value of
  `transport` in the API.

### Features

- `agentapi hub serve --allowed-hosts` lists the names browsers open the hub by, besides IP addresses
  and `localhost`, against DNS rebinding; a hub with `--no-token` and without it warns when it starts.

### Fixes

- A page of another site can no longer make AgentAPI or the hub change anything. Browsers send a
  form or a `no-cors` fetch without asking, and CORS only kept the page from reading the answer: any
  site you opened could send an agent without `--api-token` a task or type into its terminal,
  directly or through a hub with `--no-token`. Requests other than GET, HEAD and OPTIONS from a page
  of another origin are refused unless `--allowed-origins` lists it; `*` lists none. Clients other
  than browsers aren't affected.
- The hub's proxy marks every answer `Content-Security-Policy: sandbox`, so a page an instance serves
  doesn't run as the hub's, and refuses changes to an instance's webhook. Adding a URL no longer
  moves an instance the hub already has.
- A value a newer AgentAPI added no longer breaks the chat. An unknown task status reads as running,
  then failed or completed; unknown subagent statuses, permission modes, request statuses and option
  kinds fall back too. The hub opens every instance with its own chat build, so a newer instance
  could crash it, and **Clear cache and reload** replayed the same event and crashed again.
- The chat, the hub page and the terminals reopen their event streams after any failure. A proxy's
  502 closed the hub's stream for good, and a stream that died without closing (a phone changing
  networks) still showed connected while its tab stayed in front; it now reconnects once the
  heartbeats stop. When the hub doesn't answer, its page says "Can't reach the hub" and retries,
  instead of asking for the token.
- A message steered into a running task shows inside that task, marked *steered*, so the chat
  numbers tasks as `GET /tasks`, webhooks and the hub do. It showed as a task of its own, usually
  empty.
- A message sent in the 3 seconds after an interrupt or an answer, while the agent is idle, starts a
  task. `GET /tasks`, webhooks and the hub counted it as steering the task that had just stopped.
- A usage-limit stop counts as "Needs you" in the chat's header, tab title, notifications and status
  strip, and in the hub's frame bar, as it already did in the hub's list.
- The end of a turn sends one `summary_update`, with the agent idle and the task done. The first of
  the two it sent said idle with the task still running, so the hub flickered. A client that follows
  only the summary no longer makes AgentAPI build the whole replay.
- An older AgentAPI keeps using a state database a newer one only added to. It refuses a file only
  when a newer AgentAPI changed it in a way it can't write, and names the version it needs. 1.9.0
  and earlier still refuse any newer schema. On restore, tasks whose message is missing or was
  reused are dropped.
- The webhook observer runs only while a webhook URL is set, and forgets finished tool calls, closed
  requests, and everything on a restart. It kept every tool call's input (up to 4 KiB each) for as
  long as the server ran, webhook or not.
- The hub checks that the AgentAPI at an instance's URL is that instance. One with another ID (after
  a DHCP change, or on a second machine with the same hostname) makes the row unreachable ("another
  AgentAPI answers at this URL"), and the proxy doesn't forward to it. Another run with the same ID
  can't move an instance the hub is following to its own URL.
- An instance the hub can't reach that misses three heartbeats is stopped, and the hub stops
  retrying until its next heartbeat. It stayed unreachable for good, retried every 30 seconds.
  Instance events carry `heartbeat_seconds`; AgentAPI 1.9.0 and earlier don't send it and keep the
  old behaviour. A late summary no longer makes a stopped instance unreachable.
- `agentapi hub join` subscribes to `instance.*` events only: the hub shows what the summaries say,
  and the task, request, login, limit and error events (up to 64 KiB each) only told it the instance
  was there. Run `hub join` again on a machine that joined earlier. A heartbeat alone no longer sends
  browsers an event or rewrites the hub's file.
- Codex and Pi keep a record whose field changed type, instead of dropping it whole; Pi's finished
  messages still go through its stricter session-log parser. A changed `turn/completed` left Codex's
  turn running and the queue stalled. Unknown record types, changed fields and records that don't
  decode are logged once per kind, with the CLI's version where it reports one (Claude Code, Codex).
- An answer that may have reached the agent but wasn't confirmed stays in flight: the request takes
  no other answer until the agent closes it or the turn ends (409, "request is being answered"), so
  a retry can't send a contradicting approval. An unconfirmed message stays in the conversation with
  its turn running, and an interrupt ends a turn the agent never started.

### Performance

- Tasks are worked out from the messages that changed only. An update at the end of a 1,000-task
  session took 1.6 ms under the conversation's lock; it takes 37 µs.

### Internal

- `lib/instanceapi` holds the contract between an AgentAPI and the clients that watch many: the
  summary, the webhook instance and envelope with `Sign` and `Verify`, and the state a hub shows.
  `lib/httpapi` keeps aliases under the old names, so the OpenAPI schema is unchanged. The hub's
  `GET /api/instances` adds each instance's `state`, `pid`, `agentapi_version`, `workspace_url` and
  `transport`, and registry entries add `workspace_url`.
- `agentio.Host` starts and stops the native agents' processes and runs their loops, with
  `agentio.Throttle` and `InterruptDeadline`, the same for Claude Code, Codex and Pi. Codex and Pi
  read their output unbuffered and stop their process when the start fails; a startup error reads
  "<agent> exited during startup: …"; Codex no longer sends SIGTERM while it still reads output.
- The chat's event streams share one connection class (`lib/event-source.ts`), and the chat's replay
  logic moved to `lib/chat-stream.ts`, with tests.

## v1.9.0

### Features

- Claude Code's mark is the Claude logo, the starburst in Claude's orange, as Codex has OpenAI's and
  Pi its own: in the chat's header and tasks, the hub's rows and cards, and the tab icon. It was an
  orange square.

## v1.8.2

### Fixes

- `agentapi server --print-openapi` (which `make gen` runs) no longer reads the user's config file,
  so a machine joined to a hub doesn't report that short run to it, where it stayed unreachable.
- `agentapi hub serve` waits at most 10 seconds for its watchers when it stops, then stops anyway
  and logs the goroutines' stacks. (A hub that seemed not to stop on SIGTERM turned out never to
  have got it: the restart script's check of `/proc/<pid>/cmdline` didn't match. The hub stops at
  once.)

## v1.8.1

### Performance

- AgentAPI and the hub gzip text, JSON, the event streams (flushed event by event) and the chat's
  files for clients that take it. Opening a long conversation (91 tasks) without a cache sent 8.1
  MB of events, 670 KB of costs and 1.3 MB of scripts; gzipped they are 2.4 MB, 84 KB and 396 KB.
- `GET /cost` sends only the calls made since, to a client that sends `from` and the `digest` it
  got: the chat read all 3,451 calls of that session (670 KB) when it opened and every 15 seconds
  while a turn ran, and now reads a few new ones.
- The chat's build files, named by their content, are kept by the browser for good
  (`Cache-Control: immutable`); its pages are checked every time.

## v1.8.0

### Features

- The hub shows what the agents spent, at list prices (API-equivalent; a subscription doesn't charge
  it): a row its agent's last 24 hours, a group header its group's, the top of the list all of
  them, and the overview a **Spend** card with the last 24 hours and 7 days and the agents that
  spent the most. The summary carries it as `spend` (`day_usd`, `week_usd`): what the agent's
  sessions in the working directory cost, worked out from their logs once a minute, for native
  Claude Code, Codex and Pi.

## v1.7.0

### Features

- Mute an agent on the hub until what it waits for changes: **Mute until it changes** in a row's
  menu takes a login (or a request, a usage limit) you won't attend to out of **Need you**, the
  badges, the tab and the notifications. The row stays, grey, with a struck-through bell. Once the
  agent logs in or waits for something else, it counts again; **Unmute** ends it sooner. Mutes are
  kept on the hub (`PATCH /api/instances/{id}` with `{"muted": …}`).

### Documentation

- An agent skill, `skills/agentapi-hub-join`, has Claude Code join its machine's AgentAPIs to a hub:
  it checks the version and that the hub answers, asks for the hub's URL, secret and this machine's
  address, joins (explaining what `join`'s checks of the hub report) and checks where each AgentAPI
  reports, without restarting one or printing the secret. The hub guide shows how to install it.

## v1.6.1

### Fixes

- `agentapi hub join` no longer reports success for a hub that will never get the events. It first
  sends the hub a signed test event and stops, saying why, when the hub isn't reached, a sign-in
  page is in front of it (a Coder port URL), or the secret differs; and when the hub is on another
  machine and `--public-url` names `localhost`. `--no-check` joins a hub that doesn't run yet.

## v1.6.0

### Features

- The chat shows how to add its AgentAPI to a hub: **Add to an AgentAPI hub…** in the status menu
  has the steps with commands to copy (run a hub, join this machine, open the hub), filled in with
  the hub's address you type, and says which hub the AgentAPI already reports to.

## v1.5.0

### Features

- `agentapi hub serve --no-token` runs a hub without a sign-in, for one only a sign-in of its own
  reaches (a Coder workspace app, a private VPN): everyone who reaches it is in. A hub still needs
  `--token` or `--no-token`, and takes only one of them.

## v1.4.1

### Fixes

- A hub no longer shows an instance that is starting, or whose stream drops for a moment, as
  unreachable: it stays connecting (with its last state, if it had one) while the hub tries again
  for 10 s, and is unreachable only after that.

## v1.4.0

### Features

- A new hub page. The sign-in shows the hub's agents in their states. The list gives each state a
  glyph of its own and says what each agent waits for (the approval's command, the question, the
  login) or does, how long ago; search (`/`) and chips filter it. On a desktop an overview counts
  each state and has a card per request with **Answer**, what runs with its running time, and what
  finished unseen. An open agent gets the hub's bar in place of the chat's own, and the browser's
  back (a phone's back gesture) returns from it to the list. Keys (`/`, `j`,
  `k`, `Enter`, `Esc`), optional browser notifications, a tab icon with the count, and a guide with
  commands to copy while no agent has joined.
- The summary names the oldest request the agent waits on (`request`: its ID, kind, title and when
  the agent asked), so a client shows what each agent waits for without opening it.
- Sort the hub's instances into groups of your own: each row's `⋯` menu moves the instance to a
  group, a new one or none. Groups are kept on the hub (`PATCH /api/instances/{id}`), so every
  browser shows the same ones. The list then gets a header per group that folds, counts the
  instances and how many need you, and renames the group.
- The chat leaves more of a phone's screen to the conversation. An agent the hub opens has one bar,
  the hub's, with the chat's search, terminal and Explorer buttons in it, instead of two. On a
  phone the composer is one row: the message, `⋯` (attach, voice, the agent's settings) and send,
  with stop while the agent works; it grows as the message wraps. The agent's mode, model, effort
  and usage take a second row when asked for from `⋯`, when a slash command opens one of their
  menus, or while the usage nears a limit. While a card asks for you, the status line above it no
  longer says so too, and the cards leave out the number keys a phone doesn't have.

### Fixes

- On an iPhone, the top of the chat and of the hub no longer stays cut off after the keyboard
  closes: the window stays put (the pages scroll in their own panes) and goes back to the top once
  nothing is being typed in.
- On an iPhone, the end of the hub's list and the chat's composer no longer sit under a browser's
  bottom toolbar (Chrome's floats over the page): the pages are frames fixed to the visible area
  between the toolbars, not a screen-high box.
- On an iPhone, the composer no longer sits under the keyboard: iOS doesn't shrink the page for it,
  so while it is up the page's frame takes the place and size of what the browser reports visible.
  The rule is the same in every iOS browser (whether it shrinks the visible part, or the window and
  scrolls it) and in a chat the hub opens, where typing no longer lets the window be pulled back to
  the top as it scrolls. While it is up, a card above the composer scrolls in the room left and the
  composer stays whole. The terminals' key bar keeps above the keyboard the same way.
- A stopped or unreachable instance can be forgotten on a phone: **Forget** moved from a
  hover-only button to the row's `⋯` menu.

## v1.3.0

### Features

- Watch the AgentAPIs of many machines, such as Coder workspaces on a VPN of your own, with
  `agentapi hub serve`. AgentAPIs join with `agentapi hub join`; their signed `instance.*` events
  add them, and the hub follows each one's summary. Instances can also be added by URL; the hub
  keeps them across restarts. Its page (`/chat/hub/`, desktop or phone) lists them, those that
  need you first, and opens any of them in the chat through the hub. The hub sends the instance's
  API token, so the browser never holds the instances' tokens. The [hub guide](docs/hub.md) covers
  setup, every state, troubleshooting and the hub's API.
- Set `agentapi server` flags in `~/.agentapi/config.yaml` (`--config`); flags and `AGENTAPI_*`
  variables override it. A setting there outlives a restart that runs the same command line, such
  as a Coder workspace's start script.
- Connect a machine's AgentAPIs to a hub with `agentapi hub join <url> --secret … --public-url …`.
  It writes the hub's webhook into that file and applies it to the running AgentAPIs.
  `--public-url` fills in `{port}` and `{hostname}`. `agentapi hub leave` and `status` remove and
  show the webhook.
- When `PUT /webhook` points the webhook at a new URL, the AgentAPI sends `instance.heartbeat` at
  once, so the receiver lists it without waiting for the next heartbeat.

## v1.2.0

### Features

- The summary marks the latest task `unread` when it ended after a client last showed the
  conversation. `POST /tasks/seen` marks the tasks seen for every client; the chat calls it while
  it shows the conversation, and the state database keeps the seen state.
- Find the AgentAPIs running on a machine in `~/.agentapi/instances` (`--registry-dir`). Each
  AgentAPI keeps an entry there while it serves, with its ID, name, server ID, URL, PID,
  directory, agent and whether it needs a token, but never the token. `--registry=false` turns it
  off. Listing the registry removes entries whose process is gone.
- `agentapi list` shows the registered AgentAPIs with what each summary says (idle, working, what
  it needs from you, an unread task); `--json` prints them as JSON.

## v1.1.0

### Features

- Monitor each AgentAPI with one summary response or stream. `GET /summary` returns its instance,
  status, `attention` (`login`, `request`, `dialog`, `limit`, `exited` or `none`), title, latest
  task, pending requests, queued messages and usage. `/events?only=summary` sends `summary_update`
  on connection and when the summary changes, plus heartbeats.
- Set instance identity with `--instance-id` and the display name with `--name` (default the working
  directory's name). Both appear in summaries and webhook events, whose instance gains `name`.
  The former flag `--webhook-instance-id` still works.

## v1.0.0

### Breaking changes

- Update clients for renamed identifiers. In `GET /status` and `status_change`, `session_id`
  becomes `server_id` (the random ID of an AgentAPI run), and `run_id` becomes `turn` (the number
  of times the agent started working). The new `agent_session_id` identifies the agent's own session.
  `GET /cost` and timeline events also rename `session_id` to `agent_session_id`. Human-reply
  requests use `server_id` and `turn`; webhook instances and `run.status_changed` rename `run_id`
  to `server_id`. Change `.RunID` to `.ServerID` in payload templates; old templates are rejected
  at startup.
- `--state-file` is deprecated: its conversation is imported once into a state database next to it
  (the same name with `.db`, or `--state-db`), which keeps the conversation from then on, and the
  file isn't written anymore. `--load-state=false` skips the import; `--save-state` has no effect.
  SIGUSR1 writes what the state database hasn't written yet.

### Features

- Send webhook events to a receiver, such as a dashboard monitoring several agents. Select events
  with `--webhook-events` by type, group (`task.*`) or all events (`*`):
  `instance.started`, `instance.heartbeat` and `instance.stopped`; `task.started`, and
  `task.completed` or `task.failed` with the prompt, reply, duration, status and API-equivalent
  cost; `request.opened` and `request.closed` for approvals, questions, plans and terminal dialogs;
  `login.*`, `limit.*`, `agent.error`; `message.created` and `tool.completed`. The default is
  `run.status_changed` alone, as before. Every event names its instance (`--webhook-instance-id`,
  `--public-url`, the Coder workspace), `--webhook-secret` signs every delivery with HMAC-SHA256
  (`X-AgentAPI-Signature-256`), and `--webhook-heartbeat` sets the heartbeat. `GET/PUT /webhook`
  read and change the settings (the secret is never returned) and `POST /webhook/test` sends a test
  event. Payload templates get `.Instance` and `.Data`. Retries wait twice as long each time, up to
  30 seconds; a 4xx response other than 408 and 429 isn't tried again.
- Chat: the Explorer's Webhook tab sets up the webhook, also on an AgentAPI started without one,
  and sends a test event.
- Track tasks on every transport. A task groups a message, the agent's turn and any steering
  messages; it ends as `completed`, `failed` or `limited` when the agent is idle with no open
  request or dialog. `GET /tasks` lists tasks, `/events?sync=1` sends `task_update`, and rich
  messages carry `task_id`. The state database saves tasks; the chat shows their status, tool
  calls and cost, and webhooks report them through `task.*` events.
- `/events?sync=1` sends `queue_update` with the whole queue whenever it changes, so the chat sees
  what another client queued without polling. `task_update` and `queue_update`, like
  `session_sync`, go only to clients that opt in with `sync`.
- `--state-db` works with the PTY transport too: the conversation and the queue survive a restart,
  and the agent starts anew below the conversation. A PTY conversation records the initial prompt
  it got and sends it again only if it changed. Webhook settings changed through the API are kept
  in the state database and apply after a restart, unless AgentAPI starts with other webhook flags.
- Chat: an error that stops the page shows the error, with buttons to clear the cached
  conversation and reload, instead of a blank page.
- Docs: a screenshot of the Webhook tab; all screenshots retaken.

### Fixes

- Opening an earlier session while the agent compacted its context returned 409 after deleting the
  conversation, and left the lifecycle at `restarting`; a refused resume now changes nothing.
- Retracting a message now closes event streams so clients reconnect for a full replay. Previously,
  the server streamed the whole state to connected clients; a stream ending midway could lose the
  remaining state permanently.
- Unconfirmed messages are no longer sent again after a restart with `--state-db`. A message sent
  during shutdown may already have reached the agent, so retrying could run it twice.
- A restart (`DELETE /messages`, an MCP change with `?restart=true`, opening a session, a Codex or
  Pi update) no longer holds up every other request for minutes; messages wait in the queue
  meanwhile. An agent that stopped reading its input can no longer hang AgentAPI or its shutdown.
- `POST /upload` no longer races on huma's shared operation (each upload also grew the OpenAPI
  schema) and removes the temporary files of its form.
- Rich messages without content no longer blank the chat, including after reload. They were sent
  as `null`.
- Pi opens a dialog before the prompt that started it returns.
- Earlier sessions: Codex sessions whose prompt starts with `<` (pasted HTML, say) and sessions
  older than the newest 2000 Codex rollouts are listed again and count in the costs. Codex's
  injected context is recognized by its tags, and a prompt of several text blocks keeps all of them.
- Earlier sessions: Claude Code sessions of directories Claude Code names alike (`/w/a_b` and
  `/w/a-b`) are told apart by the directory their log records, and directories with long paths or
  reached through a symlink find their sessions; Pi's `--session-dir`, `PI_CODING_AGENT_SESSION_DIR`
  and settings `sessionDir` are honored.
- A resumed Claude Code session no longer shows empty tool results for interruptions and prompts
  with images, and task notifications and `!` shell commands aren't taken for prompts.
- Costs count the sessions without a prompt, Pi's calls that keep its prompt cache warm, and
  nested Codex subagents.
- Codex's `input_tokens` no longer include the input read from the cache, as for Claude Code and
  Pi, so Codex's token totals in the chat are smaller and the costs unchanged.

### Performance

- Read session, subagent and cost logs incrementally from the last offset. With one line added to
  a 111 MB log, GET /cost took 72 ms instead of 3.5 s. With 2000 Codex rollouts, GET /subagents
  took 10 ms instead of 0.8 s. Concurrent cost requests share the read.
- Load resumed sessions with tens of thousands of rich messages under one lock; 80,000 previously
  took 38 s. The state database now encodes messages outside the emitter's lock.
- Chat: a long session stays responsive while the agent works. Each update rebuilds the transcript
  only from what changed (0.3–0.5 ms instead of 85–99 ms for 200 tasks), and components read only
  the state they show, so the layout, composer, dock and Lineage no longer render again on every
  update.
- Chat: the browser cache keeps each message as its own record and writes only what changed; a
  server's cache is removed after 14 days without use, and only the 10 most recently used servers
  keep one. The cache moves to a new format, so the first visit after the update loads the
  conversation again. A stream the server or a proxy closes is opened again at once, without a
  reconnect notice.
- Log unknown records and undecodable lines once per session log, with the agent CLI version,
  to expose changes in log formats.

### Internal

- Move shared conversation types from the PTY engine to `lib/conversation`, agent log formats to
  `lib/agentlog` (one package per agent), and earlier sessions to `lib/sessions`.
  `lib/jsonlwatcher` now tails PTY session logs. File, terminal, upload, auth and webhook handlers
  use modules with only their required dependencies.

## v0.28.0

### Features

- Native agents: show session and task costs in USD at provider list prices, calculated from model
  calls in Claude Code, Codex and Pi logs, including subagents. These API-equivalent costs aren't
  charged by subscriptions. `GET /sessions` gives each session's cost and the total with hourly
  buckets; `GET /cost` gives the open session's cost and its
  model calls. Built-in prices cover Anthropic's and OpenAI's models, and `--prices <file>` adds
  models or replaces their prices. The chat shows each task's cost beside its time, and in the
  Explorer's Session tab what each session cost and what they all cost today, in the last 7 and 30
  days and in all.
- Docs: a screenshot of a task's cost; the earlier sessions screenshot shows the costs; all
  screenshots retaken.

## v0.27.0

### Features

- Native agents: open one of the agent's earlier sessions in its working directory, or start a copy
  of one. `GET /sessions` lists Claude Code's, Codex's and Pi's sessions from their session logs;
  `POST /sessions/{id}/resume` restarts the agent on one (with `{"fork": true}`, on a copy under a
  new id) and loads its conversation. The chat lists them in the Explorer's Session tab.
- Native agents: a turn a usage limit stops is reported as `limit_stop` in the status, with the
  reset time. Queued messages wait for the reset instead of failing; `POST /limit/continue`
  continues the task now or at the reset, `DELETE /limit` lets the queue go, and `--auto-continue`
  continues every stopped turn once its limit resets. The chat shows it as a card above the
  composer.
- Claude Code reports how long its prompt cache lives (`usage.prompt_cache`). While the agent is
  idle with an expired cache and a large context, the chat suggests compacting before the next
  message, which would otherwise read the whole context again at the full price.
- Chat: a Lineage panel shows the subagents a native Claude Code or Codex ran in the session, with their
  state and time, and what each did step by step, live while it works; the header counts them and
  an Agent or spawn_agent tool call's **Steps** opens its subagent. `GET /subagents` and `GET /subagents/{id}`
  serve them from the subagents' logs.
- The conversation download (and `GET /timeline?subagents=true`) includes the steps of the
  subagents a native Claude Code or Codex ran, read from their own logs, each event tagged with its
  subagent and, for Claude Code, the Agent tool call that started it. Before, only the subagent's
  prompt and final answer were in it.
- The native demo agent stops on a usage limit when a message says "limit", leaves an expired
  prompt cache when it says "cold", and hands a task to a subagent when it says "delegate". With
  `--type claude`, `codex` or `pi` it poses as that agent, so AgentAPI reads that agent's logs for
  its earlier sessions and subagents.
- Docs: screenshots of the usage limit card, the cold cache note, the earlier sessions, Lineage and
  a file found below the working directory, in the READMEs and the chat UI guide; all screenshots
  retaken from v0.27.0.

### Fixes

- Chat: a notification you closed (an agent's warning or error) stays closed after a reload; the
  server sends its recent errors again on every connection, so it came back. The cold prompt cache
  notice stays closed too.
- Chat: a file path the agent names relative to a repository below its working directory (as an
  agent started in a home directory does), or in the wrong case, no longer ends at "File not
  found": the viewer looks for it below the working directory (`GET /files/find`), opens the one
  match with a note, or lists several to choose from.
- Codex: its subagents (multi-agent v2) show in the transcript as spawn_agent and wait_agent tool
  calls, which were left out; the spawn's result runs until the subagent is done.
- Codex: an earlier session that is a fork reads with the conversation it continues, which Codex
  keeps in the forked session's log rather than copying it.
- With the PTY transport, Claude Code's timeline no longer includes the API requests Claude Code
  2.1.302 logs (`api-request-blob`: system reminders and tool lists sent to the model) as if they
  were the conversation.

## v0.26.0

### Features

- Chat: the terminals' key bar adds the keys a phone keyboard lacks: Ctrl+R, Ctrl+A, Ctrl+E,
  Ctrl+U and Ctrl+W, Home, End, Page up and Page down, and / - | ~. Its Ctrl and Alt buttons hold the
  modifier for the next key typed, for any other combination.
- Chat: the Explorer's Session tab downloads the conversation as JSONL, next to Restart; the menu
  behind the status in the header still does too.
- Docs: the chat UI guide has animated GIFs of the main flows, and `scripts/capture-docs.mjs`
  records them with the screenshots.

### Fixes

- Chat: links in the terminal and TTY mode open on a click, also when they run over several lines,
  and the last link a command printed (a login URL) shows above the terminal to open or copy.
- Codex logs in with a device code by default: the login card and `/login` run `codex login
  --device-auth`, which works on servers where a browser elsewhere can't reach the sign-in
  redirect to localhost.
- Chat: thinking stays beside the steps it explains on a wide screen; a note taller than its step
  no longer pushes the notes after it below their steps, or past the end of the task.
- A custom PTY agent that draws no input box (a shell, a REPL) counts as ready once its screen
  settles, instead of keeping every message in the queue.
- With the PTY transport, finding Codex's session log reads each session file's first line once,
  instead of every recent file every 500 ms (0.7% of a core while idle, growing with the sessions).
- Chat: a warning the agent repeats on every start (Codex's note about a config setting it ignores)
  shows once per page instead of stacking up.
- Chat: the Explorer's MCP tab names Pi among the agents it manages, and the Session tab says a
  restart updates the agent first only for Codex and Pi.
- Chat: the Links tab leaves out a link the agent's text cut short with an ellipsis.

## v0.25.0

### Features

- **Breaking:** Claude Code, Codex and Pi use the native transport by default: without
  `--transport`, `agentapi server -- claude` speaks Claude Code's own protocol instead of driving its
  terminal UI. Every other agent keeps the PTY transport, and so does a server started with
  `--state-file`, the PTY transport's persistence. `--transport pty` (or `AGENTAPI_TRANSPORT=pty`)
  keeps the old behavior, and `--state-db` no longer needs `--transport native`.

## v0.24.0

### Features

- Without `--api-token`, a server that listens on an address other machines can reach (the default
  listens on all interfaces) now warns at startup that anyone who can connect can use the agent,
  and with the native transport its shell terminal; the warning suggests `--api-token`, or
  `--bind 127.0.0.1` to accept only local connections.
- Native transport: closing Claude Code or Codex during a turn (stopping AgentAPI, or a restart)
  interrupts the turn first and waits for it to end, for the agent to exit or for the 3-second stop
  timeout before closing its input, as for Pi, so the session that `--state-db` resumes doesn't end
  in an unfinished tool call.
- `agentapi attach` works with a server started with `--api-token`: it sends `--api-token` (or
  `$AGENTAPI_API_TOKEN`) as a Bearer token, and a 401 says to pass one or that it was refused.
  Attaching to a native agent, which has no terminal, is refused up front, as with ACP, and points
  to the chat's terminal.
- Chat: the file viewer previews HTML, Markdown and SVG files on a Preview tab, and shows image
  files as images instead of "Binary file". `GET /files/preview` returns an HTML file with the
  stylesheets, scripts, images, fonts and media it refers to by relative path inlined (only
  non-hidden files inside the working directory, 4 MiB each and 16 MiB in all), and the chat shows
  it in a sandboxed iframe whose scripts can't read the chat or call the API. A Markdown file opens
  on its preview, with relative images loaded through `GET /files/image` and relative links opened
  in the viewer.
- Chat slash commands for native agents, whose terminal UI isn't there to run them: `/model`,
  `/effort` (`/think`) and `/mode` (`/permissions`) switch a setting by ID or name or open its menu,
  `/plan` switches to the plan mode, `/compact` compacts the context, `/clear` (`/new`) starts a new
  conversation, `/terminal` opens the terminal and `/login` runs the agent's login command in it,
  and `/help` lists them. The composer offers them while the command word is typed (Tab completes);
  anything else, such as Claude Code's `/code-review`, goes to the agent as typed. `POST /compact`
  compacts the context while no turn runs: Codex with `thread/compact/start`, Pi with its `compact`
  command (`compact` capability); messages sent meanwhile wait in the queue, and the agent's refusal
  is a 409 with its reason; Claude Code compacts with the `/compact` message.
- A terminal for native agents: AgentAPI runs a shell in the agent's working directory, shared by
  everyone who opens it (like a tmux window) and kept running while nobody watches. `GET /terminal`
  streams its output as server-sent events (the first event replays the recent output),
  `POST /terminal/input` and `/terminal/resize` type into and size it, and `GET /status` reports
  `terminal`. On by default with the native transport (`--terminal=false` turns it off). In the
  chat the terminal button opens it as a real terminal sized to the window, also on a phone with the
  key bar TTY mode has, and the login card's "Run in terminal" runs the login command there.
- Native transport: `GET /status` and `status_change` report the agent's `usage`: how full the
  context window is after the last turn, and the account's rate limits with how much is used and
  when they reset (Claude Code's 5-hour and weekly limits from `rate_limit_event` and its context
  from `get_context_usage`; Codex's context and usage windows; Pi's context). In the chat a ring in
  the composer's toolbar shows the context, and its menu the limits, amber from 80% and red from
  95%.
- Codex gets the plan mode: Codex's plan collaboration mode, an experimental app-server API
  AgentAPI now opts into. Codex ends its turn with a plan and AgentAPI asks "Implement this plan?"
  as the Codex TUI does: the Yes options implement it in the same turn, in the permission mode
  picked (bypass, auto review or ask, the mode before the plan first), No with feedback sends Codex
  back to planning, No alone ends the turn.
- Native transport: switch the agent's permission mode, model and reasoning effort while it runs.
  `GET /settings` lists them with the values they can take, `PUT /settings` changes them, and
  `GET /status` and `status_change` report them (`settings` capability). The modes are bypass (the
  default), auto, ask and plan: Claude Code's `bypassPermissions`, `auto`, `default` and `plan`,
  switched with its control requests (AgentAPI now adds `--allow-dangerously-skip-permissions`,
  without which a switch to bypass is ignored, except as root outside a sandbox); for Codex, no
  approvals or sandbox, the workspace sandbox with auto review, `untrusted`, and its plan mode,
  sent with the next turn; Pi has only its model and thinking level.

  The settings chosen are given again to the agent after a restart, a new login and, with
  `--state-db`, an AgentAPI restart. In the chat, menus in the composer's toolbar switch them.
- Codex on the native transport starts without approvals or sandbox (the bypass mode) unless the
  command line chooses a policy (`-a`, `-s`, the bypass flag, or `-c approval_policy`,
  `sandbox_mode`, `approvals_reviewer`), as Claude Code does.
- Native transport: Restart (Explorer, `DELETE /messages`) runs `codex update` or
  `pi update --self` first, as a PTY restart does, and an MCP change saved with restart restarts a
  native agent on its session, so the conversation stays (409 while a turn runs).
- Chat: file viewer. A file path in the chat opens the file: the file name on a file tool call
  (Claude Code's Read, Write, Edit, MultiEdit and NotebookEdit; Codex's apply_patch, also from a
  code-mode script or the shell, and view_image; Pi's read, write and edit), a path in inline code
  in the agent's reply (with `:line` it opens at that line) and the Explorer's Files tab, which now
  also finds paths in Markdown code.

  The viewer shows the file with line numbers and syntax highlighting (plain text past 200 KB;
  binary, missing and truncated files say so) and its diff: the tool call's own change, then the
  uncommitted changes git sees (or that there are none, or no repository). It is full screen on
  phones. `GET /files/content` and `GET /files/diff` serve them, only for files inside the agent's
  working directory and cut at 1 MiB.

  Expanded file tool calls list their files with File and Diff buttons and show their change as a
  colored diff instead of escaped JSON.
- Native transport: an agent that isn't logged in no longer stops AgentAPI. At startup AgentAPI
  checks the login (Claude Code with `claude auth status --json`, Codex with app-server's
  `account/read`, Pi from its state: no model and no available models); API keys,
  `CLAUDE_CODE_OAUTH_TOKEN`, an `apiKeyHelper`, Bedrock and Vertex count as logged in, and a check
  that fails lets the agent start as before.

  Without a login AgentAPI keeps running, reports `login_required` in `GET /status` and
  `status_change` (the command to run, such as `claude auth login`, `codex login` or `pi` then
  `/login`, with the host and user to run it on and as), queues messages, and starts the agent
  again every 5 seconds, so it notices the login by itself. A turn that fails for want of a login
  (`Not logged in`, HTTP 401, an invalid key, an expired token, Pi's
  `Authentication failed for "<provider>"` after a subscription login expired) reports it too, and a
  turn the model completes clears it (an interrupted turn or a message the agent handled itself
  doesn't); `POST /login/retry` checks again right away (a start under way is that check) and
  restarts an agent that lost its login on its session.

  Warnings the agent prints while it starts are shown once, not on every retry.
- Chat on the native transport: when the agent needs a login, a card docks above the composer with
  the command to run (and a copy button), the machine and user to run it on and as, the agent's own
  help, and Check again. The header, tab title, tab icon and notifications say "Needs you", as for a
  request. `native-demo` simulates it when the message says `login`.
- Claude Code on the native transport runs in `bypassPermissions` unless you pick a permission
  mode, like the chat's PTY sessions on machines whose settings default to it: tools run without
  asking, while questions and plans still arrive as requests, and approving a plan offers "Yes, and
  bypass permissions". Pass `--permission-mode default` to approve tool calls from the chat, or
  switch the mode while Claude Code runs. Claude Code refuses `bypassPermissions` as root outside a
  sandbox, so as root AgentAPI passes `--permission-mode default` instead and logs a warning (set
  `IS_SANDBOX=1` in a sandbox, or choose a mode).
- `--state-db <file>` (native transport): keeps the conversation in a SQLite file (messages, rich
  messages, timeline, requests, queue and the agent's session ID). Started again with the same file,
  AgentAPI shows the conversation as it was, keeps the queue, and resumes the agent's session; the
  epoch is kept, so browser caches stay valid.

  Requests open at shutdown are marked expired, uploads are kept in `<file>.uploads`, and a file
  written for another agent type, transport or directory is refused. The file is created 0600
  (missing directories 0700) and locked while AgentAPI runs, so a second AgentAPI can't use it;
  after a crash, which can lose the last 200 ms of writes, the conversation gets a new epoch so
  clients load it in full; writes that keep failing are reported as an error; and a restart
  (Explorer, `DELETE /messages`) clears the stored conversation and the queue.
- `/events?sync=1` sets SSE IDs (the end of the replay has the replay's `seq`, each later change
  its own) and accepts `Last-Event-ID`, so a plain `EventSource` resumes by itself; `GET /status`
  reports the `epoch` to put in the URL. When a message is taken back (a turn the agent refused to
  start), the conversation gets a new epoch and clients receive a `session_sync` with `full: true`
  and the whole state.
- Native transport for Pi (`--transport native -- pi …`): AgentAPI runs `pi --mode rpc` with its
  own `--session-id` and refuses the options that would change the mode or the session. Text,
  thinking and bash output stream into the rich messages, and the finished messages match the ones
  parsed from Pi's session log, now with the images in tool results.

  Steer, interrupt (without the "This operation was aborted" error Pi adds after an abort during a
  tool), resume by session ID, and image attachments are supported. Dialogs from Pi extensions
  become requests: a `select` or `confirm` while a tool runs is an approval of that tool call, other
  dialogs are questions; dialogs still open when a turn ends expire with it (the extension gets a
  cancel), and an extension command that runs without a model turn ends with the stop reason
  `handled`.

  Messages given as bare arguments, which RPC mode would ignore, are refused like those after `--`,
  and an interrupt Pi doesn't confirm within 10 seconds ends the turn anyway. Stopping AgentAPI
  aborts a running turn before Pi exits, so its session log doesn't end in a tool call without
  result.
- Native transport for Claude Code (`agentapi server --transport native -- claude`): AgentAPI runs
  `claude -p` with stream-json on stdin and stdout and its control protocol. Text and thinking
  stream into the chat as they are written (thinking is shown: AgentAPI adds
  `--thinking adaptive --thinking-display summarized` unless you set thinking flags), and rich
  messages have the same shape as the session-log parser's, with tool status, tool result images,
  model, stop reason and usage.

  Tool approvals (Allow, Allow always for this session, Deny with feedback), `AskUserQuestion` and
  plan approvals come as requests; steering, interrupting, resuming and image input are supported,
  and the session name is the title Claude Code generates after the first prompt. AgentAPI always
  passes a permission mode (see above), because managed or user settings can make
  `bypassPermissions` the default.

  Flags that conflict with the protocol (`-p`, `--output-format`, `--resume`, `--session-id`,
  `--permission-prompt-tool`, `--sdk-url`, `--rc`, ..., also inside a group of short flags such
  as `-pc`) are rejected at startup. After `/clear` the new session is titled and is the one a
  restart resumes, an interrupt Claude Code doesn't confirm within 10 s ends the turn anyway, and
  the latest streamed text arrives within 100 ms even when the stream pauses.
- Codex native transport (`--transport native -- codex`): AgentAPI drives Codex through
  `codex app-server` instead of its terminal UI. Replies, reasoning summaries (every turn asks for
  detailed ones), commands, file edits, MCP tool calls, plan updates and token usage stream into the
  chat in the same shape as the session-log parser's.

  Command, file-edit and MCP tool approvals, permission requests, MCP forms and `request_user_input`
  questions become requests with Codex's own decision IDs, never approved automatically; a command
  approval shows the command that runs, without the shell wrapper, and requests still open when a
  turn ends are declined so Codex doesn't keep waiting. Steer, interrupt, image attachments and
  resuming a thread (by its ID) are supported. `-c`, `--enable` and `--disable` go to app-server;
  `-m`, `-s`, `-a` and `-C` (also as `-mo3` or `-m=o3`) become the thread's settings; prompts,
  subcommands, values on switches and flags app-server can't honor (such as `--profile` or
  `--image`) are rejected at startup.
- Chat on the native transport: the agent's requests dock above the composer as a card, like a
  terminal dialog. Approvals show the command and options styled by what they do (allow, always
  allow, deny), with an optional note sent along with a denial; plans render as Markdown and can be
  approved or sent back with feedback; questions take one step each (single or multiple choice,
  typed or hidden answers) and are submitted together.

  Number keys pick options (not while you type, or while a dialog, a menu or the terminal has the
  focus), and an Enter that confirms an input method's candidate doesn't submit. A request answered
  elsewhere or no longer pending leaves the card (a conflict says so), and a question the agent
  stopped waiting for points out that a normal message can still answer it.

  The header, tab title, tab icon and notifications say "Needs you" while a request is pending, and
  the cached transcript keeps the requests across reloads.
- Chat on the native transport: a tool call with a request shows its state on its row (waiting for
  approval, approved, always allowed, denied, answered). AskUserQuestion, request_user_input and
  ExitPlanMode calls are answered on the request card only, not on the tool call.
- Chat on the native transport: while the agent works, the send button gets a menu with Steer (add
  the message to the turn it is working on, ⌘/Ctrl+Enter) and Interrupt & send (⌘/Ctrl+Shift+Enter);
  Enter still queues. Stop interrupts the turn (`POST /interrupt`). Queued tasks have Steer now, and
  every transport can move queued tasks up and down (`PUT /queue`).
- Chat: TTY mode, the terminal prompt card, the live screen tail and "Terminal output" only show on
  the PTY transport; native and ACP sessions have no terminal.
- `native-demo` (`--transport native --type custom -- native-demo`): a simulated agent for
  developing the chat without a real one. It streams thinking, a tool call and a reply, and asks for
  an approval, questions or a plan when the message says `approve`, `ask` or `plan` (see
  `chat/README.md`).
- Native transport (`--transport native`): drives the agent through its own structured protocol
  instead of its terminal UI. This release adds the framework, API, and adapters for Claude Code,
  Codex, and Pi.

  Messages, rich messages (replaced as a whole as they stream, so text and thinking grow in place)
  and the timeline come from the protocol, so there is no screen to parse. The agent runs in its own
  process group (stopping it closes its input, then sends SIGTERM and SIGKILL), the variables a
  parent Claude Code, Codex or Pi session sets (`CLAUDECODE`, Claude Code's messaging token,
  `CODEX_SANDBOX`, ...) are removed from its environment, and a restart sends `--initial-prompt`
  again. `--transport acp` is the same as `--experimental-acp`.
- Requests: with the native transport, tool approvals, questions (Claude's AskUserQuestion, Codex's
  request_user_input) and plan approvals arrive as structured requests instead of terminal dialogs.
  `GET /requests` lists them, `POST /requests/{id}/respond` answers one (an option ID for approvals
  and plans, answers keyed by question for questions), and `/events` sends a `request_update` event
  whenever one opens or closes. A request is answered at most once (409 otherwise, also for one
  restored from `--state-db`); each says whether the agent is still waiting (`live`), can be
  answered with a new message (`message`, also for a question the agent withdrew) or can no longer
  be answered (`not_resumable`). `GET /status` reports `pending_requests`, and `/handoff` offers
  pending requests as numbered choices (a reply answers exactly the offered request, or says it was
  `superseded`). Answers to secret questions are replaced by `[redacted]` in everything the server
  reports; only the agent gets them.
- `POST /message` takes `dispatch_mode`: `queue` (default, as before), `steer` (add the message to
  the turn the agent is working on) or `interrupt` (stop that turn and send the message next).
  `POST /interrupt` stops the running turn, `POST /queue/{id}/steer` moves a queued message into the
  running turn, and `PUT /queue` reorders the queue. `steer` and `interrupt` need the native
  transport; while no turn runs (also while the agent restarts or waits for a login) they send or
  queue the message as usual, and `POST /interrupt` answers 409. `GET /status` reports what the
  agent supports as `capabilities`, and `status_change` events now also carry `transport`. A
  queued message the agent refuses is tried again up to 5 times; one it didn't answer in time is
  dropped at once, since it may have reached the agent.
- The chat works on a server started with `--api-token`. Open the `#token=<token>` link (an
  auto-generated token is printed with a ready-to-open `/chat/embed#token=...` link) or enter the
  token when the chat asks for it; the page trades it for a session cookie, so the event stream, TTY
  mode and images load too. The token stays in the URL fragment, which never reaches the server, a
  proxy or a log, and is removed from the address bar. The sign-in dialog names the server that asks
  and warns when it isn't the page's own origin, says so when the browser didn't keep the cookie,
  and comes back on Reconnect now after Cancel.
- `POST /auth/session` (with `Authorization: Bearer <token>`) sets an HttpOnly, SameSite=Strict
  session cookie that authenticates later requests, Secure over HTTPS or behind a proxy that sends
  `X-Forwarded-Proto: https`. It holds an HMAC of the token, not the token, so changing the token
  signs every browser out, and its name (`agentapi_session_<hash>`) comes from it, so servers with
  different tokens on one host keep their own sign-ins. `DELETE /auth/session` signs out, and
  `GET /auth/session` reports `auth_required` and `authenticated` without needing credentials.
- `--bind` (`AGENTAPI_BIND`) picks the address the server listens on, e.g. `--bind 127.0.0.1` or
  `--bind ::1` for loopback only. It takes an IP address or hostname without a port; the default
  still listens on all interfaces.

### Removed

- **Breaking:** The Temporal integration: the `agentapi discord` command (the Temporal worker and
  Discord reply bridge), the `/temporal` settings endpoints and the Explorer's Temporal tab.
  `GET /handoff`, `POST /handoff/reply` and the run-status webhooks remain for other integrations.

### Fixes

- `codex update` and `pi update --self` before a restart no longer get AgentAPI's API token or a
  parent agent session's variables.
- CI: the PR preview workflows run only in coder/agentapi. In a fork, every pull request push left
  a preview build queued for a runner only coder/agentapi has, until GitHub dropped it a day later.
- ACP: a message is sent as one prompt, its visible text. Its hidden parts (bracketed paste, Codex's
  composer clearing) no longer become prompts of their own, which gave a Codex-typed agent a turn of
  control characters first. Sending no longer holds the server until the reply: the message shows
  right away, queue edits, the handoff and Stop don't wait for the reply, and a failed prompt is
  reported as an `agent_error` event.
- PTY and ACP agents get the native transport's environment rules: the variables of a parent agent
  session (`CLAUDECODE`, Claude Code's messaging token, Codex's sandbox markers, ...) and AgentAPI's
  `AGENTAPI_API_TOKEN` are removed, so a PTY `claude` started from inside a Claude Code session
  starts, and agents no longer get the parent's secrets.
- Session logs are found where `CODEX_HOME` and `CLAUDE_CONFIG_DIR` put them. With either set, the
  PTY transport never found the session log, so the chat showed no tool calls or thinking.
- Phones: a long request no longer fills the screen. Codex's approval titles were its whole command
  (a heredoc script included); titles of Codex approvals, elicitations and questions are now the
  first line, cut at 120 characters, with the full command in the request's input. In the chat a
  docked card (a request, the login card, a terminal prompt) takes at most 60% of a phone's screen
  (70% on wider ones); its content scrolls while the options, the feedback field and the submit
  buttons stay in view, a cut title shows the command in full in the command block, long option
  labels wrap, and the transcript's floating buttons no longer cover the header.
- Chat: the tab title keeps showing the agent's state and task. Shortly after the page loaded, the
  page's static title replaced it until the state changed again.
- Chat: copy buttons work on pages served over plain HTTP, where the browser has no clipboard API.
- Chat: editing a queued task no longer saves it when Enter confirms an input method's candidate,
  and a message can't be sent while one of its attachments is still uploading.
- Chat: a question with no options (one that only takes typed text) in an AskUserQuestion or
  request_user_input call no longer breaks the page.
- Chat: pressing Enter to confirm an input method's candidate (Chinese, Japanese, Korean) no longer
  sends the half-typed message in browsers that report that Enter (Safari).
- `GET /queue` returns an empty list instead of `null` when nothing is queued.
- API tokens are compared in constant time, so response timing no longer hints at how much of a
  guessed token was right.
- With `--api-token`, CORS preflight requests no longer get a 401 (they never carry credentials),
  and a 401 now carries the CORS headers, so a chat served from another allowed origin can tell it
  needs the token.

## v0.23.0

### Features

- Chat: images read by the agent (for example, through Claude Code's Read or Codex's view_image)
  appear as thumbnails on tool calls, even when the card is collapsed. Click a thumbnail to view the
  full image. Images are preserved from the session log and remain available after the original file
  is deleted.
- Rich messages: image metadata is available in each `tool_result` block through `images`
  (`media_type`, `url`). The server saves the images in its upload directory and serves them
  through `GET /uploads/{checksum}/{name}`, using the same checks as uploaded images. Messages do
  not contain image data.

## v0.22.1

### Fixes

- Chat: remove excessive blank space below a running task's live screen tail. The retained height
  previously came from the full initial screen, potentially hundreds of rows in the 1000-row
  terminal. It now resets when the tail switches to the transcript's last 10 lines.

## v0.22.0

### Features

- Chat: paste an image into the composer to attach it. Pasted screenshots are uploaded as
  `pasted-<date>-<time>.png`; pasted text remains text. Image attachments and images in sent
  messages appear as thumbnails that open at full size. Sent messages show thumbnails in place of
  upload paths.
- `GET /uploads/{checksum}/{name}` serves uploaded images to the chat UI. It accesses only the
  server's upload directory through `os.Root`, so symlinks can't escape it.
  Content checks allow PNG, JPEG, GIF, WebP, and BMP, and reject SVG and HTML. Responses include
  `nosniff` and a sandboxing CSP.

### Fixes

- Claude Code: messages ending with an attachment (`@"<path>"`) are now submitted correctly.
  Previously, the trailing mention opened file suggestions, and Enter selected a suggestion instead
  of sending the message.

## v0.21.1

### Fixes

- Chat: redraw a running task's live screen at most once per second and retain its height from the
  first screen onward. Claude Code's blinking tool bullet cannot be distinguished reliably from
  ordinary text. In measurements of the same task across Claude Code, Codex, and Pi, visible
  updates, including the task clock, dropped from 3.7–6.8 to about 1.6–2 per second.
- Claude Code: its spinner before the timer shows (`· Contemplating…`) and the elapsed time on a
  running tool (`· 2s`, `(3s)`) are left out of messages too.

## v0.21.0

### Features

- TTY mode key bar: the most used keys come first (↑, ↓, Enter, Esc, ←, →), so they fit a phone
  screen without scrolling; Enter is labelled "Enter".

## v0.20.4

No changes a user sees: a lint fix of v0.20.3.

## v0.20.3

### Fixes

- Chat: while a task runs, the live screen tail keeps the tallest height it has had, so the task
  doesn't jump up and down as the agent redraws.
- Chat: reduce live-screen flicker by excluding agent status lines from messages: Codex's
  `◦ Working (19s • esc to interrupt)`, Claude Code's `✻ Fermenting… (6s · ↓ 80 tokens)` and tip,
  and Pi's `⠼ Working` and `Elapsed 2.0s`. Spinner frames and elapsed-time updates no longer count
  as message changes. In a measured Codex task, chat updates dropped from 2.5 to about 0.7 per
  second.

## v0.20.2

### Fixes

- iPhone: tapping into a field (TTY mode, the composer, search, the Explorer) no longer zooms the
  page. On touch screens text fields use at least 16px, the size below which iOS Safari zooms in;
  pinch zoom still works.

## v0.20.1

### Fixes

- TTY mode on iPhone: the key bar sits above Safari's arrows-and-Done bar instead of under it. That
  bar sits on top of the keyboard but isn't counted in the space the keyboard takes.

## v0.20.0

### Features

- TTY mode: a Keyboard button opens and closes the phone's on-screen keyboard, which no longer
  opens automatically on entering TTY mode. The key bar stays above the keyboard on iOS and includes
  ←, →, and Shift+Tab. Terminal input disables autocorrect and auto-capitalization.

## v0.19.0

### Features

- Chat: Codex shows its logo (the OpenAI mark, as on Codex's own pages) in the header, the agent's
  turn and the tab icon, drawn in the text color so it follows dark mode.

### Removed

- `GET /usage` (upstream rate-limit utilization for Claude, Codex and Pi). The chat never used it,
  and its ChatGPT lookup no longer worked.

### Fixes

- Chat: the dark mode button stays in the header on phones instead of moving into the status menu.

## v0.18.0

### Features

- Chat header: show the session name supplied by the agent, including Claude Code's title or
  `/rename`, Codex's thread name, and Pi's `/name`. On phones, the session name replaces the agent
  name beside its logo. `GET /status` and `status_change` expose the name as `session_name`.

### Fixes

- Chat: center the 46rem conversation column and composer. Previously, the text sat about 8rem left
  of center, with a timeline on the left and 17rem reserved for thinking notes on the right. The
  timeline now occupies the left margin, moving inline when space is limited. Thinking notes use the
  right margin on screens at least 84rem wide.
- Chat: the Open Coder workspace button stays in the header on small screens instead of
  disappearing below 640px.

## v0.17.1

### Fixes

- Chat: mark a task Failed only when its final command fails. Previously, a failed command that the
  agent recovered from, such as a missing directory or a search with no matches, marked the whole
  task Failed.
- Codex code mode: render commands issued through `exec` as `$ <command>` with their actual output
  and exit code. Previously, the chat showed the JavaScript wrapper (`text(await
  tools.exec_command({cmd:...}))`) and a "Script completed / Wall time / Output:" header, but
  omitted the output itself. `GET /rich-messages` and `GET /timeline` now carry the command as
  `{"cmd", "script"}` and the output without that header.

## v0.17.0

### Features

- Chat: each user prompt appears in a tinted block labelled "You". Agent replies begin with the
  agent's logo and name (e.g., Pi).

## v0.16.0

### Features

- Add Pi coding agent support (`--type=pi`), with automatic detection for the `pi` command. The
  chat displays replies, tool calls, thinking, and token usage from Pi's session log. Readiness
  detection recognizes when Pi's editor accepts input.
- Pi integration: show Pi's logo in the header and tab icon, display dialogs such as project trust
  as question cards, and manage servers in `~/.pi/agent/mcp.json` through Explorer's MCP tab.
  Restart runs `pi update --self` first. Provider limit reporting through `GET /usage` was added and
  later removed (see v0.19.0).
- TTY mode fills the window: the terminal font grows with the window (11–20px) instead of a fixed
  13px, and the mirror uses the agent's real terminal width. `GET /status` and `status_change`
  report it as `terminal_columns`, so servers started with `--term-width` line up too.

## v0.15.1

### Fixes

- Chat: support Claude Code AskUserQuestion prompts with `multiSelect` in decision cards. Options
  toggle by click or number key, and Continue advances to the next question or review step.
  Previously, options were one-shot buttons and the answer could not be submitted. Exclude the
  question form's tab bar from context and prevent an input line above a dialog from becoming its
  title.
- Chat: a queued task no longer lingers as "queued" for up to 30 s after the server has sent it to
  the agent, which looked like a message that failed to send.

## v0.15.0

### Features

- Chat: background alerts use an amber dot when the agent needs a response, blue while it works,
  and green when a task finishes while the tab is hidden. Optional browser notifications are
  available under status menu → Notify me in the background and appear when the agent needs a
  response or finishes a task.
- Chat: simplify the composer to task input, files, voice, and send. Remove the Terminal tab's
  keystroke pad in favor of TTY mode. On phones, TTY mode provides Esc, Tab, arrows, Enter,
  Ctrl+C/L, and Ctrl+D/Z; Ctrl+D/Z require a second press.
- Chat: a new session shows only the agent's state and how tasks queue; the suggested starter
  prompts are gone.
- Chat redesign ("Transcript"): use a single 48px top bar and a task transcript with a timeline,
  the prompt as the heading, Markdown replies, and one row per tool call (`$ command`, exit code,
  duration, output preview). Show thinking as margin notes on wide screens.
  A status strip above the composer shows current activity. Decision cards replace the option bar
  inside the transcript, and number keys select answers. While the agent waits for a response, the
  header and tab title show "Needs you", and the queue is held. When a session log supplies the
  answer, its screen-parsed copy is placed under "Terminal output". Use the Graphite & Signal
  palette with matching hues in light and dark themes, and Geist Mono for machine output. Explorer
  opens with ⌘K and supports list filtering. Restart moves to the Session tab with a two-step
  confirmation. TTY mode includes an 80-column ruler. Long tasks initially show their latest 40
  steps.
- Chat: cache transcripts in IndexedDB and display them immediately when reopening a conversation,
  then fetch only changes from the server. Apply updates in batches and render older tasks
  progressively while the browser is idle. Previously, reloading caused a render for every replayed
  message. In a measured 13-task, 764-message session, the latest reply appeared in 0.3 s instead of
  1.8 s; with a phone-class CPU, it appeared in 1.2 s instead of 7.2 s.
- `GET /events?sync=1` (opt-in): begin the stream with `session_sync` and include `seq` in every
  `message_update` and `rich_message_update`. Reconnect with `&since=<seq>&epoch=<epoch>` to replay
  only subsequent changes. A different epoch, caused by a server restart or conversation reset,
  triggers a full replay; resets also notify connected clients. Clients without `sync` keep the
  original behavior.
- TTY mode: open the agent's terminal (xterm.js) from the chat header when the chat view does not
  display correctly. Send arrow keys, Ctrl/Alt combinations, multiline bracketed paste, and IME
  input directly to the agent. "Back to chat" restores the conversation view without changing the
  conversation.
- Codex: show readable reasoning summaries in the chat. Start the Codex CLI with
  `-c model_reasoning_summary="auto"` unless the setting is already present on the command line or
  in `$CODEX_HOME/config.toml`. Without summaries, the session log may contain only encrypted
  reasoning. This applies only when launching the `codex` CLI directly, not wrappers using
  `--type codex`.
- Explorer: Links, Files and Index list the newest task first; items keep their task numbers.
- Chat header: the conversation JSONL download lives in the status badge menu ("Download
  conversation JSONL") instead of a separate task-toolbar button, and the status badge is the same
  size as the token badge next to it.
- Codex: restarting the CLI through Explorer, `DELETE /messages`, or an MCP change with restart
  runs `codex update` first. This applies only when launching the `codex` CLI directly. A failed
  update (e.g. offline) still restarts the installed version. Claude Code manages its own
  background updates.

### Removed

- The sub-agents (Agents) panel, together with the `GET /agents` endpoint and the `agents_update`
  SSE event.

### Fixes

- Chat: the Geist webfont never applied (its CSS variable was defined on `<body>` while Tailwind
  sets `font-family` on `<html>`), so the UI used the system font.
- Chat accessibility: the composer's Task/Terminal tabs controlled panels that didn't exist, the
  attachment input had no label, the page had no level-one heading, and light-theme state colors
  were below 4.5:1 contrast.
- Codex: reasoning that is only logged encrypted no longer shows as an "(encrypted)" thinking
  block; reasoning summaries are shown instead.
- Chat: stop displaying option buttons under ordinary answers containing numbered lists or words
  such as "allow", "approve", or "sign in". Report the actual prompt as `terminal_prompt` in
  `GET /status` and `status_change`, only when the input box is absent. The chat displays one
  option bar at the end of the conversation. Report a prompt after it has been visible for one
  second, because Claude Code ignores keys sent immediately after a dialog appears. Clear it as soon
  as it changes or disappears. Extract options from the last numbered list; for unnumbered choices
  such as Claude's folder-trust dialog, move the cursor by the appropriate number of rows.
- Claude Code: restore separate reply blocks after `--continue`, `/resume`, or `/clear`. Claude
  Code can advertise a session ID without writing its JSONL file. Previously, the watcher waited
  indefinitely for that file instead of finding the active session, so the chat fell back to a
  single block of terminal text.
- Claude Code: support rich messages in working directories containing `.`, `_`, or other
  non-alphanumeric characters, such as `~/.cache/app` or `cpt_sft_baseline`. Convert each such
  character to `-` when resolving Claude's project directory; previously, only `/` was converted.
  Session files are also found by ID when the directory name still does not match.
- Codex v0.158+: the full-screen "Update available" dialog shown at startup is skipped with Esc
  (this launch only). Until it was answered every message, including the initial prompt, stayed in
  the queue.

## v0.14.0

### Features

- The chat UI shows the AgentAPI version next to the agent name (header, session details and embed
  status bar). The server reports it as `agentapi_version` in `GET /status` and the `status_change`
  SSE event.
- Releases are cut automatically from Conventional Commits on each push to `main`. Breaking
  changes trigger a major bump, `feat` triggers a minor bump, and other eligible commits trigger a
  patch bump. Commits limited to documentation, maintenance, tests, CI, style, or builds do not
  trigger a release. Releases are tagged `vX.Y.Z`, and the version is stamped into the binaries.

### Fixes

- Codex: the rich-message/timeline watcher no longer switches to a sub-agent's session log
  (sub-agent logs share the cwd and are newer than the main one).
- Codex: detect the composer regardless of how many footer lines follow it (Codex v0.157+), so an
  idle Codex no longer reports `running` forever.
- Claude Code: ignore the footer and background agents panel below the input box when detecting
  stability, so ticking subagent timers no longer keep the status `running`.
- Message sending compares only the region above the input box, so a changing footer is not
  mistaken for the agent accepting the message.
- Queue dispatch only looks for interactive terminal prompts near the bottom of the screen and
  never while the agent's input box is visible, so numbered lists in earlier output no longer block
  the queue.
- Queued messages the agent never submitted are dropped with an error instead of being retyped into
  the input box.
- Codex: clear the composer before sending each message so leftover drafts are not merged into new
  input. After an interrupted turn, Codex can restore queued follow-ups to the composer. Previously,
  the next message was appended to that draft, producing input such as "Then reply XReply with Y".
- Codex: treat chat messages sent during a question-tool dialog as free-text answers ("None of the
  above" plus notes). Previously, the message was typed into the dialog and Enter could select the
  default option. Exclude the dialog, notes field, and preceding user message from composer
  detection. Tool cards show question options and follow the active question.
- Codex: send only the digit when selecting an option from the chat or Discord. Like Claude Code,
  Codex selects on the digit alone; the extra Enter previously answered the next question with its
  default.
- Claude plan mode: deliver chat messages entered during the ExitPlanMode approval dialog as "Tell
  Claude what to change" feedback. Previously, the carriage return could select "Yes, and use auto
  mode" and approve the plan.
- Chat messages sent while any other interactive prompt (e.g. a permission request) is showing are
  queued until it is answered instead of being typed into it and approving the highlighted option.
- Discord free-text replies to the plan approval dialog are delivered as plan feedback.
- Claude Code: do not append Enter to option selections from the chat or Discord, because the digit
  alone selects the option. The extra Enter could submit empty plan feedback, reject the plan, or
  approve the next prompt. Enter-only options no longer send Enter twice.

## v0.13.0

### Features

- Render Claude thinking blocks as collapsible sections in the task timeline.
- Add a per-message Markdown/raw display toggle to replace the global toolbar toggle.

### Fixes

- Merge rich message content blocks on re-emit instead of dropping earlier blocks during Claude
  delta streaming.
- Switch tailed JSONL file at runtime when Claude Code parks a session to a new path.
- Reorder resolver priority so ParkedJobID scan runs before direct file check, preventing stale
  session selection.

## v0.12.2

### Fixes

- Remove the `x\b` workaround for the paste-echo bug fixed in Claude Code 0.2.70.
- Handle partial or malformed tool calls from Claude Code.
- Remove `util.After` calls that leaked goroutines.
- Make phase 1 of `writeStabilize` non-fatal when agents do not echo input.

## v0.12.1

### Fixes

- Prevent terminal echo from being captured as agent messages.
- Update Codex input-box detection.

## v0.12.0

### Features

- Experimental ACP integration.
- Introduce state persistence.

### Fixes

- Fix pr-preview and build-release workflow errors.

### Chore

- Refactor the codebase to improve abstraction.
- Update Go to 1.24.
- Use coder/quartz.

## v0.11.8

### Fix

- Update message box formatting detection for Claude.

## v0.11.7

### Features

- Format Codex messages to omit the `coder_report_task` tool call.

## v0.11.6

### Features

- Bump Next.js to 15.4.10.

## v0.11.5

### Features

- Add tool call logging.
- Improve parsing/detection of tool call messages.

## v0.11.4

### Features

- Temporarily remove `coder_report_task` tool-call logs.

## v0.11.3

### Features

- Format Claude messages to omit the `coder_report_task` tool call.

## v0.11.2

### Features

- Improve handling of the initial prompt.

## v0.11.1

### Features

- Add tooltips for buttons.
- Autofocus message box on user's turn.
- Add message formatting for Amp.
- Update message formatting for the latest Opencode version.

## v0.11.0

### Features

- Support sending the initial prompt through stdin.

## v0.10.2

### Features

- Improve autoscroll UX.

## v0.10.1

### Features

- Show the agent name in the chat UI, excluding embed mode.
- Downgrade the OpenAPI version to v3.0.3.
- Add CLI installation instructions in README.md.

## v0.10.0

### Features

- Add file uploads to AgentAPI.
- Add clickable links.
- Add E2E tests.
- Fix scrolling when resizing.

## v0.9.0

### Features

- Add support for initial prompt via `-I` flag.

## v0.8.0

### Features

- Add support for GitHub Copilot.
- Fix inconsistent OpenAPI generation.

## v0.7.1

### Fixes

- Add headers to prevent proxies from buffering SSE connections.

## v0.7.0

### Features

- Add support for Opencode.
- Add support for agent aliases.
- Add explicit support for Amazon Q.
- Update Next.js.

## v0.6.3

- Fix CI issues.

## v0.6.2

- Fix incorrect version string.

## v0.6.1

### Features

- Handle the animation on the Amp CLI startup screen.

## v0.6.0

### Features

- Add support for Auggie CLI.

## v0.5.0

### Features

- Add support for Cursor CLI.

## v0.4.1

### Fixes

- Set `CGO_ENABLED=0` during builds for compatibility with older Linux versions.

## v0.4.0

### Breaking changes

- When running agentapi behind a reverse proxy, you may need to set `--allowed-hosts`.
  See the [README](./README.md) for configuration.

### New features

- Add support for Sourcegraph Amp.
- Add the `--allowed-hosts` flag to the `server` command.

### Fixes

- Update Codex support for changes in its terminal UI.
