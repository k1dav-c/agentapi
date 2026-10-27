# The hub

[English](hub.md) | [繁體中文](hub.zh-TW.md)

A hub watches the AgentAPIs of many machines from one page. It lists every instance, puts the ones
that need you first, and opens any of them in the chat, on a desktop or a phone. It runs from the
same binary: `agentapi hub serve`.

![The hub: its instances, what needs you first, and one open in the chat](screenshots/hub-desktop.png)

The [README](../README.md#running-a-hub) has the short version. This guide covers setup step by
step, what each state means, the tokens, troubleshooting and the hub's API.

## Contents

- [How it works](#how-it-works)
- [Setting it up](#setting-it-up)
- [Using the hub](#using-the-hub)
- [Tokens and security](#tokens-and-security)
- [Troubleshooting](#troubleshooting)
- [Reference](#reference)
- [Limits](#limits)

## How it works

```text
 phone / desktop browser ──HTTPS──▶ hub (agentapi hub serve, an always-on machine)
                                      ▲ signed webhook events   │ summary streams and the chat,
                                      │ (instance.*, task.*…)   ▼ with each instance's token
            ┌─────────────────────────┴────────┬────────────────────────────────┐
     AgentAPI on workspace A          AgentAPI on workspace B          AgentAPI on your laptop
```

The hub and the AgentAPIs reach each other over a network you control, such as your own VPN. The
hub never has to be on the public internet.

1. An AgentAPI joins with its webhook. `agentapi hub join` writes the hub's address and secret into
   `~/.agentapi/config.yaml`, which every `agentapi server` of that user reads when it starts. Each
   AgentAPI then sends signed `instance.started`, `instance.heartbeat` and `instance.stopped`
   events, and the hub adds, updates or stops the instance.
2. The hub follows each instance's summary (`GET /events?only=summary`): its state, what it waits
   for, its title, latest task, queue and usage limits. One connection per instance, reopened after
   1 s, then up to every 30 s when it fails. A stream silent for 45 s counts as lost. An instance
   stays connecting for the first 10 s of failures (one that is starting isn't listening yet when
   its first event comes); after that it is unreachable. The summary names the AgentAPI that
   answers: when it is another one (the address went to another machine), the instance is
   unreachable at once, and the hub sends it none of the instance's requests.
3. A browser signs in with the hub token and gets every instance and each change from one stream.
4. Opening an instance loads the chat through the hub (`/i/<id>/…`). The hub forwards each request
   with the instance's API token in place of the browser's credentials.

## Setting it up

The example uses three made-up names: the hub runs on `hub.vpn`, the AgentAPIs on `dev-1.vpn` and
`dev-2.vpn`. Use your machines' VPN names or addresses instead.

### 1. Make up the secrets

You need up to three values. Keep them out of shell history and repositories, for example in your
password manager:

```bash
openssl rand -hex 32   # HUB_TOKEN: what you sign in with
openssl rand -hex 32   # HUB_SECRET: what the AgentAPIs sign their events with
```

The third is the AgentAPIs' own API token (`--api-token`), if they run with one. The hub sends it
to them. All instances can share one, or an instance added by hand can have its own.

### 2. Run the hub

Pick a machine that stays up and that both the AgentAPIs and your phone can reach over the VPN:

```bash
agentapi hub serve --bind hub.vpn --token "$HUB_TOKEN" --secret "$HUB_SECRET" \
  --instance-token "$AGENTAPI_TOKEN"
```

It listens on port 3290 and keeps the instances in `~/.agentapi/hub/instances.json`, so it still
knows them after a restart. `--bind` limits it to the VPN interface; leave it out to listen
everywhere. The values can come from `AGENTAPI_HUB_TOKEN`, `AGENTAPI_HUB_SECRET` and
`AGENTAPI_HUB_INSTANCE_TOKEN` instead of flags.

To keep it running, start it the way the machine starts its other services. A systemd user unit,
for example:

```ini
# ~/.config/systemd/user/agentapi-hub.service
[Service]
EnvironmentFile=%h/.config/agentapi-hub.env
ExecStart=/usr/local/bin/agentapi hub serve --bind hub.vpn
Restart=on-failure

[Install]
WantedBy=default.target
```

`agentapi-hub.env` holds the three `AGENTAPI_HUB_*` variables; make it readable by its owner only.

### 3. Join each machine's AgentAPIs

On every machine whose AgentAPIs the hub should watch, once:

```bash
sudo agentapi update   # the hub needs AgentAPI 1.3 or later on each machine
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" \
  --public-url 'http://dev-1.vpn:{port}'
```

- `join` first sends the hub a signed test event and stops if the hub doesn't take it (unreached, a
  sign-in page in the way, or another secret); see the [reference](#agentapi-hub-join-leave-status).
- `join` writes the hub into `~/.agentapi/config.yaml`. Every `agentapi server` of this user reads
  that file when it starts, so AgentAPIs started by a script with fixed flags, such as a Coder
  workspace's start script, join without a change to the script. Flags and `AGENTAPI_*` variables
  still win over the file.
- `--public-url` is where the hub reaches each AgentAPI. `{port}` becomes each one's port, so one
  line serves the Claude Code AgentAPI on 3286 and the Codex one on 3285 alike.
- AgentAPIs that are running now get the change at once and appear in the hub within a second.
  Ones that started before 1.2 aren't registered on the machine, so `join` can't reach them:
  restart them, or the workspace, once.
- The hub forwards requests with the instance's VPN name as the `Host` header. An AgentAPI accepts
  only `localhost`, `127.0.0.1` and `[::1]` by default, so allow its VPN name: start it with
  `--allowed-hosts '*'` (the Coder module does), or add the name to the config file:

  ```yaml
  allowed-hosts: [localhost, 127.0.0.1, "[::1]", dev-1.vpn]
  ```

Check it with:

```bash
agentapi hub status   # the hub in the config file, and where each running AgentAPI reports
agentapi list         # the AgentAPIs running here and what each one's summary says
```

An agent on the machine can do this step for you. The
[agentapi-hub-join skill](../skills/agentapi-hub-join/SKILL.md) has Claude Code check the version
and that the hub answers, ask you for the hub's URL, its secret and this machine's address, join,
and check where each AgentAPI reports. It never restarts an AgentAPI or prints the secret. Install
it on that machine:

```bash
mkdir -p ~/.claude/skills/agentapi-hub-join
curl -fsSL https://raw.githubusercontent.com/k1dav-c/agentapi/main/skills/agentapi-hub-join/SKILL.md \
  -o ~/.claude/skills/agentapi-hub-join/SKILL.md
```

Then ask the agent, for example: "Join this machine to the hub at http://hub.vpn:3290".

### 4. Open it

Open `http://hub.vpn:3290/` on a desktop or a phone that is on the VPN, and sign in with
`HUB_TOKEN`. The session lasts 30 days.

## Using the hub

### Signing in

![The hub's sign-in](screenshots/hub-sign-in.png)

The token goes to the hub once and comes back as an HttpOnly cookie derived from it; the page never
stores the token. Changing `--token` signs every browser out. A wrong token is refused on the spot,
and the eye button shows what you pasted.

A hub that only a sign-in of its own reaches, such as a Coder workspace app or a private VPN, can
run without a token: `--no-token` (instead of `--token`) skips this page, and everyone who reaches
the hub is in. The hub refuses to start with neither.

When the hub doesn't answer (it stopped, or a proxy in front of it answers with an error page), the
page says it can't reach the hub and tries again; only a hub that says the session ended asks for
the token.

### The overview

![The overview: how many need you, a tile per state, and a card for each request](screenshots/hub-desktop.png)

With no agent open, the right side says how many agents need you and why. A tile per state (need
you, working, finished unseen, offline) counts them; pick one to filter the list. Below, a card per
agent that needs you names what it waits for (the approval's command, the question, the login, the
usage limit) and since when, with **Answer** or **Open**. Running agents show their prompt and how
long they've run; finished ones you haven't seen show their title.

![The overview in the dark theme](screenshots/hub-desktop-dark.png)

### Spend

![What the agents spent: the last 24 hours and 7 days, and the agents that spent the most](screenshots/hub-spend.png)

Each AgentAPI works out what its agent's sessions in its working directory cost over the last 24
hours and the last 7 days, from the agent's session logs, once a minute, and puts it in its summary
(`spend`). It is the API-equivalent cost: the tokens at the model providers' list prices, which a
subscription doesn't charge. Only native Claude Code, Codex and Pi have it; other agents show none.

A row shows its agent's last 24 hours (`$110/24h`), a group header its group's, and the top of the
list all of them; hover for the 7 days too. On a desktop the overview has a **Spend** card: the 24
hours and 7 days of every agent that reports it, and the five that spent the most this week.

### The list

![Every state the list shows](screenshots/hub-list.png)

Each row shows the agent's name (`--name`, by default its working directory's name), what it waits
for or does, how long ago, the agent and the machine; a queue that isn't empty, what the agent spent
in the last 24 hours ([Spend](#spend)) and a usage limit above 60% join them. Within a [group](#groups), the rows sort by state in this order, then by name.
Each state has a shape of its own, so it reads without its color:

| Glyph | Second line starts with | Meaning |
| --- | --- | --- |
| Amber ! | Approval, Question, Plan, Dialog | A request waits for your answer; the line shows it |
| Amber ! | Login | The agent can't work until you log in on its machine |
| Amber ! | Usage limit | A usage limit stopped its turn; queued messages wait for the reset |
| Turning blue arc | Working | The agent works on the task shown |
| Green check | Finished | A task ended since anyone last looked at this agent |
| Grey ring | (its title) | Idle |
| Struck-through bell, grey | Muted | It needs you, and you muted that until it changes ([Muting](#muting)) |
| Dashed ring, turning | Connecting | The hub is opening its summary stream, or opening it again |
| Broken red ring | Unreachable | The hub hasn't reached it for 10 s; the line says why |
| Square | Stopped, Exited | The AgentAPI stopped, or its agent exited |

Search (**/**) matches any word of a name, group, title, machine or agent. The chips under it show
all agents or only those that need you, work, finished unseen, or are offline, each with its count.
The header says whether the hub's stream is live and how many agents there are; the tab's title and
icon count the ones that need you, so a pinned tab shows it. The stream reconnects as the chat's
does, and a stream silent for 45 s (the hub sends a heartbeat every 15 s) counts as lost.

"Finished" follows the agent's unread mark, which every client of that AgentAPI shares: opening it
in the hub, or in its own chat, marks it read everywhere.

The hub works out each row's state. A page older than the hub, kept open across an update, shows
a state it doesn't know as Unknown, with a question mark; reload the page.

### Groups

![A row's menu on a phone: its group, a new one, and Forget](screenshots/hub-phone-menu.png)

Sort the instances into groups of your own, by client, project or machine. The **⋯** at the end of
each row moves the instance to a group, to a **New group…**, or to **No group**. Groups are kept on
the hub, so the phone and the desktop show the same ones, and an instance's own events never move
it.

Once there is a group, the list gets a header per group, by name, with the instances in no group
last under **Ungrouped**. A header shows how many instances the group has and, in amber, how many
need you, so a folded group still tells you. Tap a header to fold or unfold it; each browser
remembers which groups it folded. The pencil renames the group, moving all its instances. A group
name has at most 40 characters, and a group with no instance left disappears.

### Muting

![A muted row: what it waits for, out of the way](screenshots/hub-muted.png)

An agent may wait for something you won't do soon, such as a login on a machine you don't use it
on. **Mute until it changes** in its **⋯** menu takes it out of what needs you: it stops counting
in **Need you**, the group's badge, the tab and the overview, and sends no notification. The row
stays, grey, with a struck-through bell and what it waits for. The mute lasts as long as that
does: once the agent logs in, or asks something else (another request, a usage limit), it counts
again. **Unmute** in the same menu ends it sooner. Mutes are kept on the hub, like groups.

### Opening an instance

![An agent open beside the list, its approval ready to answer](screenshots/hub-instance.png)

Pick a row or a card to open the agent's chat beside the list: everything the chat does works here,
answering requests included. The bar above it shows the agent's state, agent and machine, with the
chat's own search, terminal and Explorer buttons; **New tab** opens the chat on its own, and **×** (or **Esc**) goes back to the overview. Opening an agent
is a page in the browser's history, so Back returns to the list or the overview; moving from one
agent to another doesn't add one. The URL keeps the agent (`#<id>`), so a reload or a bookmark
comes back to it.

### Keys and notifications

| Key | Does |
| --- | --- |
| `/` | Search |
| `j`, `k` (or the arrows) | Move between the rows |
| `Enter` | Open the row |
| `Esc` | Clear the search, or go back to the overview |

The keys work while the hub has the focus; inside an open chat, keys go to the chat. The bell asks
the browser to notify you when an agent starts needing you while the hub's tab is in the background,
and a screen reader hears the same as it happens.

### On a phone

| The list | An instance |
| --- | --- |
| ![The hub's list on a phone](screenshots/hub-phone.png) | ![An instance open on a phone](screenshots/hub-phone-chat.png) |

On a narrow screen the list and the chat take turns; the back arrow at the top, or the phone's
back gesture, goes back to the list. The chat keeps the screen for the conversation: one bar above
it, and the composer one row. Answering
an approval from the phone:

![Answering an approval from the hub on a phone](screenshots/demo-hub.gif)

### Connecting the first agents

![The guide an empty hub shows](screenshots/hub-empty.png)

A hub no agent has joined yet shows the three steps of [Setting it up](#3-join-each-machines-agentapis)
with commands to copy, its own address filled in. An AgentAPI's own chat has the same steps for
its machine: **Add to an AgentAPI hub…** in the menu of the status in its header
([Session details](chat-ui.md#session-details-and-alerts)).

### Adding and forgetting instances

![Adding an instance by its URL](screenshots/hub-add.png)

**+** adds an AgentAPI by its URL, for one that can't run `agentapi hub join` (an older version, or
one you don't log in to). The hub asks it for its summary first, so it must be reachable and run
AgentAPI 1.1 or later. A token given here is kept for that instance and sent instead of
`--instance-token`. An instance added this way doesn't report starting or stopping; the hub just
finds it reachable or not.

The **⋯** menu of a stopped or unreachable row has **Forget**, which removes it from the hub. An
instance that joined with its webhook comes back the next time it starts.

## Tokens and security

| Value | Who holds it | What it does |
| --- | --- | --- |
| Hub token (`--token`) | You, and the hub | Signs a browser in to the hub |
| Hub secret (`--secret`) | The hub, and each machine's config file | Signs the AgentAPIs' events, so only your machines can add instances |
| Instance token (`--instance-token`, or one per added instance) | The hub, and the AgentAPIs | Lets the hub call the AgentAPIs; browsers never get it |

- The hub takes an event only with a valid `X-AgentAPI-Signature-256` (HMAC-SHA256 of
  `<timestamp>.<body>`), a timestamp at most 10 minutes off, and a delivery ID it hasn't taken yet.
  This matters because an event names the URL the hub sends an instance token to.
- The hub sends an instance's requests only while the AgentAPI at its URL says it is that
  instance, and doesn't let another run with the same ID move it elsewhere while it still follows
  this one.
- The session cookie is HttpOnly and SameSite=Lax, derived from the hub token rather than holding
  it. The proxy drops the browser's cookies and its `Authorization`, `Origin` and `Referer`
  headers, and drops the instance's `Set-Cookie`, so neither side's credentials reach the other.
- `~/.agentapi/hub/instances.json` holds the tokens of instances added by hand; it is created
  readable by its owner only. `~/.agentapi/config.yaml` holds the hub secret; `agentapi hub join`
  writes it the same way.
- Signing in sends the hub token. Serve the hub only on the VPN, or behind HTTPS.
- Anyone signed in to the hub can do what the AgentAPIs allow: send tasks, answer approvals, use
  their terminals. Give the hub token only to people you would give the instance token.
- With `--no-token`, that is everyone who reaches the hub's port: keep it behind a sign-in of its
  own, and never on a public address.

## Troubleshooting

| What you see | Why | What to do |
| --- | --- | --- |
| An instance never appears | Its AgentAPI started before `join`, or before 1.2 | Restart it, or its workspace |
| `join` says a sign-in page is in front of the hub | The hub's URL is a Coder port URL (or another behind a login) | Join with the hub's address on your network or VPN; the AgentAPIs send events themselves |
| `join` says the hub is on another machine | `--public-url` names `localhost` | Give the address the hub reaches this machine at |
| An instance never appears; the hub logs "Refused an event" | `--secret` of `join` and of the hub differ, or the clocks are more than 10 minutes apart | Run `agentapi hub join` again with the hub's secret; check the clocks |
| An instance never appears; `agentapi hub status` shows no hub | `join` wrote to another config file than the one the AgentAPI reads | Use the same user, or pass the same `--config` to both |
| Unreachable: `400 Bad Request` | The AgentAPI refuses the VPN name as `Host` | Allow it in `--allowed-hosts` (step 3) |
| Unreachable: `needs an API token` | The AgentAPI runs with `--api-token` and the hub has another | Give the hub `--instance-token`, or the token when adding |
| Unreachable: connection refused or timed out | Wrong `--public-url`, or the hub can't reach the VPN | Check the URL from the hub's machine with `curl <url>/summary` |
| Unreachable: `another AgentAPI answers at this URL` | The instance's address now belongs to another machine (a new DHCP lease), or `--public-url` names another machine | Check `--public-url`; an instance that moved sends its new address with its next heartbeat, within a minute |
| The hub logs "Two AgentAPIs report the same instance ID" | Two machines have the same name, so their AgentAPIs on the same port have the same ID; the hub keeps the one it follows | Rename one machine, or give its AgentAPIs `--instance-id` |
| Unreachable, and the hub's VPN client runs in userspace | The hub's connections don't go through the VPN | Set `ALL_PROXY` to the VPN client's SOCKS5 proxy for the hub |
| "is it AgentAPI 1.1 or later?" when adding | The instance has no summary | Update it with `agentapi update` |
| The page says the hub didn't answer | Wrong address, or the hub isn't running | Open `http://<hub>:3290/api/session`; it answers JSON when the hub runs |
| Behind Coder's port URL, the page asks for Coder first | Coder's port URLs need a Coder login | Sign in to Coder in that browser, then to the hub |

`agentapi hub status` on an instance's machine shows which hub its config file names and where
each running AgentAPI reports. The hub logs each instance it starts following, stops or can't
reach (with the error), and each event it refuses; the AgentAPI logs each webhook delivery that
failed.

## Reference

### `agentapi hub serve`

| Flag | Default | Description |
| --- | --- | --- |
| `--token` | `AGENTAPI_HUB_TOKEN` | Token browsers sign in with; required, unless `--no-token` |
| `--no-token` | `false` | No sign-in: everyone who reaches the hub is in (only behind a sign-in of its own) |
| `--secret` | `AGENTAPI_HUB_SECRET` | Secret the AgentAPIs sign their events with; required |
| `--instance-token` | `AGENTAPI_HUB_INSTANCE_TOKEN`, else `AGENTAPI_API_TOKEN` | API token sent to the AgentAPIs |
| `--state` | `~/.agentapi/hub/instances.json` | File that keeps the instances |
| `--port` | `3290` | Port to listen on |
| `--bind` | all interfaces | Address to listen on, such as the VPN address |

The hub reaches the instances through the proxy of the environment (`HTTP_PROXY`, `HTTPS_PROXY`,
`ALL_PROXY`).

### `agentapi hub join`, `leave`, `status`

| Flag | Default | Description |
| --- | --- | --- |
| `--secret` | `AGENTAPI_HUB_SECRET` | Secret that signs the events; without it the hub refuses them |
| `--public-url` | | Where the hub reaches the AgentAPIs; `{port}` and `{hostname}` are filled in |
| `--events` | `instance.*`, `task.*`, `request.*`, `login.*`, `limit.*`, `agent.error` | Events sent to the hub |
| `--api-token` | `AGENTAPI_API_TOKEN` | Token for running AgentAPIs that need one |
| `--config` | `~/.agentapi/config.yaml` | Config file to write |
| `--registry-dir` | `~/.agentapi/instances` | Where the running AgentAPIs are registered |
| `--no-check` | `false` | Join without checking that the hub takes the events (for a hub that doesn't run yet) |

Before it writes anything, `join` checks that the hub takes this machine's events: it sends the hub
a signed `webhook.test` event and stops, saying why, when the hub isn't reached, a sign-in page is
in front of it (a Coder port URL redirects to Coder's login), or the secret differs. It also stops
when the hub is on another machine and `--public-url` names `localhost`, which the hub can't reach.

`join` writes `webhook-url`, `webhook-secret`, `webhook-events` and `public-url` into the config
file and applies them to the running AgentAPIs with `PUT /webhook`, keeping their timeout and
attempts. `leave` removes them and turns the running AgentAPIs' webhook off. An AgentAPI that can't
be changed now applies the file when it restarts.

### The hub's HTTP API

| Endpoint | Description |
| --- | --- |
| `POST /hooks` | AgentAPI webhook events; signed with `--secret`, no session needed |
| `GET /api/session` | `{"authenticated": true}` when the request carries the session or the hub token |
| `POST /api/session` | `{"token": …}` signs in and sets the session cookie |
| `DELETE /api/session` | Signs out |
| `GET /api/instances` | The instances: who each one is, its summary, its connection and the state its row shows (`state`: `needs-you`, `working`, `unread`, `idle`, `muted`, `connecting`, `unreachable` or `stopped`) |
| `POST /api/instances` | `{"url": …, "token": …}` adds an instance by its URL |
| `PATCH /api/instances/{id}` | `{"group": …}` puts an instance in a group, `""` in none; `{"muted": true}` mutes what it waits for now, until that changes (409 when it waits for nothing), `false` ends the mute |
| `DELETE /api/instances/{id}` | Forgets an instance |
| `GET /api/events` | Server-sent events: `instances` (all of them, as `GET /api/instances` has them), then `instance` (one changed), `removed` (`{"id"}`) and a `heartbeat` every 15 s |
| `/i/{id}/…` | The instance's own API and chat, through the hub |

Every endpoint but `/hooks` and `/api/session` needs the session cookie or
`Authorization: Bearer <hub token>`. Instance tokens never appear in any response.

### Files

| File | Written by | Holds |
| --- | --- | --- |
| `~/.agentapi/config.yaml` | `agentapi hub join` | The hub's webhook, read by every `agentapi server` |
| `~/.agentapi/instances/` | `agentapi server` | The AgentAPIs running on this machine, for `join` and `agentapi list` |
| `~/.agentapi/hub/instances.json` | `agentapi hub serve` | The instances the hub knows, their groups, and tokens of the ones added by hand |

## Limits

- The hub serves the chat of its own version to every instance. Run the same AgentAPI version on
  the hub and the instances.
- One hub token for everyone: there are no per-user accounts or permissions.
- The hub watches and opens AgentAPIs; it doesn't start or stop them.
- Nothing reaches your phone while the hub's page is closed: the hub doesn't send push
  notifications yet. Each AgentAPI has one webhook, and the hub uses it.
