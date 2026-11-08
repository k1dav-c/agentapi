---
name: agentapi-hub-join
description: Join this machine's AgentAPIs to an AgentAPI hub (agentapi hub join), so the hub lists them, opens them in its chat and, if the user allows it, updates them. Use when asked to add, connect or join this machine, workspace or AgentAPI to a hub, or to check or undo that.
---

# Join an AgentAPI hub

An AgentAPI hub (`agentapi hub serve`) lists the AgentAPIs of many machines on one page. A machine
joins once: `agentapi hub join` writes the hub into `~/.agentapi/config.yaml`, which every
`agentapi server` of this user reads when it starts. AgentAPIs already running get the change at
once. The guide is [docs/hub.md](https://github.com/k1dav-c/agentapi/blob/main/docs/hub.md).

## What to ask for

The hub's page has a prompt with these values filled in: **+** opens its Add dialog, and **Join a
machine** has a prompt for an agent on the machine. If the user pasted it, use its values. Otherwise
ask the user for them; never guess or make them up:

| What | Example | Notes |
| --- | --- | --- |
| The hub's webhook URL | `http://192.168.1.20:3290/hooks` | The hub's address on the network this machine shares with it, plus `/hooks`. The Add dialog lists the hub's addresses, and the hub logs them when it starts |
| The hub secret | (a hex string) | `AGENTAPI_HUB_SECRET` on the hub's machine; it signs this machine's events. The Add dialog shows it only on a hub with a token |
| Where the hub reaches this machine | `http://{ip}:{port}` | Use it as written: each AgentAPI fills in `{ip}` with this machine's address on the route to the hub and `{port}` with its port, and follows the address when DHCP changes it. Take another address or name only if the user gives one |
| Updates from the hub | yes or no | Whether the hub may update this machine's AgentAPIs to the latest release (`--allow-update`). Ask; the default is no |

### Picking the addresses

The hub and this machine must reach each other: on one private network or VPN, or the same
machine (`http://127.0.0.1:3290/hooks` and `http://127.0.0.1:{port}`). A URL behind a sign-in
page, such as a Coder port URL, doesn't work for either direction: the hub and the AgentAPI
send requests, not browsers.

Don't look up this machine's address: `{ip}` is the address it sends to the hub from, which is the
one the hub reaches it at on a shared network or VPN. Only when the hub reaches it at another
address (NAT, a port forward) does `--public-url` need that address or a name instead, and the
user has to say which. If the user gave several hub URLs, use the one whose check in step 2
answers 401.

## Steps

1. **Check the version.** `{ip}` in `--public-url` needs AgentAPI 2.2.0 or later,
   whose `join` also checks that the hub takes this machine's events before it writes anything and
   takes `--allow-update`:

   ```bash
   agentapi --version
   ```

   If it is 2.1.2 or older, it needs `sudo agentapi update` first. Run that only if the user asked
   for it (the hub page's prompt does); otherwise ask.

2. **Check that this machine reaches the hub.** An unsigned event is refused with 401, which shows
   the hub is there:

   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' -X POST http://192.168.1.20:3290/hooks
   ```

   `401`: the hub answers. `000` or a timeout: wrong address, or no route to the hub; try the hub's
   other URLs, if the user gave any. `302`, `303` or an HTML page: a sign-in page is in the way.
   Stop and tell the user; joining won't work.

3. **Join.** Give the secret through the environment, so it doesn't show in the command line or
   the shell history, and never print it:

   ```bash
   read -rsp 'Hub secret: ' AGENTAPI_HUB_SECRET && export AGENTAPI_HUB_SECRET && echo
   agentapi hub join http://192.168.1.20:3290/hooks --public-url 'http://{ip}:{port}'
   ```

   If you can't type into a prompt, have the user export `AGENTAPI_HUB_SECRET` in their own shell,
   or pass the value they give you as `AGENTAPI_HUB_SECRET=… agentapi hub join …` without echoing
   it. If this machine's AgentAPIs need an API token (`--api-token`), add `--api-token` (or set
   `AGENTAPI_API_TOKEN`); the hub then needs the same token as its `--instance-token`. Add
   `--allow-update` only if the user said yes to updates from the hub (step 4).

   `join` prints the address `{ip}` stands for now (`{ip} is 192.168.1.31 now`), then sends the hub
   a signed test event. `The hub at … took a signed test event` means the
   hub gets this machine's events. Otherwise it stops without writing anything, and says why:
   - `can't reach the hub` or `didn't answer`: wrong address, or no route to the hub.
   - `a sign-in page is in front of it`: the URL is behind a login, such as a Coder port URL. Ask for
     the hub's address on the network or VPN instead.
   - `refused the signature`: the secret differs from the hub's, or the clocks are more than 10
     minutes apart.
   - `is on another machine, so it can't reach this one at localhost`: give the address the hub
     reaches this machine at as `--public-url`.
   - `no address of this machine for {ip}`: this machine has no IPv4 route to the hub, nor an
     address other machines may reach. Ask the user for the address the hub reaches it at.

   Don't work around these with `--no-check`; that is only for a hub that doesn't run yet, and
   only when the user says so.

   Then it prints one line per running AgentAPI:
   - `applied`: it reports to the hub now.
   - `applied; too old for {ip}, it reports http://… until it restarts`: an AgentAPI of 2.1.2 or
     older got today's address, and follows the machine's address only after a restart. Tell the
     user; don't restart it.
   - `needs an API token`: give `--api-token` and run `join` again.
   - Any AgentAPI that isn't listed started before 1.2: it joins when it next starts.

4. **Updates from the hub,** only with `--allow-update`. `join` writes `allow-update: true` and says
   the AgentAPIs take it up when they start: `restart the running ones to let it update them too`.
   Don't restart them; tell the user which ones run.

   If this machine's `agentapi` can't replace its own binary, `join` also prints the one-time setup:

   ```text
   The hub can't update them yet: AgentAPI can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable. Move it to a directory you own, once: mkdir -p /home/me/.local/bin && cp /usr/local/bin/agentapi /home/me/.local/bin/agentapi && sudo ln -sf /home/me/.local/bin/agentapi /usr/local/bin/agentapi
   ```

   An AgentAPI updates by replacing its own binary, and never runs `sudo`; a binary in a directory
   owned by root blocks that. The command copies the binary to the user's `~/.local/bin` and leaves
   a symlink in its place, so updates replace the copy. It needs `sudo`, once. Explain this, show
   the command as `join` printed it, and ask the user before you run it. If they agree but `sudo`
   asks for a password you can't give, have them run it.

   An update restarts the AgentAPI. Without a state DB the agent resumes its session, but only
   one with a state DB keeps its whole conversation, queued messages included, across the
   restart. If `~/.agentapi/config.yaml` has no `state-db`, suggest this line, which gives each
   AgentAPI a file of its own, and ask before you add it; it applies when they next start:

   ```yaml
   state-db: ~/.agentapi/state/{port}.db
   ```

5. **Check it.**

   ```bash
   agentapi hub status
   ```

   It names the hub in the config file and, for each running AgentAPI, where it reports. Each one
   should report to the hub's `/hooks` URL. Then ask the user to look at the hub's page: the
   agents appear within a second, as Connecting and then in their state.

6. **Make sure the hub can open them.** The hub sends the host of `--public-url` as the `Host`
   header. An AgentAPI accepts `localhost`, `127.0.0.1`, `[::1]` and the address `{ip}` stands for,
   unless it runs with `--allowed-hosts '*'` (as the Coder module starts it) or lists more. With a
   name or a fixed address in `--public-url`, if the hub shows the instance as unreachable with
   `400 Bad Request`, add it to `~/.agentapi/config.yaml`:

   ```yaml
   allowed-hosts: [localhost, 127.0.0.1, "[::1]", 192.168.1.31]
   ```

   That takes effect when the AgentAPI next starts.

## A machine joined with a fixed IP

A machine joined with a fixed address in `--public-url`, such as `http://192.168.1.31:{port}`,
keeps reporting it after DHCP gives the machine another one (a Coder workspace that restarted, for
example), and the hub shows its agents as unreachable (`no route to host`). Run `join` again with
the same hub URL and `--public-url 'http://{ip}:{port}'` (steps 1 to 5). The hub moves each
instance to the new address with its next heartbeat, within a minute, and after every later change
once the old address stops answering, within about two minutes.

## Rules

- Don't restart or stop an AgentAPI to make it join: the one running this session is likely one
  of them. Tell the user which ones need a restart and let them choose when.
- Don't print, log or commit the secret, and don't write it anywhere but through `agentapi hub
  join` (which keeps `~/.agentapi/config.yaml` readable by its owner only).
- Don't run anything with `sudo` (`agentapi update`, the one-time setup) unless the user asked
  for it or agreed.
- Add `--allow-update` and `state-db` only when the user said yes.
- `agentapi hub join` changes every AgentAPI of this user on this machine. Say so before you run
  it if the user asked about only one.
- To undo it, `agentapi hub leave` removes the hub from the config file and from the running
  AgentAPIs. It keeps `allow-update`; remove that line to turn updates from the hub off.

## When it doesn't work

| What the user sees | Why | What to do |
| --- | --- | --- |
| The instance never appears; the hub logs "Refused an event" | The secret differs from the hub's, or the clocks are more than 10 minutes apart | Join again with the hub's secret; check `date` on both machines |
| The instance never appears; `agentapi hub status` shows no hub | `join` ran as another user, or with another `--config`, than the AgentAPI | Run `join` as the user the AgentAPI runs as |
| Unreachable (`no route to host`, or timed out) after the machine got a new IP address | It joined with a fixed IP, which DHCP changed | Join again with `--public-url 'http://{ip}:{port}'` ([A machine joined with a fixed IP](#a-machine-joined-with-a-fixed-ip)) |
| Unreachable: connection refused or timed out | The hub can't reach the `--public-url` | Have the user run `curl <public-url>/summary` on the hub's machine; join again with an address that answers |
| Unreachable: `400 Bad Request` | The AgentAPI refuses the name as `Host` | Step 6 |
| Unreachable: `needs an API token` | The AgentAPI requires a token the hub doesn't send | The hub needs `--instance-token` with the AgentAPI's token |
| `unknown flag: --allow-update`, or `--public-url: … not "http://{ip}:1"` | AgentAPI is 2.1.2 or older | Update it first (step 1) |
| `The hub can't update them yet: AgentAPI can't replace …` | The binary is in a directory this user can't write, such as `/usr/local/bin` | The one-time setup it prints, once the user agrees (step 4) |
| The hub's page says updates are off on this machine | No `allow-update: true`, or the AgentAPI started before it was written | Join with `--allow-update`; the AgentAPIs take it up when they restart |
