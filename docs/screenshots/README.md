# Documentation screenshots

The documentation images show real Tendyard pages with simulated agent data; the `github-*.png`
views show real work on github.com. They support the [chat UI guide](../chat-ui.md) and [README](../../README.md).

- **Chat and instance monitoring:** `capture-docs.mjs` runs the real server with demo agents.
- **Issue dispatch and settings:** `capture-dispatch.mjs` combines real pages with fixed test data.
- **GitHub results:** the public demo repository supplies the issue, review and merged-PR views.

Chromium captured most views from AgentAPI 5.6.0, built from source with `make build` and serving
the production chat UI and API. `capture-docs.mjs` captures the chat and the hub's list and overview.
`capture-dispatch.mjs` captures Issues, an issue's chat, GitHub App setup, Activity, GitHub and
Providers settings, and the model menu with the hub's providers.

`hub-activity.png`, `dispatch-chat.png` and `dispatch-github-connect.png` were captured later from
a 5.6.1 build. The `github-*.png` images show the public demo repository on github.com; their
capture setup is described below.

[scripts/capture-docs.mjs](../../scripts/capture-docs.mjs) starts its own servers on a small sample
project, `my-app` (a page with a stylesheet, a script and release notes, two commits and
uncommitted changes):

- the built-in `native-demo` agent, for the chat. The server is real: it streams the events, keeps
  the queue, opens and answers the requests, applies the settings, serves the files and diffs, and
  runs the shell terminal. The demo's replies, tool results, models, context and usage limits are
  simulated, so no paid agent runs;
- the same demo posing as Claude Code (`--type claude -- native-demo`), one directory above
  `my-app`, for the earlier sessions, a subagent and a file found below the working directory.
  Tendyard reads Claude Code's logs for it, which the script writes into a temporary home
  directory: three earlier sessions, and the log of the subagent the demo's Agent tool call starts;
- `agentapi hub serve` with nine `native-demo` instances that join it with `agentapi hub join`,
  posing as Claude Code, Codex and Pi, for the hub: one asks for an approval, one asks questions,
  one needs a login, one hit its usage limit, one works, one finished a task, one is idle, one
  stopped and one was killed, so the hub can't reach it. Six are in two groups, and two of those
  are pinned to the top. The one that asks questions can run a binary stamped with an older
  version (`AGENTAPI_OLDER_BINARY`), so that it shows as older than the hub: 5.5.1 beside the
  hub's 5.6.0 in the screenshots. The hub starts on a file that says how long each instance
  worked over the last week, as a hub that watched them would keep it, since a demo doesn't run
  for hours. A second, empty hub shows the guide for the first agents. The instances all run on
  one machine, so the hub's page gets them as if they ran on three, named `dev-1` to `dev-3`: the
  script renames each one's machine in the hub's events before the page reads them. The rows, the
  By machine views, the overview's cards and the switch of a machine's agents show these names.

[scripts/capture-dispatch.mjs](../../scripts/capture-dispatch.mjs) runs a real `agentapi hub
serve` on a scratch home directory: it serves the page, signs the browser in, keeps the favorites
and two model providers, `gateway` (LiteLLM gateway, three models, a key) and `local` (Local vLLM,
one model), priced from an OpenRouter list the script writes as the hub's copy, and reads a GitHub
App the script writes (a key made on the spot, `acme-agents`, installed on the organization `acme`
with selected repositories and on a user, `dave`). The overview's Activity card reads the hub's own
`/api/activity`, from an `activity.json` the script writes before the hub starts: a year of work,
hour by hour, mostly on weekdays, more of it lately, with two weeks off. At the end, the script disconnects
the app so `/github/connect` shows the setup page. Machine, agent and GitHub responses are supplied
as fixed test data: the hub's stream gives it the script's agents and its board of three
machines (`pve-dev` and `mac-mini` online, `gpu-box` offline) and the issues and jobs of
`acme/web`, `acme/api` and `acme/infra`; Playwright answers the calls that would go to GitHub and
refuses any other address off the machine; the page's clock is fixed. For the model menu, an
`agentapi server` joins the hub and runs a small stand-in for Claude Code that the script writes;
for Check connection, a fake OpenAI-compatible endpoint lists six models. For `dispatch-chat.png`,
another `agentapi server` runs `native-demo` posing as Claude Code under the name and instance ID a
launcher gives the job of `acme/web#12`, joins the hub, and gets the review prompt the dispatch
sends, rendered as `lib/dispatch/prompts.go` renders it (the review fenced between `BEGIN-` and
`END-` markers); the list shows the hub's own row for it among the script's, and that page's clock
is the real one, as the conversation's times are.

The `github-*.png` views are of github.com, signed out, at 1280 pixels wide: the issue and pull
request of [k1dav-c/agentapi-demo](https://github.com/k1dav-c/agentapi-demo), a public repository
kept for them. A real hub dispatched the issue to Claude Code on a real machine; the review, the
`/agent` comment, the approval and the merge are a person's. That repository has no dispatch
workflow (a public repository shouldn't run jobs on self-hosted runners), so the pull request's
events reached the hub as the workflow sends them, through `POST /api/dispatch` with the hub's
dispatch token.

<a id="gallery"></a>

## The gallery records each captured view and animation

Desktop views are 1440 × 1000 (or a part of one), phone views 390 × 844, all in the light theme at a
device scale factor of 1.

| Screenshot | What it shows |
| --- | --- |
| [chat-desktop.png](chat-desktop.png) | The window: two finished tasks with prompts, tool calls and thinking notes, and the composer |
| [tool-call.png](tool-call.png) | An expanded tool call with its input and result |
| [task-menu.png](task-menu.png) | A task's ⋯ menu: Preview Markdown, Copy task and output, Export Markdown |
| [task-preview.png](task-preview.png) | The Markdown preview of a task |
| [search.png](search.png) | Searching the transcript, with highlighted and counted matches |
| [session-details.png](session-details.png) | The session details menu opened from the status, with the agent's session ID |
| [keyboard-shortcuts.png](keyboard-shortcuts.png) | The keyboard shortcuts |
| [join-hub.png](join-hub.png) | Add to an AgentAPI hub: the hub's address and the commands to copy |
| [permission-mode.png](permission-mode.png) | The permission mode menu |
| [model-settings.png](model-settings.png) | The model and reasoning effort menu |
| [model-providers.png](model-providers.png) | Claude Code's model menu: its own models, then those of the hub's two providers, LiteLLM gateway and Local vLLM, marked **hub** |
| [context-usage.png](context-usage.png) | The context window and the usage limits |
| [slash-commands.png](slash-commands.png) | The slash commands |
| [attachments.png](attachments.png) | An image attached in the composer |
| [explorer-index.png](explorer-index.png) | The Explorer's Index tab, each task with its state |
| [explorer-links.png](explorer-links.png) | The Explorer's Links tab |
| [explorer-session.png](explorer-session.png) | The Explorer's Session tab asking for a second click to restart |
| [file-viewer.png](file-viewer.png) | The file viewer's File tab on `index.html` |
| [file-preview.png](file-preview.png) | The preview of `index.html` |
| [file-markdown.png](file-markdown.png) | The preview of a Markdown file |
| [file-diff.png](file-diff.png) | The uncommitted changes to `README.md` |
| [terminal.png](terminal.png) | The shell terminal and its key bar |
| [chat-mobile.png](chat-mobile.png) | The chat on a phone |
| [native-approval.png](native-approval.png) | An approval card |
| [native-question.png](native-question.png) | A question card |
| [mobile-question.png](mobile-question.png) | A question card on a phone |
| [native-plan.png](native-plan.png) | A plan card |
| [queue-steer.png](queue-steer.png) | Queued tasks during a long turn, and the send menu |
| [limit-card.png](limit-card.png) | A task stopped on a usage limit (Limit reached), with a task held in the queue |
| [retry-card.png](retry-card.png) | A task the API's gateway failed with a 502, the card counting down to the first of three retries, with a task held in the queue |
| [cold-cache.png](cold-cache.png) | The note that the prompt cache expired, with Compact |
| [explorer-webhook.png](explorer-webhook.png) | The Explorer's Webhook tab, filled in: the receiver URL, event groups, signing secret, instance ID, heartbeat and public URL |
| [explorer-sessions.png](explorer-sessions.png) | The Explorer's Session tab with what the sessions cost and Claude Code's earlier sessions |
| [task-cost.png](task-cost.png) | A task's time rail: its start time, duration and cost |
| [lineage.png](lineage.png) | The Lineage panel on a subagent |
| [file-find.png](file-find.png) | The file viewer on a file found below the working directory |
| [login-card.png](login-card.png) | A login card |
| [hub-desktop.png](hub-desktop.png) | The hub's overview: how many need you, a tile per state, a card per request, what runs and what finished |
| [hub-desktop-dark.png](hub-desktop-dark.png) | The same overview in the dark theme |
| [hub-instance.png](hub-instance.png) | An agent open beside the list, its approval ready to answer, with its machine's agents in the bar |
| [composer-favorites.png](composer-favorites.png) | An idle agent open from the hub, the composer's ☆ menu of favorites over a draft |
| [hub-empty.png](hub-empty.png) | The guide an empty hub shows: the hub's address, the join command and a prompt for an agent on the machine, the secret masked |
| [hub-sign-in.png](hub-sign-in.png) | Signing in to the hub, beside its picture of agents in their states |
| [hub-list.png](hub-list.png) | The hub's list: two pinned agents, then two groups and Ungrouped, with every state's glyph: approval, question, login, usage limit, working, finished, idle, unreachable, stopped; and each agent's Tendyard version, one older than the hub |
| [hub-add.png](hub-add.png) | The Add dialog: joining a machine (the hub's address, updates from the hub, the command and a prompt for an agent, the secret masked), or adding an instance by its URL and token |
| [hub-settings-favorites.png](hub-settings-favorites.png) | Hub settings at its Favorites tab: the favorites, each with its buttons to move, edit and delete it |
| [hub-settings-providers.png](hub-settings-providers.png) | Hub settings at its Providers tab: two providers, each with its monogram, ID and endpoint, its models, whether it has a key, its usage and its health over the last hour; one with its models' prices from OpenRouter open |
| [hub-settings-provider-check.png](hub-settings-provider-check.png) | A new provider's editor after Check connection: reached in about 180 ms with six models, two of them picked to offer |
| [hub-settings-github.png](hub-settings-github.png) | Hub settings at its GitHub tab: the hub's GitHub App, the accounts it is installed on with their repositories, and Choose repositories, App settings on GitHub, Reconnect and Disconnect |
| [hub-recent.png](hub-recent.png) | The hub's list by the agents' latest changes: the pinned agents, then one list without group headers, the most recent first, and each row's group where it has room |
| [hub-machines.png](hub-machines.png) | The hub's list By machine in the groups: the pinned agents, then each group with a card per machine: the machine with its version and spend once, a tab per agent with its state's dot, and the picked agent below, one older than the hub |
| [hub-machines-recent.png](hub-machines-recent.png) | The hub's list By machine in the recent order: the pinned agents, then a card per machine without group headers, the machine of the latest change first, each with the age of that change and the agent that changed last |
| [hub-agent-filter.png](hub-agent-filter.png) | The hub's list filtered by agent and state: Claude Code and Codex, those that need you |
| [hub-phone.png](hub-phone.png) | The hub's list on a phone: the pinned agents, then the groups |
| [hub-phone-menu.png](hub-phone-menu.png) | A row's menu on a phone: Pin to top, its group, a new one, and Forget |
| [hub-muted.png](hub-muted.png) | The list on a phone with an agent's login wait muted |
| [hub-spend.png](hub-spend.png) | The overview's Spend card: the last 24 hours and 7 days, the agents that spent the most with their machines, and each machine |
| [hub-tokens.png](hub-tokens.png) | The overview's Tokens card: the last 24 hours and 7 days, by kind, the agents that used the most, each group and each machine |
| [hub-work.png](hub-work.png) | The overview's Work time card: the last 24 hours and 7 days, the agents that worked the most, each group and each machine |
| [hub-activity.png](hub-activity.png) | The overview's Activity card: each day of the last year shaded by work time, a day's tooltip with its work time, tokens and spend |
| [hub-versions.png](hub-versions.png) | The overview's Versions card: how many agents run each instance version, on which machines, and how to update the older ones |
| [hub-phone-chat.png](hub-phone-chat.png) | An instance open from the hub on a phone, with the way back to the list and its machine's agents |

The GIFs use a 1100 × 720 window, or 390 × 844 for the phone view. A blue dot marks the pointer and
clicks because headless Chromium draws no pointer. Frames store only changed pixels to keep files
small.

| GIF | What it shows |
| --- | --- |
| [demo-task.gif](demo-task.gif) | Sending a task: the agent thinks, runs a tool and replies |
| [demo-approval.gif](demo-approval.gif) | An approval card, answered with Allow |
| [demo-question.gif](demo-question.gif) | Three questions answered step by step |
| [demo-queue.gif](demo-queue.gif) | A task queued and another steered into a long turn, then Stop |
| [demo-settings.gif](demo-settings.gif) | The permission mode, the model and effort, and the context |
| [demo-files.gif](demo-files.gif) | A file, its preview, and a diff |
| [demo-terminal.gif](demo-terminal.gif) | The shell terminal: a command, a link above it, and a held Ctrl |
| [demo-phone.gif](demo-phone.gif) | The terminal on a phone, typed with the key bar |
| [demo-hub.gif](demo-hub.gif) | On a phone: opening the instance that needs you, allowing its command, and back to the list where it no longer does |
| [dispatch-issues.png](dispatch-issues.png) | The hub's Issues page: the stages, the machines, and the issues by section, what needs you first |
| [dispatch-assign.png](dispatch-assign.png) | Assign's menu: each agent's machines that can take the issue, and those that can't, with why |
| [dispatch-repos.png](dispatch-repos.png) | The repository menu: the 20 most recent, or any one, found by its name |
| [dispatch-detail.png](dispatch-detail.png) | An issue's detail beside the board: an agent switch under way, its spend, runs, agent time, elapsed time and tokens, its description, its runs and a comment from GitHub |
| [dispatch-clear.png](dispatch-clear.png) | Clear asks first: the ended jobs of the Done section it forgets |
| [dispatch-review.png](dispatch-review.png) | The detail of an issue in review: a review and a message from the hub in its timeline |
| [dispatch-failed.png](dispatch-failed.png) | A job whose Tendyard exited 6 times in 30 minutes: why, its log, and Retry |
| [dispatch-confirm-stop.png](dispatch-confirm-stop.png) | Stop asks first: the worktree and the branch are deleted |
| [dispatch-confirm-switch.png](dispatch-confirm-switch.png) | Switch asks first: Codex takes over on the same branch |
| [dispatch-new-issue.png](dispatch-new-issue.png) | New issue: open an issue and assign it to an agent |
| [dispatch-list.png](dispatch-list.png) | The list: the agents of issues, each with its issue's stage and pull request; the badge on ◎ |
| [dispatch-phone.png](dispatch-phone.png) | Issues on a phone |
| [dispatch-phone-detail.png](dispatch-phone-detail.png) | An issue's detail on a phone |
| [dispatch-chat.png](dispatch-chat.png) | Open chat on an issue's job: the review that came in from GitHub, fenced as the user's content |
| [dispatch-no-github.png](dispatch-no-github.png) | Issues before GitHub is connected: Connect GitHub, and start an issue by its reference |
| [dispatch-github-connect.png](dispatch-github-connect.png) | `/github/connect`, the hub's page that creates its GitHub App |
| [github-issue.png](github-issue.png) | On github.com: the issue, and the hub's comment that Claude Code started on a machine |
| [github-pr-review.png](github-pr-review.png) | On github.com: a review asking for a change and an `/agent` comment, each answered by the agent with a push and a summary |
| [github-pr-cost.png](github-pr-cost.png) | On github.com: the merge, the cost summary the hub posted, and the branch it deleted |

The login card names the host and user to log in as; the script shows `dev-machine` and `dev` in
their place. The servers get a clean environment (no tokens or workspace links of the shell that
runs the script) and a neutral shell prompt.

<a id="capture-again"></a>

## Rebuild and run both scripts to reproduce the captures

1. Get the binary to show. Either download a release binary, or build one with the production chat
   assets (`make build` needs Go, Bun and the chat's dependencies; it stamps the version from the
   latest tag):

   ```bash
   make build
   ```

2. Install the automation packages outside the repository: Playwright, and gifenc and pngjs to
   write the GIFs:

   ```bash
   npm install --prefix /tmp/agentapi-docs-browser --no-package-lock --no-audit --no-fund \
     playwright-core@1.63.0 gifenc@1.0.3 pngjs@7.0.0
   ```

   Version 1.63.0 supports the Playwright APIs the script uses; its Chromium build is the browser
   recorded in [capture.json](capture.json).

3. Run the script from the repository root. It needs Node.js 18 or later, git and bash, and
   thirteen free ports (3311 to 3323 unless you set `AGENTAPI_SCREENSHOT_PORT` to the first of
   thirteen others):

   ```bash
   export AGENTAPI_PLAYWRIGHT_MODULE=/tmp/agentapi-docs-browser/node_modules/playwright-core/index.mjs
   export AGENTAPI_BINARY=out/agentapi        # the default; or a downloaded release binary
   export AGENTAPI_OLDER_BINARY=/tmp/agentapi-5.5.1  # optional: one hub instance runs it
   export CHROMIUM_PATH=/absolute/path/to/chromium   # optional: Playwright's own Chromium otherwise
   node scripts/capture-docs.mjs
   ```

   For the older binary, build the same source stamped with an earlier version:
   `go build -ldflags "-X github.com/coder/agentapi/internal/version.Version=5.5.1" -o /tmp/agentapi-5.5.1 main.go`.

4. Run the second script for the Issues page and its chat, `/github/connect`, the Activity card,
   the GitHub and Providers settings and the model menu. It needs four free ports (3400 to 3403
   unless you set `AGENTAPI_DISPATCH_PORT` to the first of four others), and takes about two
   minutes:

   ```bash
   node scripts/capture-dispatch.mjs
   ```

The script creates a temporary sample project and home directory, then starts its servers on
127.0.0.1. It captures every view and GIF and writes [capture.json](capture.json) with the binary
version, browser, capture time, viewports and images. It stops the servers and removes temporary
files even if a step fails. The run takes about 25 minutes; a chat error fails it.

The binary's version shows in the chat's header. To show the version a merge will release, build
with it: `make build VERSION=5.6.0`.

Review the new images before updating the [chat UI guide](../chat-ui.md), its
[Traditional Chinese translation](../chat-ui.zh-TW.md) and the READMEs.
