# AgentAPI Chat Interface

The AgentAPI web chat groups conversations by task and displays tool activity, attachments and a
terminal. It is also an example client for the AgentAPI HTTP API. Claude Code generated 95% of the code.

## Development Setup

1. Start the AgentAPI backend on `localhost:3284`. From the repository root, run:

   ```bash
   go run main.go server -- claude
   ```

2. In the `chat/` directory, install dependencies:

   ```bash
   bun install
   ```

3. Start the development server:

   ```bash
   bun run dev
   ```

4. Open <http://localhost:3000/chat/?url=http://localhost:3284> in your browser.

### Developing against the native transport without an agent

`native-demo` simulates an agent for developing request cards, steering, interrupts, and queue
controls without installing or paying for a real agent. Select it with this exact program name and
`--type custom`:

```bash
go run main.go server --type custom -- native-demo
```

Each turn streams thinking, a simulated tool call, and a reply. Include these keywords in a message
to try specific interactions:

- `approve`: a command that needs approval (allow, always allow, or deny with
  feedback)
- `ask`: a question request with a single-choice, a multiple-choice and a
  free-text question; add `secret` for a hidden answer
- `plan`: a Markdown plan to approve or send back with feedback
- `long`: a turn that keeps working for about half a minute, to try steering,
  Stop, Interrupt & send and reordering the queue
- `login`: the agent loses its login, so the login card docks above the
  composer. Check again restarts the demo, whose first start still finds no
  login: the card stays, now saying AgentAPI checks by itself, and the next
  start (about 5 seconds later, or Check again) works
- `limit`: the account reaches its 5-hour limit after the message's requests,
  so the usage limit card docks above the composer and queued tasks wait
- `cold`: the turn leaves a large context whose prompt cache already expired,
  for the note that suggests compacting
- `delegate`: the agent hands a task to a subagent with an Agent tool call

To try earlier sessions, costs and Lineage, run the demo as Claude Code with
`--type claude -- native-demo`, or use the Codex or Pi type. AgentAPI reads that agent's log
directories and shows the sessions, subagents and model calls it finds. The demo writes no session
log, so its own tasks have no cost.

Its settings can change from the composer's menus or slash commands: models Swift, Deep, and Plain
(which has no effort setting) and all four permission modes. In ask mode every turn's first command
needs approval; in the plan mode every turn ends with a plan, whose approval switches back to
bypass. Every turn reports simulated context and rate-limit usage that grows with each turn, and
`/compact` empties the context. The terminal button opens a real shell in the working directory, as
for any native agent.

Steering appends to the reply, and Stop ends the turn as interrupted. The adapter lives in
`lib/native/demo`.

## What the chat keeps in the browser

The chat stores one conversation per server in IndexedDB. Reopening shows the cached transcript
immediately, then fetches changes; saves write only changed messages. Caches expire after 14 days
without use, and only the 10 most recently used servers keep one.

Drafts, failed messages and dismissed notices stay in localStorage. If the chat crashes, the error
screen shows the error and offers to clear that server's cached conversation and reload.

## Interface screenshots

These Chromium captures show the production chat UI built with `make build`, served by a dedicated
AgentAPI server running `native-demo`. The backend delivers real native events and requests;
agent replies and tool results are simulated.

![Native desktop chat with structured tool activity and session controls](../docs/screenshots/chat-desktop.png)

The task groups the prompt, tool activity, and reply. Native session controls appear in the
composer, and wide screens show thinking alongside the transcript.

![Expanded tool call with its command input and full result](../docs/screenshots/tool-call.png)

Expand a tool row to inspect its input and full output. This demo lists a simulated workspace; it
does not run the displayed command.

![Explorer Index tab with task navigation and settings tabs](../docs/screenshots/explorer-index.png)

Open Explorer with ⌘K / Ctrl+K to jump to a task, open a discovered file, or reach MCP and Session
settings.

![Mobile native chat with the prompt, tool output, reply, and session controls](../docs/screenshots/chat-mobile.png)

On phones, thinking collapses into the conversation and the composer stays at the bottom with the
permission mode, model, and context controls.

The [screenshot guide](../docs/screenshots/README.md) includes native question and plan cards, the
file viewer, the model menu, and instructions for regenerating all eight images with
[scripts/capture-docs.mjs](../scripts/capture-docs.mjs).
