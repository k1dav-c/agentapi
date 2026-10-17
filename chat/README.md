# AgentAPI Chat Interface

A simple ChatGPT-like interface for AgentAPI. It's a demo showcasing how to use AgentAPI. 95% of the code was generated with Claude Code.

## Development Setup

1. Ensure the AgentAPI backend server is running on `localhost:3284`. You can run it from the root of the repository with e.g.

   ```bash
   go run main.go server -- claude
   ```

2. Install dependencies:

   ```bash
   bun install
   ```

3. Start the development server:

   ```bash
   bun run dev
   ```

4. Open <http://localhost:3000/chat/?url=http://localhost:3284> in your browser

### Developing against the native transport without an agent

`native-demo` is a simulated agent for working on the chat UI (request cards,
steering, interrupting, the queue) without installing or paying for a real
one. It only exists as this program name with `--type custom`:

```bash
go run main.go server --transport native --type custom -- native-demo
```

Every turn streams a thinking block, runs a tool call and streams a reply.
Words in your message make it ask for things:

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

Its settings can change from the composer's menus or slash commands: models
Swift, Deep and Plain (without efforts) and all four permission modes. In the
ask mode every turn's first command needs approval; in the plan mode every
turn ends with a plan, whose approval switches back to bypass. Every turn
reports a growing simulated context and rate limit usage, and `/compact`
empties the context. The terminal button opens a real shell in the working
directory, as for any native agent.

Steering appends to the reply, and Stop ends the turn as interrupted. The
adapter lives in `lib/native/demo`.
