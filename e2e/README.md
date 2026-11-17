# End-to-End Tests

The end-to-end suite checks the real `agentapi` program against a fake Claude Code, covering
restart continuity and provider switching without a paid agent.

- **Run:** execute `go test ./e2e` from the repository root on a Unix system.
- **Understand:** tests exercise the server process, fake protocol and session logs together.
- **Extend:** add scenarios with frames that match the real CLI.

<a id="run-the-tests"></a>

## Run the end-to-end suite from the repository root

From the repository root:

```shell
go test ./e2e
```

<a id="how-they-work"></a>

## Fake agent protocols test restart and provider behavior

`native_test.go` runs `agentapi server -- claude` with `fakeclaude/` first on `PATH` as Claude Code,
both with a state database and without one. `fakeclaude` reports a login for
`claude auth status`, answers the control requests Tendyard sends, and answers each user message
with one turn, `Echo: <message>`, in the stream-json frames a real CLI writes. It also writes each
turn, and the session's title, to the session's log under the test's `HOME`, as Claude Code does.
The tests take a turn, restart the server in place with `SIGUSR2`, and check that a new run
(`server_id`) keeps the conversation, the agent's session and the API token. They need a Unix
system.

`providers_test.go` gives the server a config file with two [model providers](../docs/providers.md):
a gateway the test serves, which lists two models, and one that never answers. `fakeclaude` tracks
the provider that its environment and `apply_flag_settings` name, and the model that `set_model`
picks; on a provider, its reply says which: `Echo: <message> [via <base URL> on <model>]`. The test checks
that `GET /settings` gives up on the provider that doesn't answer within seconds, and doesn't wait
for it again, then switches to the gateway while Claude Code runs and back to its own provider in
the same session.

<a id="adding-a-test"></a>

## Extend the fake agent only with frames the real CLI emits

Add a test to `native_test.go`. To test another reply, teach `fakeclaude/main.go` to send it: the
frames it writes must match what the real CLI writes.
