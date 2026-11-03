# End-to-End Tests

The end-to-end tests run the `agentapi` program against a fake agent.

## Run the tests

From the repository root:

```shell
go test ./e2e
```

## How they work

`native_test.go` runs `agentapi server -- claude` with a state database and `fakeclaude/` first on
`PATH` as Claude Code. `fakeclaude` reports a login for `claude auth status`, answers the control
requests AgentAPI sends, and answers each user message with one turn, `Echo: <message>`, in the
stream-json frames a real CLI writes. The tests take a turn, restart the server in place with
`SIGUSR2`, and check that a new run (`server_id`) keeps the conversation and the API token. They
need a Unix system.

## Adding a test

Add a test to `native_test.go`. To test another reply, teach `fakeclaude/main.go` to send it: the
frames it writes must match what the real CLI writes.
