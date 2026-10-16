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

Steering appends to the reply, and Stop ends the turn as interrupted. The
adapter lives in `lib/native/demo`.
