# Changelog

## Unreleased

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
  menus, or while the usage nears a limit. While a card asks for you, the status line above it no longer says so too, and the cards
  leave out the number keys a phone doesn't have.

### Fixes

- On an iPhone, the top of the chat and of the hub no longer stays cut off after the keyboard
  closes: the window stays put (the pages scroll in their own panes) and goes back to the top once
  nothing is being typed in.
- On an iPhone, the end of the hub's list and the chat's composer no longer sit under a browser's
  bottom toolbar (Chrome's floats over the page): the pages are frames fixed to the visible area
  between the toolbars, not a screen-high box.
- On an iPhone, the composer no longer sits under the keyboard: iOS doesn't shrink the page for it,
  so while it is up the page's frame takes the place and size of what the browser reports
  visible, the same rule in every iOS browser (whether it shrinks the visible part, or the window
  and scrolls it), also for a chat the hub opens, where typing in it no longer lets the window be
  pulled back to the top as it scrolls. While it is up, a card above the composer scrolls in the room left and the
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
- Don't resend unconfirmed messages after a restart with `--state-db`. A message sent during
  shutdown may already have reached the agent, so retrying could run it twice.
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
- Load resumed sessions with tens of thousands of rich messages at once; 80,000 previously took
  38 s. The state database now encodes messages outside the emitter's lock.
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
  server sends its recent errors again on every connection, so it came back. So does the cold
  prompt cache notice.
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
- Use device-code login for Codex by default. The login card and `/login` run `codex login
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
  passes a permission mode (see below), because managed or user settings can make
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
  instead of its terminal UI. This release adds the framework, API, and adapters for Claude Code, Codex, and Pi.

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
  terminal. Reset it when switching to the transcript's last 10 lines.

## v0.22.0

### Features

- Chat: paste an image into the composer to attach it. Pasted screenshots are uploaded as
  `pasted-<date>-<time>.png`; pasted text remains text. Image attachments and images in sent
  messages appear as thumbnails that open at full size. Sent messages show thumbnails in place of
  upload paths.
- `GET /uploads/{checksum}/{name}` serves uploaded images to the chat UI. It accesses only the
  server's upload directory through `os.Root`, preventing symlinks from escaping that directory.
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
  agent's logo and name (e.g., Pi), making prompts and replies easier to distinguish.

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
  A status strip above the composer shows current activity. Decision cards replace the option bar inside the transcript, and number keys select answers. While waiting for a response, the header and tab title show "Needs you", and the queue is held. When a session log supplies the answer, its screen-parsed copy is placed under "Terminal output".
  Use the Graphite & Signal palette with matching hues in light and dark themes, and Geist Mono for machine output. Explorer opens with ⌘K and supports list filtering. Restart moves to the Session tab with a two-step confirmation. TTY mode includes an 80-column ruler. Long tasks initially show their latest 40 steps.
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
  option bar at the end of the conversation.
  Report a prompt after it has been visible for one second, because Claude Code ignores keys sent immediately after a dialog appears. Clear it as soon as it changes or disappears. Extract options from the last numbered list; for unnumbered choices such as Claude's folder-trust dialog, move the cursor by the appropriate number of rows.
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
