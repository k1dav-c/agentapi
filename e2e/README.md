# End-to-End Testing Framework

AgentAPI's end-to-end (E2E) tests send scripted conversations through the server. A fake agent reads
the JSON scripts and checks incoming messages before returning predefined responses.

## Run the tests

From the repository root:

```shell
go test ./e2e
```

## How it works

For each test, `echo_test.go`:

1. Selects a JSON conversation script from `testdata/`.
2. Starts AgentAPI with the fake agent (`echo.go`), which reads that script.
3. Sends messages through AgentAPI.
4. Lets the fake agent check the messages and send predefined responses.
5. Checks the responses against the expected results.

## Adding a new test

1. Create a new JSON file in `testdata/` with a unique name.
2. Define the conversation. Each message has these fields:
   - `expectMessage`: The message from the user that the fake agent expects.
   - `thinkDurationMS` (optional): The simulated thinking time in milliseconds before the agent
     responds.
   - `responseMessage`: The response the fake agent sends.
3. Add a test case in `echo_test.go` that references the new JSON file.
   By default, the framework loads `testdata/<test-case-name>.json`; the test
   case name must match the filename without the `.json` extension. Tests that
   set `scriptFilePath` explicitly can use another filename.
4. Run the E2E tests to verify the new test case.
