# Documentation screenshots

The [chat UI guide](../chat-ui.md) and [README](../../README.md) use these screenshots and GIFs.
Chromium captured them from an AgentAPI binary built from the source (`go build`, version `0.0.0-dev`)
after v1.2.0, serving the production chat UI and API.

[scripts/capture-docs.mjs](../../scripts/capture-docs.mjs) starts its own servers on a small sample
project, `my-app` (a page with a stylesheet, a script and release notes, two commits and
uncommitted changes):

- the built-in `native-demo` agent with the native transport, for everything but TTY mode. The
  server is real: it streams the events, keeps the queue, opens and answers the requests, applies
  the settings, serves the files and diffs, and runs the shell terminal. The demo's replies, tool
  results, models, context and usage limits are simulated, so no paid agent runs;
- the same demo posing as Claude Code (`--type claude -- native-demo`), one directory above
  `my-app`, for the earlier sessions, a subagent and a file found below the working directory.
  AgentAPI reads Claude Code's logs for it, which the script writes into a temporary home
  directory: three earlier sessions, and the log of the subagent the demo's Agent tool call starts;
- bash as a PTY agent (`--term-width 100`), for TTY mode;
- `agentapi hub serve` with six `native-demo` instances that join it with `agentapi hub join`, for
  the hub: one waits for an approval, one works, one finished a task, one is idle, one stopped
  and one was killed, so the hub can't reach it. The rows name the machine; the screenshots show
  placeholders (`dev-1` and so on).

## Gallery

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
| [permission-mode.png](permission-mode.png) | The permission mode menu |
| [model-settings.png](model-settings.png) | The model and reasoning effort menu |
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
| [cold-cache.png](cold-cache.png) | The note that the prompt cache expired, with Compact |
| [explorer-webhook.png](explorer-webhook.png) | The Explorer's Webhook tab, filled in: the receiver URL, event groups, signing secret, instance ID, heartbeat and public URL |
| [explorer-sessions.png](explorer-sessions.png) | The Explorer's Session tab with what the sessions cost and Claude Code's earlier sessions |
| [task-cost.png](task-cost.png) | A task's time rail: its start time, duration and cost |
| [lineage.png](lineage.png) | The Lineage panel on a subagent |
| [file-find.png](file-find.png) | The file viewer on a file found below the working directory |
| [login-card.png](login-card.png) | A login card |
| [tty-mode.png](tty-mode.png) | TTY mode on a PTY agent |
| [hub-desktop.png](hub-desktop.png) | The hub: its instances, what needs you first, and one open in the chat with its approval |
| [hub-sign-in.png](hub-sign-in.png) | Signing in to the hub |
| [hub-list.png](hub-list.png) | The hub's list with every state: needs your answer, working, finished, idle, unreachable, stopped |
| [hub-add.png](hub-add.png) | Adding an instance by its URL and token |
| [hub-phone.png](hub-phone.png) | The hub's list on a phone |
| [hub-phone-chat.png](hub-phone-chat.png) | An instance open from the hub on a phone, with the way back to the list |

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

The login card names the host and user to log in as; the script shows `dev-machine` and `dev` in
their place. The servers get a clean environment (no tokens or workspace links of the shell that
runs the script) and a neutral shell prompt.

## Capture again

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

3. Run the script from the repository root. It needs Node.js 18 or later, git and bash, and three
   free ports (3311 to 3313 unless you set `AGENTAPI_SCREENSHOT_PORT` to the first of three others):

   ```bash
   export AGENTAPI_PLAYWRIGHT_MODULE=/tmp/agentapi-docs-browser/node_modules/playwright-core/index.mjs
   export AGENTAPI_BINARY=out/agentapi        # the default; or a downloaded release binary
   export CHROMIUM_PATH=/absolute/path/to/chromium   # optional: Playwright's own Chromium otherwise
   node scripts/capture-docs.mjs
   ```

The script creates a temporary sample project and home directory, then starts three servers on
127.0.0.1. It captures every view and GIF and writes [capture.json](capture.json) with the binary
version, browser, capture time, viewports and images. It stops the servers and removes temporary
files even if a step fails. The run takes about five minutes; a chat error fails it.

The binary's version shows in the chat's header. To show the version a merge will release, build
with it: `make build VERSION=0.27.0`.

Review the new images before updating the [chat UI guide](../chat-ui.md), its
[Traditional Chinese translation](../chat-ui.zh-TW.md) and the READMEs.
