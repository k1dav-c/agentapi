# Changelog

## Unreleased

### Features
- Chat slash commands for native agents, whose terminal UI isn't there to run them: `/model`, `/effort` (`/think`) and `/mode` (`/permissions`) switch a setting by id or name or open its menu, `/plan` switches to the plan mode, `/compact` compacts the context, `/clear` (`/new`) starts a new conversation, `/terminal` opens the terminal and `/login` runs the agent's login command in it, and `/help` lists them. The composer offers them while the command word is typed (Tab completes); anything else, such as Claude Code's `/code-review`, goes to the agent as typed. `POST /compact` compacts the context while no turn runs: Codex with `thread/compact/start`, Pi with its `compact` command (`compact` capability); Claude Code compacts with the `/compact` message
- A terminal for native agents: AgentAPI runs a shell in the agent's working directory, shared by everyone who opens it (like a tmux window) and kept running while nobody watches. `GET /terminal` streams its output as server-sent events (the first event replays the recent output), `POST /terminal/input` and `/terminal/resize` type into and size it, and `GET /status` reports `terminal`. On by default with the native transport (`--terminal=false` turns it off). In the chat the terminal button opens it as a real terminal sized to the window, also on a phone with the key bar TTY mode has, and the login card's "Run in terminal" runs the login command there
- Native transport: `GET /status` and `status_change` report the agent's `usage`: how full the context window is after the last turn, and the account's rate limits with how much is used and when they reset (Claude Code's 5-hour and weekly limits from `rate_limit_event` and its context from `get_context_usage`; Codex's context and usage windows; Pi's context). In the chat a ring in the composer's toolbar shows the context, and its menu the limits, amber from 80% and red from 95%
- Codex gets the plan mode: Codex's plan collaboration mode, an experimental app-server API AgentAPI now opts into. Codex ends its turn with a plan and AgentAPI asks "Implement this plan?" as the Codex TUI does: Yes implements it in the same turn in the mode before the plan, No with feedback sends Codex back to planning, No alone ends the turn
- Native transport: switch the agent's permission mode, model and reasoning effort while it runs. `GET /settings` lists them with the values they can take, `PUT /settings` changes them, and `GET /status` and `status_change` report them (`settings` capability). The modes are bypass (the default), auto, ask and plan: Claude Code's `bypassPermissions`, `auto`, `default` and `plan`, switched with its control requests (AgentAPI now adds `--allow-dangerously-skip-permissions`, without which a switch to bypass is ignored, except as root outside a sandbox); for Codex, no approvals or sandbox, the workspace sandbox with auto review, `untrusted`, and its plan mode, sent with the next turn; Pi has only its model and thinking level. The settings chosen are given again to the agent after a restart, a new login and, with `--state-db`, an AgentAPI restart. In the chat, menus in the composer's toolbar switch them
- Codex on the native transport starts without approvals or sandbox (the bypass mode) unless the command line chooses a policy (`-a`, `-s`, the bypass flag, or `-c approval_policy`, `sandbox_mode`, `approvals_reviewer`), as Claude Code does
- Native transport: Restart (Explorer, `DELETE /messages`) runs `codex update` or `pi update --self` first, as a PTY restart does, and an MCP change saved with restart restarts a native agent on its session, so the conversation stays (409 while a turn runs)
- Chat: file viewer. A file path in the chat opens the file: the file name on a file tool call (Claude Code's Read, Write, Edit, MultiEdit and NotebookEdit; Codex's apply_patch, also from a code-mode script or the shell, and view_image; Pi's read, write and edit), a path in inline code in the agent's reply (with `:line` it opens at that line) and the Explorer's Files tab, which now also finds paths in Markdown code. The viewer shows the file with line numbers and syntax highlighting (plain text past 200 KB; binary, missing and truncated files say so) and its diff: the tool call's own change, then the uncommitted changes git sees (or that there are none, or no repository). It is full screen on phones. `GET /files/content` and `GET /files/diff` serve them, only for files inside the agent's working directory and cut at 1 MiB. Expanded file tool calls list their files with File and Diff buttons and show their change as a colored diff instead of escaped JSON
- Native transport: an agent that isn't logged in no longer stops AgentAPI. At startup AgentAPI checks the login (Claude Code with `claude auth status --json`, Codex with app-server's `account/read`, Pi from its state: no model and no available models); API keys, `CLAUDE_CODE_OAUTH_TOKEN`, an `apiKeyHelper`, Bedrock and Vertex count as logged in, and a check that fails lets the agent start as before. Without a login AgentAPI keeps running, reports `login_required` in `GET /status` and `status_change` (the command to run, such as `claude auth login`, `codex login` or `pi` then `/login`, with the host and user to run it on and as), queues messages, and starts the agent again every 5 seconds, so it notices the login by itself. A turn that fails for want of a login (`Not logged in`, HTTP 401, an invalid key, an expired token, Pi's `Authentication failed for "<provider>"` after a subscription login expired) reports it too, and a turn the model completes clears it (an interrupted turn or a message the agent handled itself doesn't); `POST /login/retry` checks again right away (a start under way is that check) and restarts an agent that lost its login on its session. Warnings the agent prints while it starts are shown once, not on every retry
- Chat on the native transport: when the agent needs a login, a card docks above the composer with the command to run (and a copy button), the machine and user to run it on and as, the agent's own help, and Check again. The header, tab title, tab icon and notifications say "Needs you", as for a request. `native-demo` simulates it when the message says `login`
- Claude Code on the native transport runs in `bypassPermissions` unless you pick a permission mode, like the chat's PTY sessions on machines whose settings default to it: tools run without asking, while questions and plans still arrive as requests, and approving a plan offers "Yes, and bypass permissions". Pass `--permission-mode default` to approve tool calls from the chat, or switch the mode while Claude Code runs. Claude Code refuses `bypassPermissions` as root outside a sandbox, so as root AgentAPI passes `--permission-mode default` instead and logs a warning (set `IS_SANDBOX=1` in a sandbox, or choose a mode)
- `--state-db <file>` (native transport): keeps the conversation in a SQLite file (messages, rich messages, timeline, requests, queue and the agent's session ID). Started again with the same file, AgentAPI shows the conversation as it was, keeps the queue, and resumes the agent's session; the epoch is kept, so browser caches stay valid. Requests open at shutdown are marked expired, uploads are kept in `<file>.uploads`, and a file written for another agent type, transport or directory is refused. The file is created 0600 (missing directories 0700) and locked while AgentAPI runs, so a second AgentAPI can't use it; after a crash, which can lose the last 200 ms of writes, the conversation gets a new epoch so clients load it in full; writes that keep failing are reported as an error; and a restart (Explorer, `DELETE /messages`) clears the stored conversation and the queue
- `/events?sync=1` sets SSE ids (the end of the replay has the replay's `seq`, each later change its own) and accepts `Last-Event-ID`, so a plain `EventSource` resumes by itself; `GET /status` reports the `epoch` to put in the URL. When a message is taken back (a turn the agent refused to start), the conversation gets a new epoch and clients receive a `session_sync` with `full: true` and the whole state
- Native transport for Pi (`--transport native -- pi …`): AgentAPI runs `pi --mode rpc` with its own `--session-id` and refuses the options that would change the mode or the session. Text, thinking and bash output stream into the rich messages, and the finished messages match the ones parsed from Pi's session log, now with the images in tool results. Steer, interrupt (without the "This operation was aborted" error Pi adds after an abort during a tool), resume by session ID, and image attachments are supported. Dialogs from Pi extensions become requests: a `select` or `confirm` while a tool runs is an approval of that tool call, other dialogs are questions; dialogs still open when a turn ends expire with it (the extension gets a cancel), and an extension command that runs without a model turn ends with the stop reason `handled`. Messages given as bare arguments, which RPC mode would ignore, are refused like those after `--`, and an interrupt Pi doesn't confirm within 10 seconds ends the turn anyway. Stopping AgentAPI aborts a running turn before Pi exits, so its session log doesn't end in a tool call without result
- Native transport for Claude Code (`agentapi server --transport native -- claude`): AgentAPI runs `claude -p` with stream-json on stdin and stdout and its control protocol. Text and thinking stream into the chat as they are written (thinking is shown: AgentAPI adds `--thinking adaptive --thinking-display summarized` unless you set thinking flags), and rich messages have the same shape as the session-log parser's, with tool status, tool result images, model, stop reason and usage. Tool approvals (Allow, Allow always for this session, Deny with feedback), `AskUserQuestion` and plan approvals come as requests; steering, interrupting, resuming and image input are supported, and the session name is the title Claude Code generates after the first prompt. AgentAPI always passes a permission mode (see below), because managed or user settings can make `bypassPermissions` the default. Flags that conflict with the protocol (`-p`, `--output-format`, `--resume`, `--session-id`, `--permission-prompt-tool`, `--sdk-url`, `--rc`, ..., also inside a group of short flags such as `-pc`) are rejected at startup. After `/clear` the new session is titled and is the one a restart resumes, an interrupt Claude Code doesn't confirm within 10 s ends the turn anyway, and the latest streamed text arrives within 100 ms even when the stream pauses
- Codex native transport (`--transport native -- codex`): AgentAPI drives Codex through `codex app-server` instead of its terminal UI. Replies, reasoning summaries (every turn asks for detailed ones), commands, file edits, MCP tool calls, plan updates and token usage stream into the chat in the same shape as the session-log parser's. Command, file-edit and MCP tool approvals, permission requests, MCP forms and `request_user_input` questions become requests with Codex's own decision IDs, never approved automatically; a command approval shows the command that runs, without the shell wrapper, and requests still open when a turn ends are declined so Codex doesn't keep waiting. Steer, interrupt, image attachments and resuming a thread (by its ID) are supported. `-c`, `--enable` and `--disable` go to app-server; `-m`, `-s`, `-a` and `-C` (also as `-mo3` or `-m=o3`) become the thread's settings; prompts, subcommands, values on switches and flags app-server can't honor (such as `--profile` or `--image`) are rejected at startup
- Chat on the native transport: the agent's requests dock above the composer as a card, like a terminal dialog. Approvals show the command and options styled by what they do (allow, always allow, deny), with an optional note sent along with a denial; plans render as Markdown and can be approved or sent back with feedback; questions take one step each (single or multiple choice, typed or hidden answers) and are submitted together. Number keys pick options (not while you type, or while a dialog, a menu or the terminal has the focus), and an Enter that confirms an input method's candidate doesn't submit. A request answered elsewhere or no longer pending leaves the card (a conflict says so), and a question the agent stopped waiting for points out that a normal message can still answer it. The header, tab title, tab icon and notifications say "Needs you" while a request is pending, and the cached transcript keeps the requests across reloads
- Chat on the native transport: a tool call with a request shows its state on its row (waiting for approval, approved, always allowed, denied, answered). AskUserQuestion, request_user_input and ExitPlanMode calls are answered on the request card only, not on the tool call
- Chat on the native transport: while the agent works, the send button gets a menu with Steer (add the message to the turn it is working on, ⌘/Ctrl+Enter) and Interrupt & send (⌘/Ctrl+Shift+Enter); Enter still queues. Stop interrupts the turn (`POST /interrupt`). Queued tasks have Steer now, and every transport can move queued tasks up and down (`PUT /queue`)
- Chat: TTY mode, the terminal prompt card, the live screen tail and "Terminal output" only show on the PTY transport; native and ACP sessions have no terminal
- `native-demo` (`--transport native --type custom -- native-demo`): a simulated agent for developing the chat without a real one. It streams thinking, a tool call and a reply, and asks for an approval, questions or a plan when the message says `approve`, `ask` or `plan` (see `chat/README.md`)
- Native transport (`--transport native`): drives the agent through its own structured protocol instead of its terminal UI. This release adds the framework and API; the Claude Code, Codex and Pi adapters follow. Messages, rich messages (replaced as a whole as they stream, so text and thinking grow in place) and the timeline come from the protocol, so there is no screen to parse. The agent runs in its own process group (stopping it closes its input, then sends SIGTERM and SIGKILL), the variables a parent Claude Code, Codex or Pi session sets (`CLAUDECODE`, Claude Code's messaging token, `CODEX_SANDBOX`, ...) are removed from its environment, and a restart sends `--initial-prompt` again. `--transport acp` is the same as `--experimental-acp`
- Requests: with the native transport, tool approvals, questions (Claude's AskUserQuestion, Codex's request_user_input) and plan approvals arrive as structured requests instead of terminal dialogs. `GET /requests` lists them, `POST /requests/{id}/respond` answers one (an option ID for approvals and plans, answers keyed by question for questions), and `/events` sends a `request_update` event whenever one opens or closes. A request is answered at most once (409 otherwise, also for one restored from `--state-db`); each says whether the agent is still waiting (`live`), can be answered with a new message (`message`, also for a question the agent withdrew) or can no longer be answered (`not_resumable`). `GET /status` reports `pending_requests`, and `/handoff` offers pending requests as numbered choices (a reply answers exactly the offered request, or says it was `superseded`). Answers to secret questions are replaced by `[redacted]` in everything the server reports; only the agent gets them
- `POST /message` takes `dispatch_mode`: `queue` (default, as before), `steer` (add the message to the turn the agent is working on) or `interrupt` (stop that turn and send the message next). `POST /interrupt` stops the running turn, `POST /queue/{id}/steer` moves a queued message into the running turn, and `PUT /queue` reorders the queue. `steer` and `interrupt` need the native transport; while no turn runs (also while the agent restarts or waits for a login) they send or queue the message as usual, and `POST /interrupt` answers 409. `GET /status` reports what the agent supports as `capabilities`, and `status_change` events now also carry `transport`. A queued message the agent refuses is tried again up to 5 times; one it didn't answer in time is dropped at once, since it may have reached the agent
- The chat works on a server started with `--api-token`. Open the `#token=<token>` link (an auto-generated token is printed with a ready-to-open `/chat/embed#token=...` link) or enter the token when the chat asks for it; the page trades it for a session cookie, so the event stream, TTY mode and images load too. The token stays in the URL fragment, which never reaches the server, a proxy or a log, and is removed from the address bar. The sign-in dialog names the server that asks and warns when it isn't the page's own origin, says so when the browser didn't keep the cookie, and comes back on Reconnect now after Cancel
- `POST /auth/session` (with `Authorization: Bearer <token>`) sets an HttpOnly, SameSite=Strict session cookie that authenticates later requests, Secure over HTTPS or behind a proxy that sends `X-Forwarded-Proto: https`. It holds an HMAC of the token, not the token, so changing the token signs every browser out, and its name (`agentapi_session_<hash>`) comes from it, so servers with different tokens on one host keep their own sign-ins. `DELETE /auth/session` signs out, and `GET /auth/session` reports `auth_required` and `authenticated` without needing credentials
- `--bind` (`AGENTAPI_BIND`) picks the address the server listens on, e.g. `--bind 127.0.0.1` or `--bind ::1` for loopback only. It takes an IP address or hostname without a port; the default still listens on all interfaces
- Chat: an image the agent reads (Claude Code's Read or Codex's view_image on a PNG, a screenshot) shows as a thumbnail on the tool call, also while the card is closed; clicking it opens the image full size. It is the image the agent saw, kept from the session log, so it still shows after the file is gone
- Rich messages: a `tool_result` block lists its images in `images` (`media_type`, `url`). The server saves them to its upload directory and serves them through `GET /uploads/{checksum}/{name}`, with the same checks as uploaded images; messages never carry the image data
- Chat: paste an image into the composer to attach it (a pasted screenshot is uploaded and named `pasted-<date>-<time>.png`; pasted text still pastes as text). Image attachments and the images in sent messages show as thumbnails that open full size, and the message shows the thumbnails instead of the upload paths
- `GET /uploads/{checksum}/{name}` returns an uploaded image so the chat can show it. It reads only the server's own upload directory (through `os.Root`, so symlinks can't lead out), serves only PNG, JPEG, GIF, WebP and BMP as judged by the content (not SVG or HTML), and sends `nosniff` and a sandboxing CSP
- TTY mode key bar: the most used keys come first (↑, ↓, Enter, Esc, ←, →), so they fit a phone screen without scrolling; Enter is labelled "Enter"
- TTY mode works on phones: a Keyboard button in the key bar opens and closes the on-screen keyboard (it no longer pops up when TTY mode opens), the key bar stays above the keyboard on iOS, and the bar has ←, → and Shift+Tab. The terminal input turns off autocorrect and auto-capitalization
- Chat: Codex shows its logo (the OpenAI mark, as on Codex's own pages) in the header, the agent's turn and the tab icon, drawn in the text color so it follows dark mode
- The chat header shows the session name, the one the agent gives it: Claude Code's title (or its `/rename`), Codex's thread name, or Pi's `/name`. On a phone it replaces the agent name next to the agent's mark. `GET /status` and `status_change` report it as `session_name`
- Chat: your messages read as yours. Each prompt sits in a tinted block labelled "You", and the agent's turn starts with its mark and name (e.g. Pi), so the prompt and the reply no longer run together
- Pi coding agent support (`--type=pi`, auto-detected for the `pi` executable). The chat shows Pi's replies, tool calls, thinking and token usage from its session log, and knows when Pi is ready for input from its editor
- Pi in the chat: the header and tab icon show Pi's logo; Pi's dialogs (such as project trust) appear as a question card; the Explorer's MCP tab edits Pi's servers (`~/.pi/agent/mcp.json`); `GET /usage` reports the limits of the provider Pi is logged in to; and Restart runs `pi update --self` first, like `codex update` for Codex
- TTY mode fills the window: the terminal font grows with the window (11–20px) instead of a fixed 13px, and the mirror uses the agent's real terminal width. `GET /status` and `status_change` report it as `terminal_columns`, so servers started with `--term-width` line up too
- Chat: background-tab alerts. The tab icon shows an amber dot while the agent needs you, a blue dot while it works, and a green dot when a task finished while you were away. Optional browser notifications (status menu → Notify me in the background) fire when the agent needs you or finishes while the tab is hidden
- Chat: the composer is just a task box (text, files, voice, send). The Terminal tab's keystroke pad is gone; TTY mode covers typing into the agent and now has a key bar (Esc, Tab, arrows, Enter, Ctrl+C/L, and Ctrl+D/Z with a second press) for phones
- Chat: a new session shows only the agent's state and how tasks queue; the suggested starter prompts are gone
- Chat redesign ("Transcript"): one 48px top bar; tasks read as a transcript with a time rail, the prompt as the heading, agent replies as Markdown prose, tool calls as one-line instrument rows (`$ command`, exit code, duration, output preview) and thinking as margin notes on wide screens. A status strip above the composer shows what the agent is doing; when it asks something, a decision card replaces the in-transcript option bar (number keys answer) and the header, tab title and queue say "Needs you" / held instead of "Ready". The screen-parsed copy of an answer is hidden behind "Terminal output" when the session log has it. Graphite & Signal palette with the same hue in light and dark, Geist Mono for machine output; Explorer opens with ⌘K, lists filter, and Restart moved to a Session tab with a two-step confirm; TTY mode has an 80-column ruler. Long tasks show their latest 40 steps first
- Chat: reopening a long conversation is fast. The transcript is cached in the browser (IndexedDB) and shown immediately; the page then asks the server only for what changed. Incoming updates are applied in batches (a reload used to re-render once per replayed message), and older tasks render progressively while the browser is idle. On a 13-task, 764-message session the latest reply appears after 0.3 s instead of 1.8 s, and on a phone-class CPU after 1.2 s instead of 7.2 s
- `GET /events?sync=1` (opt-in): the stream starts with a `session_sync` event and every `message_update` / `rich_message_update` carries a `seq`. Reconnecting with `&since=<seq>&epoch=<epoch>` replays only what changed since; a different epoch (server restart, conversation reset) replays everything, and a reset notifies connected clients. Clients that don't pass `sync` get the same stream as before
- The chat UI shows the AgentAPI version next to the agent name (header, session details and embed status bar). The server reports it as `agentapi_version` in `GET /status` and the `status_change` SSE event
- Releases are cut automatically on every push to `main`: the version is bumped from Conventional Commits (breaking → major, `feat` → minor, otherwise patch), tagged `vX.Y.Z`, and stamped into the release binaries
- TTY mode: an escape hatch for when the chat view looks wrong. The terminal button in the chat header swaps the conversation for the agent's terminal screen (xterm.js) and sends every key straight to the agent, including arrows, Ctrl/Alt keys, multi-line paste (as one bracketed paste) and IME input. "Back to chat" returns to the chat view; the conversation is unaffected
- Codex: thinking shows in the chat. AgentAPI starts Codex with `-c model_reasoning_summary="auto"` so it logs readable reasoning summaries (otherwise its session log only holds encrypted reasoning), unless `model_reasoning_summary` is already set on the command line or in `$CODEX_HOME/config.toml`. Only applies when the program is the `codex` CLI itself, not a wrapper started with `--type codex`
- Explorer: Links, Files and Index list the newest task first; items keep their task numbers
- Chat header: the conversation JSONL download lives in the status badge menu ("Download conversation JSONL") instead of a separate task-toolbar button, and the status badge is the same size as the token badge next to it
- Codex: restarting the agent (Explorer → Restart, `DELETE /messages`, or an MCP change with restart) runs `codex update` before starting it again (only when the program is the `codex` CLI), so the restarted Codex is the latest release. A failed update (e.g. offline) still restarts the installed version. Claude Code updates itself in the background, so its restart already starts the latest version

### Removed
- The Temporal integration: the `agentapi discord` command (the Temporal worker and Discord reply bridge), the `/temporal` settings endpoints and the Explorer's Temporal tab. `GET /handoff`, `POST /handoff/reply` and the run-status webhooks remain for other integrations
- `GET /usage` (upstream rate-limit utilization for Claude, Codex and Pi). The chat never used it, and its ChatGPT lookup no longer worked
- The sub-agents (Agents) panel, together with the `GET /agents` endpoint and the `agents_update` SSE event

### Fixes
- Phones: a long request no longer fills the screen. Codex's approval titles were its whole command (a heredoc script included); titles of Codex approvals, elicitations and questions are now the first line, cut at 120 characters, with the full command in the request's input. In the chat a docked card (a request, the login card, a terminal prompt) takes at most 60% of a phone's screen (70% on wider ones); its content scrolls while the options, the feedback field and the submit buttons stay in view, a cut title shows the command in full in the command block, long option labels wrap, and the transcript's floating buttons no longer cover the header
- Chat: the tab title keeps showing the agent's state and task. Shortly after the page loaded, the page's static title replaced it until the state changed again
- Chat: copy buttons work on pages served over plain HTTP, where the browser has no clipboard API
- Chat: editing a queued task no longer saves it when Enter confirms an input method's candidate, and a message can't be sent while one of its attachments is still uploading
- Chat: a question with no options (one that only takes typed text) in an AskUserQuestion or request_user_input call no longer breaks the page
- Chat: pressing Enter to confirm an input method's candidate (Chinese, Japanese, Korean) no longer sends the half-typed message in browsers that report that Enter (Safari)
- `GET /queue` returns an empty list instead of `null` when nothing is queued
- API tokens are compared in constant time, so response timing no longer hints at how much of a guessed token was right
- With `--api-token`, CORS preflight requests no longer get a 401 (they never carry credentials), and a 401 now carries the CORS headers, so a chat served from another allowed origin can tell it needs the token
- Claude Code: a message that ends with an attachment (`@"<path>"`) is sent. Claude Code opened its file suggestions for the trailing mention and the Enter picked a suggestion instead, so the message stayed in its input box
- Chat: the live screen of a running task is redrawn at most once a second and never shrinks, from the first screen on. Claude Code blinks the bullet of a running tool, which the server can't tell from ordinary text. Measured on the same task, the running task now changes on screen about 1.6–2 times a second (its clock included) instead of 3.7–6.8 for Claude Code, Codex and Pi
- Claude Code: its spinner before the timer shows (`· Contemplating…`) and the elapsed time on a running tool (`· 2s`, `(3s)`) are left out of messages too
- Chat: while a task runs, the live screen tail keeps the tallest height it has had, so the task doesn't jump up and down as the agent redraws
- Chat: a running task no longer leaves thousands of pixels of blank space below its live screen tail. The held height started from the whole first screen (hundreds of rows of the 1000-row terminal) and was kept after the view switched to the transcript's last 10 lines; it now starts over when the view switches
- The live screen no longer flickers while an agent works. Its own status line (Codex's `◦ Working (19s • esc to interrupt)`, Claude Code's `✻ Fermenting… (6s · ↓ 80 tokens)` and tip, Pi's `⠼ Working` and `Elapsed 2.0s`) is left out of messages, so a spinner frame or a ticking second no longer counts as a change: during a Codex task the chat gets about 0.7 updates a second instead of 2.5
- iPhone: tapping into a field (TTY mode, the composer, search, the Explorer) no longer zooms the page. On touch screens text fields use at least 16px, the size below which iOS Safari zooms in; pinch zoom still works
- TTY mode on iPhone: the key bar sits above Safari's arrows-and-Done bar instead of under it. That bar sits on top of the keyboard but isn't counted in the space the keyboard takes
- Chat: the dark mode button stays in the header on phones instead of moving into the status menu
- Chat: the conversation is centered. The text column sat about 8rem left of center, past a time rail and with 17rem kept free on the right for thinking notes; now the 46rem column and the composer are centered, the time rail hangs in the left margin (shown inline when there is no room) and thinking notes use the right margin from 84rem up
- Chat: the Open Coder workspace button stays in the header on small screens instead of disappearing below 640px
- Chat: a task is marked Failed only when it ends on a failed command. A command the agent went on from (a missing directory, a grep with no match) used to mark the whole task Failed
- Codex code mode: commands run through Codex's `exec` tool show as `$ <command>` with their real output and exit code. They showed as the JavaScript snippet Codex runs (`text(await tools.exec_command({cmd:...}))`) with only a "Script completed / Wall time / Output:" header, the output itself cut off. `GET /rich-messages` and `GET /timeline` carry the command as `{"cmd", "script"}` and the output without the header
- Chat: multi-select questions from Claude Code (AskUserQuestion with `multiSelect`) can be answered in the decision card. Options show as checkboxes that toggle on click or with their number key, and Continue moves on to the next question or the review step. Previously each option was a one-shot button, so the answer could not be submitted from the chat. The question form's tab bar no longer shows as context, and an input line left on screen above a dialog no longer becomes its title
- Chat: a queued task no longer lingers as "queued" for up to 30 s after the server has sent it to the agent, which looked like a message that failed to send
- Chat: the Geist webfont never applied (its CSS variable was defined on <body> while Tailwind sets font-family on <html>), so the UI used the system font
- Chat accessibility: the composer's Task/Terminal tabs controlled panels that didn't exist, the attachment input had no label, the page had no level-one heading, and light-theme state colors were below 4.5:1 contrast
- Codex: reasoning that is only logged encrypted no longer shows as an "(encrypted)" thinking block; reasoning summaries are shown instead
- Chat: option buttons ("Select an option or switch to Terminal tab…") no longer appear under answers that merely contain a numbered list or words like "allow", "approve" or "sign in". The server now reports the prompt the agent is actually showing as `terminal_prompt` in `GET /status` and `status_change` (only when its input box is gone), and the chat shows a single option bar from it at the end of the conversation. A prompt is reported once it has been on screen for a second (Claude Code ignores keys sent the moment a dialog is drawn) and cleared as soon as it changes or goes away. Options come from the last numbered list in the prompt, and unnumbered options (Claude's folder trust dialog) move the cursor by the right number of rows
- Claude Code: agent replies are split into separate blocks again after a resumed session (`--continue`, `/resume`) or `/clear`. Claude Code can advertise a session id whose JSONL file is never written; the watcher waited for that file forever and never picked up the session actually in use, so the chat UI fell back to one undivided block of screen text
- Claude Code: rich messages work in working directories containing `.`, `_` or other non-alphanumeric characters (e.g. `~/.cache/app`, `cpt_sft_baseline`). Claude Code turns every such character into `-` in its project directory name; only `/` was converted. Session files are also found by id if the directory name still doesn't match
- Codex v0.158+: the full-screen "Update available" dialog shown at startup is skipped with Esc (this launch only). Until it was answered every message, including the initial prompt, stayed in the queue
- Codex: the rich-message/timeline watcher no longer switches to a sub-agent's session log (sub-agent logs share the cwd and are newer than the main one)
- Codex: detect the composer regardless of how many footer lines follow it (Codex v0.157+), so an idle Codex no longer reports `running` forever
- Claude Code: ignore the footer and background agents panel below the input box when detecting stability, so ticking subagent timers no longer keep the status `running`
- Message sending compares only the region above the input box, so a changing footer is not mistaken for the agent accepting the message
- Queue dispatch only looks for interactive terminal prompts near the bottom of the screen and never while the agent's input box is visible, so numbered lists in earlier output no longer block the queue
- Queued messages the agent never submitted are dropped with an error instead of being retyped into the input box
- Codex: messages are no longer merged with a leftover composer draft. When a turn with queued follow-up inputs is interrupted, Codex puts them back into the composer, and the next message sent through AgentAPI was appended to that text (Codex received "Then reply XReply with Y"). The composer is now cleared before each message
- Codex follow-up questions (question tool): a chat message sent while a question is showing becomes that question's free-text answer ("None of the above" + notes) instead of being typed into the dialog, where Enter would pick the default option. The question dialog (including its notes field and a user message just above it) is no longer mistaken for the composer. The tool card renders Codex's questions with option buttons and follows the question Codex is currently asking
- Option buttons and Discord option replies send only the digit for Codex too, which, like Claude Code, selects on the digit; the extra Enter answered the next question with its default
- Claude plan mode: a chat message sent while the ExitPlanMode approval dialog is showing is delivered as "Tell Claude what to change" feedback instead of approving the plan (the carriage return used to pick "Yes, and use auto mode")
- Chat messages sent while any other interactive prompt (e.g. a permission request) is showing are queued until it is answered instead of being typed into it and approving the highlighted option
- Discord free-text replies to the plan approval dialog are delivered as plan feedback
- Option buttons (chat UI) and Discord option replies no longer append Enter for Claude Code, which selects on the digit alone; the extra Enter submitted empty plan feedback (rejecting the plan) or approved the next prompt. Enter-only options no longer send Enter twice

## v0.13.0

### Features
- Render Claude thinking blocks as collapsible sections in the task timeline
- Per-message markdown/raw render toggle (replaces global toolbar toggle)

### Fixes
- Merge rich message content blocks on re-emit instead of dropping earlier blocks during Claude delta streaming
- Switch tailed JSONL file at runtime when Claude Code parks a session to a new path
- Reorder resolver priority so ParkedJobID scan runs before direct file check, preventing stale session selection

## v0.12.2

### Fixes
- drop x\b workaround for fixed Claude Code 0.2.70 paste-echo bug
- handle partial/malformed tool call detection for claude-code
- exorcise goroutine-leaking util.After from the codebase
- make writeStabilize Phase 1 non-fatal when agents don't echo input

## v0.12.1

### Fixes
- Prevent terminal echo from being captured as agent messages
- Update codex message box detection

## v0.12.0

### Features
- Experimental ACP integration
- Introduce state persistence

### Fixes
- Fix pr-preview and build-release workflow errors

### Chore
- Codebase refactor for abstraction
- Go version bump to 1.24
- Use coder/quartz

## v0.11.8

### Fix
- Update message box formatting detection for Claude

## v0.11.7

### Features
- format codex messages to skip the coder_report_task tool call

## v0.11.6

### Features
- Bump Next.js to 15.4.10

## v0.11.5

### Features
- Add tool call logging.
- Improve parsing/detection of tool call messages.

## v0.11.4

### Features
- Temporarily remove coder report_task tool-call logs

## v0.11.3

### Features
- format claude messages to skip the coder_report_task tool call

## v0.11.2

### Features
- Improved handling of initial prompt

## v0.11.1

### Features
- Add tooltips for buttons
- Autofocus message box on user's turn
- Add msgfmt logic for amp module
- Update msgfmt for latest version in opencode

## v0.11.0

### Features
- Support sending initial prompt via stdin

## v0.10.2

### Features
- Improve autoscroll UX

## v0.10.1

### Features
- Visual indicator for agent name in the UI (not in embed)
- Downgrade openapi version to v3.0.3
- Add CLI installation instructions in README.md

## v0.10.0

### Features
- Feature to upload files to agentapi
- Introduced clickable links
- Added e2e tests
- Fixed the resizing scroll issue

## v0.9.0

### Features
- Add support for initial prompt via `-I` flag

## v0.8.0

### Features
- Add Support for GitHub Copilot
- Fix inconsistent openapi generation

## v0.7.1

### Fixes

- Adds headers to prevent proxies buffering SSE connections

## v0.7.0

### Features
- Add Support for Opencode.
- Add support for Agent aliases
- Explicitly support AmazonQ
- Bump NEXT.JS version

## v0.6.3

- CI fixes.

## v0.6.2

- Fix incorrect version string.

## v0.6.1

### Features
- Handle animation on Amp cli start screen.

## v0.6.0

### Features

- Adds support for Auggie CLI.

## v0.5.0

### Features

- Adds support for Cursor CLI.

## v0.4.1

### Fixes

- Sets `CGO_ENABLED=0` in build process to improve compatibility with older Linux versions.

## v0.4.0

### Breaking changes

- If you're running agentapi behind a reverse proxy, you'll now likely need to set the `--allowed-hosts` flag. See the [README](./README.md) for more details.

### New features

- Sourcegraph Amp support
- Added a new `--allowed-hosts` flag to the `server` command.

### Fixes

- Updated Codex support after its TUI has been updated in a recent version.
