# End-to-End Tests

The end-to-end tests run the `agentapi` program against a fake agent.

## Run the tests

From the repository root:

```shell
go test ./e2e
```

## How they work

`native_test.go` runs `agentapi server -- claude`, with a state database and without one, and
`fakeclaude/` first on `PATH` as Claude Code. `fakeclaude` reports a login for
`claude auth status`, answers the control requests AgentAPI sends, and answers each user message
with one turn, `Echo: <message>`, in the stream-json frames a real CLI writes. It also writes each
turn, and the session's title, to the session's log under the test's `HOME`, as Claude Code does.
The tests take a turn, restart the server in place with `SIGUSR2`, and check that a new run
(`server_id`) keeps the conversation, the agent's session and the API token. They need a Unix
system.

`providers_test.go` gives the server a config file with two [model providers](../README.md#model-providers):
a gateway the test serves, which lists two models, and one that never answers. `fakeclaude` keeps
the provider its environment and `apply_flag_settings` name and the model `set_model` picks, and on
a provider its reply says which: `Echo: <message> [via <base URL> on <model>]`. The test checks
that `GET /settings` gives up on the provider that doesn't answer within seconds, and doesn't wait
for it again, then switches to the gateway while Claude Code runs and back to its own provider in
the same session.

## Adding a test

Add a test to `native_test.go`. To test another reply, teach `fakeclaude/main.go` to send it: the
frames it writes must match what the real CLI writes.
