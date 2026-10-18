# Documentation screenshots

These screenshots were captured with Chromium from the production chat UI, built from this
repository with `make build`. They show the native transport UI, including model controls,
structured question and plan cards, and the file viewer.

The binary serves the frontend and API, and runs the built-in `native-demo` adapter. The
real backend delivers SSE events and requests, applies session settings, and reads files. Replies,
command results, model names, context usage, and account limits come from the demo adapter and are
simulated. No paid agent or external integration is used. The file viewer reads this repository's
README.

## Gallery

| Screenshot | What it explains |
| --- | --- |
| [Desktop chat](chat-desktop.png) | A task, structured tool output, thinking notes, and native session controls. |
| [Expanded tool call](tool-call.png) | Command input and the complete simulated result. |
| [Explorer index](explorer-index.png) | Task navigation and tabs for files, links, MCP, and Session. |
| [File viewer](file-viewer.png) | Source with line numbers and File, Preview, and Diff tabs. |
| [Model settings](model-settings.png) | Model selection and reasoning effort. |
| [Native question](native-question.png) | A question card with steps, answer choices, and Needs you status. |
| [Native plan](native-plan.png) | A Markdown plan, approval options, and feedback. |
| [Mobile chat](chat-mobile.png) | The same native conversation on a phone viewport. |

Desktop captures use a 1440 × 1000 viewport; the phone uses 390 × 844. Explorer, tool details, and
the file viewer are captured directly from their rendered elements. All captures use the light theme
and a device scale factor of 1.

## Capture again

Run these commands from the repository root unless otherwise indicated. Use a dedicated demo
server. The script clears its conversation and interrupts simulated requests as it moves between
examples. It verifies that the backend reports native transport, the custom agent type, and the
Native demo session name before changing the conversation.

1. Build the binary, including the production chat assets:

   ```bash
   make build
   ```

   Building requires Go, Bun, and installed chat dependencies. The binary serves
   both the chat UI and API used by the captures.

2. Start a dedicated demo server. Start it from the repository root: the file viewer capture
   opens `README.md` from the server's working directory.

   ```bash
   ./out/agentapi server \
     --bind 127.0.0.1 --port 3311 \
     --pid-file /tmp/agentapi-docs.pid \
     --allowed-hosts localhost,127.0.0.1 \
     --allowed-origins http://127.0.0.1:3311 \
     --transport native --type custom --terminal=false -- native-demo
   ```

3. In another terminal, install the automation package outside the repository:

   ```bash
   npm install --prefix /tmp/agentapi-docs-browser --no-package-lock --no-audit --no-fund playwright-core@1.63.0
   ```

   Version 1.63.0 supports the Playwright APIs the script uses. Its Chromium build,
   153.0.8010.12, is the browser recorded in [capture.json](capture.json).

4. Set your Chromium executable path and run the capture script from the repository root:

   ```bash
   export CHROMIUM_PATH=/absolute/path/to/chromium
   export AGENTAPI_PLAYWRIGHT_MODULE=/tmp/agentapi-docs-browser/node_modules/playwright-core/index.mjs
   node scripts/capture-docs.mjs
   ```

The script opens `http://127.0.0.1:3311/chat/`, writes all eight images and
[capture metadata](capture.json), and closes the browser. The metadata records
the running binary's version, Chromium version, capture time, and viewport sizes.
Stop the demo server separately.

To change the URLs, set `AGENTAPI_SCREENSHOT_UI` and `AGENTAPI_SCREENSHOT_API`;
configure the server's allowed origin to match the frontend. Node.js with
built-in `fetch` and Chromium are required for the capture script. If the
frontend URL points to a development server, its Next.js indicator is hidden
during capture.

Review the regenerated images before updating the [English README](../../README.md),
[Traditional Chinese README](../../README.zh-TW.md), or [chat guide](../../chat/README.md).
