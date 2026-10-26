---
name: agentapi-hub-join
description: Join this machine's AgentAPIs to an AgentAPI hub (agentapi hub join), so the hub lists them and opens them in its chat. Use when asked to add, connect or join this machine, workspace or AgentAPI to a hub, or to check or undo that.
---

# Join an AgentAPI hub

An AgentAPI hub (`agentapi hub serve`) lists the AgentAPIs of many machines on one page. A machine
joins once: `agentapi hub join` writes the hub into `~/.agentapi/config.yaml`, which every
`agentapi server` of this user reads when it starts. AgentAPIs that run already get the change at
once. The guide is [docs/hub.md](https://github.com/k1dav-c/agentapi/blob/main/docs/hub.md).

## What to ask for

Ask the user for these; never guess or make them up:

| What | Example | Notes |
| --- | --- | --- |
| The hub's webhook URL | `http://hub.vpn:3290/hooks` | The hub's address as this machine reaches it, plus `/hooks` |
| The hub secret | (a hex string) | `AGENTAPI_HUB_SECRET` on the hub's machine; it signs this machine's events |
| Where the hub reaches this machine | `http://dev-1.vpn:{port}` | This machine's name or address on the network the hub is on; `{port}` stays as it is |

The hub and this machine must reach each other: on one private network or VPN, or the same
machine (`http://127.0.0.1:3290/hooks` and `http://127.0.0.1:{port}`). A URL behind a sign-in
page, such as a Coder port URL, doesn't work for either direction: the hub and the AgentAPI
send requests, not browsers.

## Steps

1. **Check the version.** The hub needs AgentAPI 1.3 or later here:

   ```bash
   agentapi --version
   ```

   If it is older, ask before you run `sudo agentapi update`.

2. **Check that this machine reaches the hub.** An unsigned event is refused with 401, which shows
   the hub is there:

   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' -X POST http://hub.vpn:3290/hooks
   ```

   `401`: the hub answers. `000` or a timeout: wrong address, or no route to the hub. `302`, `303`
   or an HTML page: a sign-in page is in the way. Stop and tell the user; joining won't work.

3. **Join.** Give the secret through the environment, so it doesn't show in the command line or
   the shell history, and never print it:

   ```bash
   read -rsp 'Hub secret: ' AGENTAPI_HUB_SECRET && export AGENTAPI_HUB_SECRET && echo
   agentapi hub join http://hub.vpn:3290/hooks --public-url 'http://dev-1.vpn:{port}'
   ```

   If you can't type into a prompt, have the user export `AGENTAPI_HUB_SECRET` in their own shell,
   or pass the value they give you as `AGENTAPI_HUB_SECRET=… agentapi hub join …` without echoing
   it. If this machine's AgentAPIs need an API token (`--api-token`), add `--api-token` (or set
   `AGENTAPI_API_TOKEN`); the hub then needs the same token as its `--instance-token`.

   `join` prints one line per running AgentAPI:
   - `applied`: it reports to the hub now.
   - `needs an API token`: give `--api-token` and run `join` again.
   - Any AgentAPI that isn't listed started before 1.2: it joins when it next starts.

4. **Check it.**

   ```bash
   agentapi hub status
   ```

   It names the hub in the config file and, for each running AgentAPI, where it reports. Each one
   should report to the hub's `/hooks` URL. Then ask the user to look at the hub's page: the
   agents appear within a second, as Connecting and then in their state.

5. **Make sure the hub can open them.** The hub sends this machine's name as the `Host` header.
   An AgentAPI accepts only `localhost`, `127.0.0.1` and `[::1]` unless it runs with
   `--allowed-hosts '*'` (as the Coder module starts it) or lists the name. If the hub shows the
   instance as unreachable with `400 Bad Request`, add the name to `~/.agentapi/config.yaml`:

   ```yaml
   allowed-hosts: [localhost, 127.0.0.1, "[::1]", dev-1.vpn]
   ```

   That takes effect when the AgentAPI next starts.

## Rules

- Don't restart or stop an AgentAPI to make it join: the one running this session is likely one
  of them. Tell the user which ones need a restart and let them choose when.
- Don't print, log or commit the secret, and don't write it anywhere but through `agentapi hub
  join` (which keeps `~/.agentapi/config.yaml` readable by its owner only).
- `agentapi hub join` changes every AgentAPI of this user on this machine. Say so before you run
  it if the user asked about only one.
- To undo it, `agentapi hub leave` removes the hub from the config file and from the running
  AgentAPIs.

## When it doesn't work

| What the user sees | Why | What to do |
| --- | --- | --- |
| The instance never appears; the hub logs "Refused an event" | The secret differs from the hub's, or the clocks are more than 10 minutes apart | Join again with the hub's secret; check `date` on both machines |
| The instance never appears; `agentapi hub status` shows no hub | `join` ran as another user, or with another `--config`, than the AgentAPI | Run `join` as the user the AgentAPI runs as |
| Unreachable: connection refused or timed out | The hub can't reach the `--public-url` | Have the user run `curl <public-url>/summary` on the hub's machine; join again with an address that answers |
| Unreachable: `400 Bad Request` | The AgentAPI refuses the name as `Host` | Step 5 |
| Unreachable: `needs an API token` | The AgentAPI requires a token the hub doesn't send | The hub needs `--instance-token` with the AgentAPI's token |
