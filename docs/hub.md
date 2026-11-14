# The hub

[English](hub.md) | [繁體中文](hub.zh-TW.md)

A hub watches the AgentAPIs of many machines from one page. It lists every instance, puts the ones
that need you first, and opens any of them in the chat, on a desktop or a phone. It runs from the
same binary: `agentapi hub serve`.

![The hub: its instances, what needs you first, and one open in the chat](screenshots/hub-desktop.png)

The [HTTP API guide](api.md#running-a-hub) has the short version.

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
                                      │ (instance.*)            ▼ with each instance's token
            ┌─────────────────────────┴────────┬────────────────────────────────┐
     AgentAPI on workspace A          AgentAPI on workspace B          AgentAPI on your laptop
```

The hub and the AgentAPIs reach each other over a network you control, such as your own VPN. The
hub never has to be on the public internet.

1. An AgentAPI joins with its webhook. `agentapi hub join` writes the hub's address and secret into
   `~/.agentapi/config.yaml`, which every `agentapi server` of that user reads when it starts. Each
   AgentAPI then sends signed `instance.started`, `instance.heartbeat` (every minute) and
   `instance.stopped` events, and the hub adds, updates or stops the instance (`instance.stopped`
   doesn't stop one added by hand: the hub keeps trying it). An instance the hub can't reach that also
   missed three heartbeats, such as one whose workspace was killed, counts as stopped, and the hub
   stops trying until its next heartbeat. These events only add instances and say where they are;
   what each one does comes from its summary. An event that names another URL moves the instance
   there only once the old URL is dead to the hub: it failed there past the 10 s grace
   (unreachable, or stopped after missed heartbeats), or another AgentAPI answers there. Never while
   the hub is connected or still connecting (after the hub restarts, or within 10 s of a dropped
   stream), and never one added by hand. One stopped by `instance.stopped` is tried at its old URL
   first. An AgentAPI whose machine got another address (`{ip}` in its `--public-url`) moves with
   its first heartbeat after the old address became unreachable: within about two minutes (see
   [Troubleshooting](#troubleshooting)). The file keeps the URL where the hub last read each
   instance's summary, and a restarted hub follows it there first.
2. The hub follows each instance's summary (`GET /events?only=summary`), the source of truth for
   what the instance does: its state, what it waits for, its title, latest task, queue and usage
   limits. One connection per instance, reopened after 1 s, then up to every 30 s when it fails. A
   stream silent for 45 s counts as lost. An instance stays connecting for the first 10 s of
   failures (one that is starting isn't listening yet when its first event comes); after that it
   is unreachable. The summary names the AgentAPI that answers: when it is another one (the address
   went to another machine), the instance is unreachable at once, and the hub sends it none of the
   instance's requests.
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
knows them after a restart. It writes the file within a second of a change, and when it stops.
`--bind` limits it to the VPN interface; leave it out to listen everywhere. The values can come
from `AGENTAPI_HUB_TOKEN`, `AGENTAPI_HUB_SECRET` and `AGENTAPI_HUB_INSTANCE_TOKEN` instead of
flags.

Once it listens, the hub logs the `agentapi hub join` command for each address machines may reach
it at: the `--bind` address, or else each address of the machine, private networks first, then
CGNAT ones such as Tailscale's. Containers' and bridges' interfaces don't count. The log leaves out
the secret: `join` takes it from `AGENTAPI_HUB_SECRET`.

```text
level=INFO msg="To join a machine, run this on it with AGENTAPI_HUB_SECRET set to the hub's secret" interface=eth0 command="agentapi hub join http://192.168.1.20:3290/hooks --public-url 'http://{ip}:{port}'"
```

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

After an update, restart the hub this way too (`systemctl --user restart agentapi-hub`). The
AgentAPIs restart in place with `agentapi update --restart` or
[`agentapi restart`](cli.md#agentapi-restart); the hub isn't one of them. The overview's
[Versions](#versions) card shows the AgentAPIs still on an older version, and updates those whose
machine allows it.

### 3. Join each machine's AgentAPIs

On every machine whose AgentAPIs the hub should watch, once:

```bash
sudo agentapi update   # {ip} needs AgentAPI newer than 2.1.2 on each machine
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" \
  --public-url 'http://{ip}:{port}'
```

The hub's page has this command ready, with the hub's address and secret filled in, and a prompt
for an agent on the machine: [Joining a machine from the page](#joining-a-machine-from-the-page).

- `join` first sends the hub a signed test event and stops if the hub doesn't take it (unreached, a
  sign-in page in the way, or another secret); see the [reference](#agentapi-hub-join-leave-status).
- `join` writes the hub into `~/.agentapi/config.yaml`. Every `agentapi server` of this user reads
  that file when it starts, so AgentAPIs started by a script with fixed flags, such as a Coder
  workspace's start script, join without a change to the script. Flags and `AGENTAPI_*` variables
  still win over the file.
- `--public-url` is where the hub reaches each AgentAPI. `{port}` becomes each one's port, so one
  line serves the Claude Code AgentAPI on 3286 and the Codex one on 3285 alike. `{ip}` becomes the
  machine's IPv4 address on the route to the hub. Each AgentAPI looks it up again while it runs (at
  most every 30 seconds), so when DHCP gives the machine another address, as after a Coder
  workspace restarts, its next heartbeat names the new one and the hub follows it
  ([How it works](#how-it-works)). `{ip}` needs AgentAPI newer than 2.1.2; a name that
  always resolves, such as `http://dev-1.vpn:{port}`, works too.
- The same file can keep each AgentAPI's conversation when it restarts (an update, a reboot): add
  `state-db: ~/.agentapi/state/{port}.db` to it; see
  [Persistent sessions](agents.md#persistent-sessions).
- Add `--allow-update` to let the hub update this machine's AgentAPIs
  ([Updating from the hub](#updating-from-the-hub)).
- AgentAPIs that are running now get the change at once and appear in the hub within a second.
  Ones that started before 1.2 aren't registered on the machine, so `join` can't reach them:
  restart them, or the workspace, once.
- The hub forwards requests with the host of the instance's URL as the `Host` header. By default an
  AgentAPI accepts `localhost` and any IP address, so `{ip}` and a fixed address need nothing. For
  a VPN name, start it with `--allowed-hosts '*'` (the Coder module does), or add the name
  to the config file:

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
and that the hub answers, and ask you for the hub's URL, its secret, this machine's address and
whether the hub may update this machine's AgentAPIs. It then joins and checks where each AgentAPI
reports. It never
restarts an AgentAPI or prints the secret, and asks before it runs anything with `sudo`. Install it
on that machine:

```bash
mkdir -p ~/.claude/skills/agentapi-hub-join
curl -fsSL https://raw.githubusercontent.com/k1dav-c/agentapi/main/skills/agentapi-hub-join/SKILL.md \
  -o ~/.claude/skills/agentapi-hub-join/SKILL.md
```

Then give the agent the prompt from the hub page's Add dialog, which has these values, or ask it,
for example: "Join this machine to the hub at http://hub.vpn:3290".

### 4. Open it

Open `http://hub.vpn:3290/` on a desktop or a phone that is on the VPN, and sign in with
`HUB_TOKEN`. The session lasts 30 days.

## Using the hub

### Signing in

![The hub's sign-in](screenshots/hub-sign-in.png)

The token goes to the hub once and comes back as an HttpOnly cookie signed with it; the page never
stores the token. The hub accepts the cookie for 30 days from the sign-in. Changing `--token`
signs every browser out. A wrong token is refused on the spot, and the eye button shows what you
pasted.

A hub behind a sign-in of its own, such as a Coder workspace app or a private VPN, can run
without a token: `--no-token` (instead of `--token`) skips this page, and everyone who reaches
the hub is in. The hub refuses to start with neither.

When the hub doesn't answer (it stopped, or a proxy in front of it answers with an error page), the
page says it can't reach the hub and tries again; only a hub that says the session ended asks for
the token.

### The overview

![The overview: how many need you, a tile per state, and a card for each request](screenshots/hub-desktop.png)

With no agent open, the right side says how many agents need you and why. A tile per state (need
you, working, finished unseen, offline) counts them; pick one to filter the list by that state,
keeping the agents picked ([below](#the-list)). Below, a card per
agent that needs you names what it waits for (the approval's command, the question, the login, the
usage limit) and since when, with **Answer** or **Open**. Running agents show their prompt and how
long they've run; finished ones you haven't seen show their title. Between the tiles and these cards
are the [Spend](#spend), [Tokens](#token-usage), [Providers](#provider-usage), [Work time](#work-time)
and [Activity](#activity) cards; at the bottom, the [Versions](#versions) card counts the AgentAPI versions the agents run.
The chevron before a card's title folds the card to its title and its 7 days' figure (Activity's:
the year's); the browser remembers which cards you folded.

![The overview in the dark theme](screenshots/hub-desktop-dark.png)

### Spend

![What the agents spent: the last 24 hours and 7 days, the agents that spent the most, and each machine](screenshots/hub-spend.png)

Once a minute, each AgentAPI works out from its agent's session logs what the agent's sessions in
its working directory cost over the last 24 hours and the last 7 days, and puts it in its summary
(`spend`). It is the API-equivalent cost: the tokens at the model providers' list prices, which a
subscription doesn't charge. Claude Code, Codex and Pi have it; `native-demo` and an AgentAPI 2's
PTY agents show none.

A row shows its agent's last 24 hours (`$110/24h`), a group header its group's, and the top of the
list all of them; hover for the 7 days too. On a desktop the overview has a **Spend** card: the 24
hours and 7 days of every agent that reports it, the five that spent the most this week, and
**By machine**, each machine's figures once the agents run on more than one.

Two AgentAPIs of one agent in one directory of a machine read the same sessions and report the same
amounts, with the same `scope` (a hash of the machine, the agent, the directory and where the agent
keeps its logs). The totals count each scope once, with its latest figure (`updated_at`). An agent
the hub doesn't follow right now (stopped, unreachable, connecting) shows its last figure dimmed,
as last known, and the totals leave it out. A scope belongs to one machine, so the machines add up
to the totals.

The overview's tables name each agent's machine after its name, since agents on different machines
often share one (their home directory's). **By machine** puts the most this week first and leaves
out the machines with none. With more than eight, the seven with the most show and the rest add up
in one row (**Other 9 machines**). Agents that don't report their machine add up under
**Not reported**.

### Token usage

![The tokens the agents used: the last 24 hours and 7 days, by kind, the agents that used the most, each group and each machine](screenshots/hub-tokens.png)

The spend's pass also counts the tokens of the same calls: `spend` has them for the last 24 hours
(`day_tokens`) and 7 days (`week_tokens`), as `input`, `output`, `cache_write` (Anthropic's
five-minute and one-hour caches together), `cache_read` and their `total`. A call counts once, where
its cost does: a fork's copy of an earlier session's call counts in that session. Calls to a model
without a known price count their tokens and no cost. What Claude Code counted but didn't log call
by call (compacting the context) counts with the tokens its own count gives it.

`spend.providers` splits the same calls by [model provider](providers.md): for each
provider with calls in the last 7 days, its `provider` (`default` for the agent's own), `day_usd`,
`week_usd`, `day_tokens` and `week_tokens`, the most expensive this week first. A Codex call counts
toward the provider its log names; a Claude Code call toward the provider AgentAPI last recorded
its session on (`~/.agentapi/provider-ledger.jsonl`). Earlier releases don't send it.

Below Spend, the overview's **Tokens** card has the 24 hours and 7 days of every agent that reports
them, by kind, the five that used the most this week, each group's when there are groups, and each
machine's when there are several. They add up as the spend does: each scope once, connected agents only. AgentAPI 2.4.0 and older send
their spend without tokens; the card says how many agents report them. Counts are short (`950`,
`12k`, `3.4M`, `1.2B`); hover a total for the exact number. Rows have no room for another figure:
hover a row's spend for its last 24 hours of tokens.

### Provider usage

Below Tokens, once some agent has used a [model provider](#model-providers) besides its own in the
last 7 days, the overview's **Providers** card has each provider's 24 hours and 7 days, from
`spend.providers`: the agents' own as **Own provider**, the others by the name the hub keeps for
them, else by ID (one defined only in a machine's config file). Each figure is the cost over the
tokens; a gateway's models often have no known price, so a provider whose calls cost nothing but
used tokens says **no price** rather than `$0`. The most spent this week comes first, then the most
tokens. The card's totals are the tokens that went to providers other than the agents' own; it
counts each scope once, connected agents only, and says how many agents report it. AgentAPIs
before this release, and those without calls in the last 7 days, don't.

### Work time

![How long the agents worked: the last 24 hours and 7 days, the agents that worked the most, each group and each machine](screenshots/hub-work.png)

The hub times each agent while its row says it works (`working`): from the summary that starts a
turn to the one that ends it or asks you something. Waiting for you, idle, connecting, unreachable
and stopped don't count. The hub counts only what it saw, so time before the hub had this feature,
while the hub was down, and while it couldn't reach the agent isn't there. A summary stream that
drops ends the count at the last event it sent; a hub that stops ends it then. The agent counts
again from its next summary.

A row shows its agent's last 24 hours after the spend, with a timer (`2.3h`), unless it shows a
usage limit; hover for the 7 days too. Below Tokens and Providers, the overview's **Work time** card has the 24
hours and 7 days of all the agents, the five that worked the most this week, each group's when there
are groups, and each machine's when there are several. Figures are rounded down: minutes under an hour (`45m`), tenths of an hour under
ten (`2.3h`), then hours (`31h`). Unlike spend, every agent counts, stopped ones too: the time is
the hub's own record, per instance.

The hub keeps the spans of the last 7 days in its file and sends what they add up to (`work`); the
page adds the span going on.

### Activity

![Each day of the last year shaded by work time, a day's tooltip with its work time, tokens and spend](screenshots/hub-activity.png)

Below Work time, the overview's **Activity** card is a heatmap of the last year, as GitHub draws
contributions: a column per week, Sunday on top, today in the last column, a month's name above the
week it starts in. Each day's square is shaded by **Work time**, **Tokens** or **Spend**, whichever
you pick above the card (the browser remembers it): five shades, none for a day without any, then
four that each take a quarter of the days with any, so one busy day doesn't wash out the rest. Hover a square, or
focus the grid and move with the arrow keys, for that day's three figures. The days are those of
your browser's time zone; the page reads them again every 10 minutes and when you come back to it.

The hub records the activity from the day it runs a release with it, hour by hour, and keeps a year
(366 days) in `activity.json` next to its file of instances. Days before that are blank, and the
card says since when it keeps them. Work time adds up each agent's spans, as the Work time card
counts them; at the first start, the hub adds the 7 days its file of instances has. Spend and
tokens come from the agents' summaries: from 4.2 on, an AgentAPI's `spend` lists what its scope
spent in each of the last 25 hours (`hours`). The hub sets each scope's hours to the latest
figures, so a scope counts once however many AgentAPIs report it, and an hour still under way keeps
growing. An older AgentAPI's spend doesn't count until you update it. An hour more than 48 hours
old is final: no summary lists it any more, and the hub adds it into the day's totals.

### Versions

![The AgentAPI versions the agents run, on which machines, and how to update the older ones](screenshots/hub-versions.png)

Each row shows the AgentAPI version its instance runs next to its name (`v2.0.0`), and on a desktop
so does the bar above an open agent. A version older than the hub's says **older**: the hub serves
its own chat to every instance ([Limits](#limits)), so those are the ones to update. Hover for both
versions. A development build (`0.0.0-dev`, or what `make build` stamps after a release, such as
`2.0.0-21-g61be34f`) compares with nothing: it gets no mark, and a hub that runs one marks none. An
agent the hub doesn't follow right now (stopped, unreachable, connecting) shows the version it ran
last, dimmed and unmarked. After an update and a restart, the row shows the new version as soon as
the AgentAPI is back.

At the bottom of the overview, the **Versions** card counts the agents the hub follows by version,
with their machines, and says how many run an older version than the hub.

#### Updating from the hub

An AgentAPI updates itself when the hub asks, if its machine allows it. **Update AgentAPI…** in the
row's **⋯** menu asks you first. By default the update waits until the agent is idle: its task
finished, and no approval, question or plan waits for an answer. **Update now** doesn't wait: it
interrupts the task. The AgentAPI installs the latest official release for its OS and architecture,
checked against the release's `checksums.txt`, waits as asked, and restarts in place, as
[`agentapi restart`](cli.md#agentapi-restart) does. The AgentAPIs of one machine share the
binary: the first one replaces it, the others only restart.

Before it restarts, the AgentAPI runs the new program with its own settings under
`agentapi server --validate`. If the new program wouldn't start with them (a flag or an agent it
doesn't take), the AgentAPI keeps serving and the update fails, saying why. AgentAPI 3 runs only
Claude Code, Codex and Pi: an AgentAPI 2 that runs another agent, or any agent with
`--transport pty`, fails this way, but its machine already has AgentAPI 3 on disk. Put v2.5.1 back
there before that AgentAPI restarts.

The row shows how far the update got: checking, downloading, waiting for the task, restarting.
While it waits for the task, **Cancel the update** in the **⋯** menu, or **Cancel** in the bar above
the open agent, calls it off. A failed update says so; hover for the error, or open
**Update AgentAPI…** to read it and try again. Once the AgentAPI is back, the row shows its new
version.

On the **Versions** card, **Update the older ones** does this for every connected agent that runs an
older version than the hub and can update this way, all at once; the AgentAPIs of one machine take
turns on their own. It asks first, and says how many will start a new session. Then the card
says how many it asked, which agents it can't update, and why.

Each machine needs:

- **To allow it.** Updates from the hub are off by default. Turn them on once per machine: add
  `allow-update: true` to `~/.agentapi/config.yaml`, or join with
  `agentapi hub join … --allow-update`. AgentAPIs read it when they start, so restart them once
  (`agentapi restart --all`). Until then, the menu item says updates are off there, and its dialog
  shows the line to add.
- **Write access to its binary's directory.** The AgentAPI replaces its own binary (symlinks
  resolved) and never runs `sudo`. A binary in a directory owned by root, such as `/usr/local/bin`,
  blocks it. The menu item then says it needs a one-time setup, and its dialog shows the command:
  it moves the binary to your `~/.local/bin` and leaves a symlink in its place. For
  `/usr/local/bin`:

  ```bash
  mkdir -p ~/.local/bin && cp /usr/local/bin/agentapi ~/.local/bin/agentapi \
    && sudo ln -sf ~/.local/bin/agentapi /usr/local/bin/agentapi
  ```

- **A state DB, to keep the whole conversation.** An AgentAPI with a state DB keeps its conversation
  and the agent's session across the restart, and sends its queued messages after it. Without one
  the agent resumes its session (Claude Code, Codex and Pi), and its conversation is read back from
  the session's log, but queued messages and the settings chosen in the chat start fresh; the dialog
  says so. An AgentAPI of an earlier release starts a new session, and the confirmation says so. Add
  `state-db: ~/.agentapi/state/{port}.db` to the config file ([step 3](#3-join-each-machines-agentapis)).

#### Updating by hand

Older AgentAPIs that can't update from the hub show nothing new in the menu; the card says how to
update them. On the machine:

- From 2.1.0 on, AgentAPI restarts in place: `sudo agentapi update --restart` updates the binary
  and restarts the machine's AgentAPIs into it (without `sudo` when the binary is yours).
- To let the hub update them from then on, add `--allow-update` (from 3.8.0), which writes
  `allow-update: true` into the config file first. With an older `agentapi`, add that line to
  `~/.agentapi/config.yaml` yourself, then run `sudo agentapi update --restart`.
- Before 2.1.0, run `sudo agentapi update`, then restart each AgentAPI once, or its workspace.

### The list

![Every state the list shows, below two pinned agents](screenshots/hub-list.png)

Each row shows the agent's name (`--name`, by default its working directory's name) and AgentAPI
version ([Versions](#versions)), what it waits for or does, how long ago, the agent and the machine;
a queue that isn't empty, what the agent spent and how long it worked in the last 24 hours
([Spend](#spend), [Work time](#work-time)) and a usage limit above 60% join them. An AgentAPI
update under way, or one that failed, gets a line of its own
([Updating from the hub](#updating-from-the-hub)). [Pinned](#pinning) agents come first, in your
order. Within a [group](#groups), the rows sort by state in this order, then by name;
[Recent changes](#recent-changes) puts the agents with tasks first and sorts by the latest change
instead; [By machine](#by-machine) puts each machine's agents together in either. Each state has a
shape of its own, so it reads without its color:

| Glyph | Second line starts with | Meaning |
| --- | --- | --- |
| Amber ! | Approval, Question, Plan | A request waits for your answer; the line shows it |
| Amber ! | Login | The agent can't work until you log in on its machine |
| Amber ! | Usage limit | A usage limit stopped its turn; queued messages wait for the reset |
| Turning blue arc | Working | The agent works on the task shown |
| Turning blue arc | Retrying | Its turn failed on an API error (a 502, say), and AgentAPI tries it again by itself; the line shows the status and which try |
| Green check | Finished | A task ended since anyone last looked at this agent |
| Green ring with a check | (its title) | Idle, finished in the last 30 minutes (seen) |
| Grey ring | (its title) | Idle |
| Struck-through bell, grey | Muted | It needs you, and you muted that until it changes ([Muting](#muting)) |
| Dashed ring, turning | Connecting | The hub is opening its summary stream, or opening it again |
| Broken red ring | Unreachable | The hub hasn't reached it for 10 s; the line says why |
| Square | Stopped, Exited | The AgentAPI stopped (or missed three heartbeats while unreachable), or its agent exited and AgentAPI gave up starting it again |

Search (**/**) matches any word of a name, group, title, machine, agent or version. The chips under
it show all agents or only those that need you, work, finished unseen, or are offline. Once the hub
watches more than one kind of agent, a second row picks agents, with a chip for each kind it
watches: Claude Code, Codex, Pi, and **Other** for the rest (an AgentAPI 2's agent such as aider,
`native-demo`, or one that hasn't reported its agent yet). Pick several to see any of them; pick one
again to take it out, or **Any agent** to clear them all. An agent shows when it matches the search,
the state and one of the agents picked. Each chip counts what the other row lets through, so its number is what picking it
shows; the search doesn't change the counts. **Show all** on an empty list clears all three. Each
browser remembers the agents picked.

![The list filtered to Claude Code and Codex agents that need you](screenshots/hub-agent-filter.png)

The header says whether the hub's stream is live and how many agents there are; the tab's title and
icon count the ones that need you, so a pinned tab shows it. The stream reconnects as the chat's
does, and a stream silent for 45 s (the hub sends a heartbeat every 15 s) counts as lost.

"Finished" follows the agent's unread mark, which every client of that AgentAPI shares: opening it
in the hub, or in its own chat, marks it read everywhere.

The hub works out each row's state. A page older than the hub, kept open across an update, shows
a state it doesn't know as Unknown, with a question mark; reload the page.

### Groups

![A row's menu on a phone: Pin to top, its group, a new one, and Forget](screenshots/hub-phone-menu.png)

Sort the instances into groups of your own, by client, project or machine. The **⋯** at the end of
each row moves the instance to a group, to a **New group…**, or to **No group**. The same menu pins
it ([Pinning](#pinning)), mutes it ([Muting](#muting)), updates its AgentAPI
([Updating from the hub](#updating-from-the-hub)) and forgets a stopped one
([Adding and forgetting instances](#adding-and-forgetting-instances)). Groups are kept on the hub,
so the phone and the desktop show the same ones, and an instance's own events never move it. A
change the hub refuses or doesn't get (a group, a pin, a mute, a forget) changes nothing and says
why in a notice.

Once there is a group, the list gets a header per group, by name, with the instances in no group
last under **Ungrouped**, in [By machine](#by-machine) too; [Recent changes](#recent-changes) shows no
group headers. A header shows
how many instances the group has and, in amber, how many need you, so a folded group still tells
you. Tap a header to fold or unfold it; each browser remembers which groups it folded. The pencil
renames the group, moving all its instances. A group name has at most 40 characters, and a group
with no instance left disappears.

### Recent changes

![The list by the agents' latest changes: the pinned agents, then one list without group headers, the most recent first](screenshots/hub-recent.png)

The switch beside the search sets the list's order. **Groups**, the default, is the order above:
what needs you first within each group. The [By machine](#by-machine) toggle beside it puts each
machine's agents together, in either order. **Recent changes** is one list below the
[pinned](#pinning) agents, whatever the group: first the agents that have had a task (one runs,
finished, or ran before), then those with no task yet and the muted ones, each part with the most
recent change first, so an unused agent that just reconnected doesn't push the others down. Each row
then shows how long ago it last changed and, where there is room, its group. The filters and the
search apply in every order, and `j` and `k` follow the rows as they show. Rows move as the agents
change; the open one stays selected. Each browser remembers its order.

These count as changes: a new state (it needs you, works, finishes, waits, stops, or the hub loses
or finds it), a new task, something new to wait for (a request, a login, a usage limit), a new
title, a change in the queue, an update under way and a new version. These don't: heartbeats, what
people do (a group, a pin, a mute, reading a finished task), the spend, work time and usage
figures, the agent's messages while it works, and a summary stream that drops and comes back
within 10 s.

The hub records when each instance last changed (`changed_at`) and keeps it in its file, so
restarting the hub doesn't reorder the list. For a new instance, that is when the hub first saw it.
A hub updated from an earlier version has no record yet: until an instance changes, the list places
it by the time its row shows, else by when the hub last heard of it.

### By machine

![By machine in the groups: the pinned agents, then each group with a card per machine, a tab per agent and the picked one below](screenshots/hub-machines.png)

A machine often runs two or three agents (Claude Code, Codex, Pi) in the same directory, so their
rows look alike. **By machine**, the toggle beside the order's switch, puts each machine's agents
together in one card, in either order; each browser remembers it. The card's first line is the
machine's host name alone, wrapped rather than cut; the start most machines' names share (such as
`coder-<user>-`) is dimmed, so the rest tells them apart.

Below the name, each agent has a tab with its mark and a dot in its state's color: amber when it
needs you, blue while it works, green when it finished unseen, grey when muted, stopped or idle, red
when unreachable. An idle agent's dot is a green ring for 30 minutes after it finished, seen or not,
so the agents that just ran stand out, and a grey ring until it has run (a task, or work the hub saw
this week), so a tab says its state without its label. The picked tab also says the agent's short name (Claude, Codex, Pi);
hover over the others for theirs. Tabs keep their order, by agent, whatever their states. Beside
them is what the agents share: their AgentAPI version and what they spent in the last 24 hours. A
card with one agent shown (the only one, or the only one a filter lets through) has a label instead
of a tab, and its spend only once, below. Under the tabs, the card shows one agent: what it waits
for or does, how long ago, and its spend, work time, queue or usage limit, with its directory's name
when the host name doesn't contain it and its version when the card's agents don't share one. Click it to open the agent; its **⋯** menu is a list row's: pin, mute, group, update,
forget. A click on a tab opens that agent, as a row would.

In **Groups**, the groups stay, and within one the machine whose agent needs you the most comes
first, then by name. A machine whose agents are in different groups has a card in each, with that
group's agents. The card shows the agent open beside the list when it is one of its own; otherwise
the tab picked last on it (the browser keeps it across reloads), else the agent that needs you the
most.

![By machine in the recent order: a card per machine, the machine of the latest change first](screenshots/hub-machines-recent.png)

In **Recent changes**, there are no group headers: one card per machine, the machine whose agent
changed last first, in the order of the [recent](#recent-changes) rows. The card's header says how
long ago that change was. The card shows the agent open beside the list when it is one of its own;
otherwise the tab picked on it, as long as you picked it after the card's latest change; else the
agent that changed last. So a pick stays until something on the machine changes, and then the card
shows what changed. The panel names the agent's group and how long ago it last changed, as a
recent row does. Picks in Recent changes are separate from those in Groups.

`j` and `k` step from card to card, to the agent the card shows, and ← and → show the card's other
agents there without opening them; `Enter` opens the one shown. On the tabs themselves, ← and →
(and `Home`, `End`) move between them, and `Enter` opens one.

In both orders, [pinned](#pinning) agents keep their rows on top, and an agent that doesn't report
its machine keeps a row of its own. The filters and the search apply to each agent: a card has tabs
for the ones that match and disappears when none does, and the search matches the machine's name
too. A machine with one agent has one tab. The section headers count agents, not machines. With
**By machine** off, both orders show a row per agent.

### Pinning

**Pin to top** in a row's **⋯** menu keeps an agent at the top of the list, under **Pinned**, in
every order. A new pin goes last. **Move up** and **Move down** in the same menu move it past the
next pinned agent the list shows, and **Unpin** takes it out. `p` pins or unpins the row with the
focus.

A pinned agent shows only there, not again in its group, its machine's card or below in the recent
list; in [By machine](#by-machine) it keeps a row of its own too. The row has a pin after the name and,
where there is room, names its group. The section's header counts its
agents and those that need you, and folds like a group's. Below it, the rest of the list gets
headers too: the groups and **Ungrouped**, or **Recent changes**. The filters and the search apply
to pinned agents as well: one they leave out is hidden, and the section disappears once none is
left.

Pins are kept on the hub, like groups, so every browser shows the same ones in the same order.
Pinning, unpinning and moving an agent aren't changes for [Recent changes](#recent-changes).
Forgetting an agent drops its pin.

### Muting

![A muted row: what it waits for, out of the way](screenshots/hub-muted.png)

An agent may wait for something you won't do soon, such as a login on a machine you don't use it
on. **Mute until it changes** in its **⋯** menu takes it out of what needs you: it stops counting
in **Need you**, the group's badge, the tab and the overview, and sends no notification. The row
stays, grey, with a struck-through bell and what it waits for. The mute lasts as long as that
does: once the agent logs in, or asks something else (another request, a usage limit), it counts
again. **Unmute** in the same menu ends it sooner. Mutes are kept on the hub, like groups.

### Opening an instance

![An agent open beside the list, its approval ready to answer, its machine's agents in the bar](screenshots/hub-instance.png)

Pick a row or a card to open the agent's chat beside the list: everything the chat does works here,
answering requests included. The bar above it shows the agent's state, agent, machine and AgentAPI
version, with the chat's own search, terminal and Explorer buttons. When the machine runs other
agents the hub watches, the bar has a button for each of them too, in any list order: their marks
with the same state dots as the [By machine](#by-machine) tabs, the open one marked. A click opens
another in its place, so you switch between Claude Code and Codex on one machine without the list.
The hub keeps the last few agents you opened loaded (four, two on a phone), so going back to one
shows it at once instead of loading its chat again.
**New tab** opens the chat on its own, and **×** goes back to the overview, past Issues too when you
opened the agent from there; **Esc** steps back one level (from an agent opened from Issues, to
Issues). The hub's mark at the top of the list also goes back to the overview from anywhere. Each
agent you open is a page in the browser's history, so Back returns to the agent before it, and from
the first to the list or the overview; **×** returns to the overview at once, past them all. The URL keeps the agent (`#<id>`), so a reload or a bookmark
comes back to it. An AgentAPI update under way shows in the bar too, with **Cancel** while it waits
for the task ([Updating from the hub](#updating-from-the-hub)).

### Favorites

![Hub settings: the favorites, each with its buttons](screenshots/hub-settings-favorites.png)

Favorites are prompts you send often, kept on the hub. The gear in the header opens **Hub
settings** at its **Favorites** tab (the others are [Providers](#model-providers) and
[GitHub](#github-settings)), which lists them, and adds, edits, deletes and moves them up and
down. Each has a prompt of up to 20,000 characters and an optional title of up to 100; without a
title, its first line names it. The hub keeps up to 100. A change is saved at once, and every page
signed in to the hub shows it, so your phone and desktop have the same favorites. **Undo** brings
back the last one deleted. In the editor, ⌘/Ctrl+Enter saves and **Esc** cancels.

![The ☆ menu in the composer of an agent opened from the hub](screenshots/composer-favorites.png)

In the chat of an agent you open here, **☆** in the composer lists the favorites, each with its
text on one line. Picking one inserts it in the message without sending it: an empty message
becomes the favorite, and otherwise the favorite is a paragraph of its own at the end, with the
cursor after it. Ctrl/⌘+Z takes it back out. Past seven favorites, a search box filters the list,
and typing in the list types in it. **Manage favorites…** opens the hub's settings at the
favorites. The menu shows even before the first favorite, with that link. A chat opened on its own,
in a new tab or at its AgentAPI's URL, has no ☆.

### Model providers

![Hub settings: the Providers tab, with two providers, their endpoints, models and keys](screenshots/hub-settings-providers.png)

The hub keeps [model providers](providers.md) for every machine that reports to it:
endpoints such as a LiteLLM gateway that Claude Code and Codex sessions can switch to. The
**Providers** tab of **Hub settings** lists them, and adds, edits and deletes them. Each has an ID
(lowercase letters, digits, `-` and `_`), an optional name, a base URL, an optional Anthropic base
URL, an API key and the models it offers; without models, each AgentAPI asks the
provider's `<base_url>/models`. The hub keeps up to 50. The key is write-only: the page shows
whether a provider has one, and a key left empty in the editor keeps it only while the provider ID and
endpoints stay the same. Enter the key again after changing either endpoint; **Remove the key**
removes it. An ID can't change once added. Saving includes the list's revision, so another tab that
saved since you opened yours gets a conflict rather than silently losing either tab's changes.

![The editor after Check connection: the provider answered with six models, two of them picked](screenshots/hub-settings-provider-check.png)

**Check connection** in the editor asks the provider for its models from the hub, with the key typed
or, at an unchanged endpoint, the one the hub keeps. It says how long it took and how many it listed,
or why it failed (a refused key, an endpoint changed without its key, a URL without the API, a provider
the hub can't reach). A result applies only to the fields it checked, not to edits made while it ran.
Tap the models to offer only those; none picked offers all of them.

Each AgentAPI that reports to the hub asks it for the providers when it starts and every minute
after, in a request signed with the hub's secret. The answer has their keys, encrypted with a key
derived from the secret (AES-256-GCM), so they don't cross the network in the clear over plain
HTTP. A separate proof binds that answer to the request's nonce, so an observer can't replay an old
provider list. New AgentAPIs refuse an unbound answer; upgrade the hub before the machines reporting
to it. A change reaches the chats within a minute. On a machine, a provider of its own config file
wins over the hub's with the same ID.

Once some agent reports its providers, each provider in the tab has a line of what the agents used
it for: its tokens and their cost (or **no price**) in the last 24 hours and 7 days, counted as the
[Providers card](#provider-usage) counts them, or **No calls in the last 7 days**. A provider the
agents called in the last hour has a second line of how it answered them, through each AgentAPI's
[provider proxy](providers.md): the calls, the share that failed, the median time to the first byte
and the last error with when, added up over the instances the hub follows (`provider_health` in
their summaries). It turns amber while the provider fails (the latest call failed, or half of the
hour's); hover for the full figures.

The hub prices its providers' models at OpenRouter's list prices
([Prices](providers.md#prices)), and each AgentAPI prices the calls to them with those. It gets
OpenRouter's public list (no key) once a day while it has providers, and on **Refresh prices** at the
top of the tab; it keeps the last list it got in `openrouter.json`, so it goes on pricing
offline. A refresh also has each provider that defines no models list them, with its key. Under each
provider, **Prices** folds out its models: each one's price per million tokens (input / output; hover
for the cache's) and the OpenRouter model it comes from, **auto** when matched by name, **picked**
when chosen. **Change** opens a picker that searches the hub's copy of the list, starting from the
model's name, with **No price** and **Automatic** (back to the match by name). A pick saves the list
as the editor does, with its revision. A line under the list says how old the prices are, and why the
last refresh failed, if it did; the hub then keeps the prices it had.

Every machine that joined the hub gets the keys: anyone with the hub's secret can. Give the hub only
keys you would give each of those machines, and each agent that runs on the provider.

### GitHub settings

![Hub settings: the hub's GitHub App, where it is installed and on which repositories](screenshots/hub-settings-github.png)

The **GitHub** tab of **Hub settings** shows the hub's GitHub App. The app lists the
[Issues](#issues), and the jobs push branches and open pull requests as the app. On the Issues page,
the app's name beside **Issues**, and **GitHub settings…** at the foot of the repository menu, open
it too. It has the app's name, linked to its page on GitHub, and the account or organization that
owns it. Below, each account the app is installed on has its picture, **All repositories** or how
many were selected, and their first names. The section and the Issues page read the app again when
the hub's page shows again, so coming back from GitHub is enough to see a change.

- **Choose repositories** opens GitHub in a new tab, where you install the app on another account
  or pick the repositories of one it is on. **Change** beside an account goes straight to its
  installation.
- **App settings on GitHub** opens the app's settings: its name, permissions, and **Delete GitHub
  App**. A hub that registered its app before 3.14 knows whose settings they are under only once
  GitHub sends you back from an installation (after **Choose repositories** and **Save**); until
  then the link opens the app's public page.
- **Reconnect…** registers a new app, for your account or an organization (the current one when
  the app is an organization's), from the hub's `/github/connect`. The hub keeps its current app
  until GitHub hands the new one back, so leaving the flow on GitHub changes nothing. Then install
  the new app; until you do, jobs can't get repository tokens and Issues lists nothing. The old
  app stays on GitHub: delete it in its settings.
- **Disconnect…** asks first, then the hub forgets its app (`DELETE /api/github`): its key and
  where it is installed. The jobs at work can't get new repository tokens, so their pushes and pull
  requests fail once the token they have expires, within the hour. Issues needs GitHub connected
  again. The app stays on GitHub, installed where it was, until you delete it there.

Without an app, the tab has the same **Connect GitHub** as Issues, for your account or an
organization.

### Issues

![Issues: the board by stage, what needs you first](screenshots/dispatch-issues.png)

**Issues** hands GitHub issues to agents on your machines, through the hub's
[issue dispatch](specs/issue-dispatch.zh-TW.md) (in Traditional Chinese; the
[flow](specs/issue-dispatch-flow.zh-TW.md) has the pictures). ◎ in the header, or `i`, opens it in
place of the overview; the badge on ◎ counts the issues that need you: a failed job, a pull
request in review, an agent that waits on you. ◎ again, `Esc` or the back arrow on a phone closes
it.

The page puts the issues of the repositories it shows on one board. The headline says what needs
you, and the line under it counts the stages. The bar under them shows how many issues are at
each stage, To do, In progress, In review and Done, in proportion; In progress also says how many
of its issues wait on you and how many failed. Below are the machines whose launchers report:
online or not, their jobs against their room (●●●○), and the agents each runs. An offline machine
has **×**, which forgets it after a dialog: the hub drops the machine and the launcher token it
pinned its name to, so a launcher set up again, with a new token, can join under the name. The
button is off while one of its jobs still works (all but done, stopped and failed); stop them
first. Nothing changes on the machine, and a launcher that reports again shows up again. Then the
issues, by section:

- **Needs you**: a failed job, then an agent that waits on you (an approval, a question), then a
  pull request to review.
- **Working**: the agents at work, the longest first, each with what it does now.
- **To do**: the open issues no agent has.
- **Done**: those done or stopped, the latest first; **Show all** past three.

A card shows the issue's number, state and title, the agent and machine on it, its pull request,
what its runs cost and how many there were, and the one thing its state asks for: **Assign**,
**Answer**, **Review #21**, **Retry**. When a card changes state, it moves to its new section. The
page gets the machines and jobs from the hub's stream as they change. What you do on the page
shows at once, as the hub answers it; the stream takes over from there.

The repository menu at the top picks the board's scope. **Recent repositories** shows the open
issues of the 20 repositories the hub's GitHub App can see that were pushed to or changed last, and the
jobs of all of them. Pick one repository to see only its issues; past eight, a search box finds
one. The page keeps the choice.

#### Assigning an issue

![The agents and machines an issue can go to](screenshots/dispatch-assign.png)

**Assign** gives the issue to the agent and machine the button shows. That is the machine the hub
would pick (one that has the repository, then the least busy one) with its default agent, or the
agent you picked by hand last, on a machine that runs it. The arrow beside it lists every agent with
the machines that can take the issue, and greys out the others with the reason: offline, full,
doesn't run that agent, or doesn't have the repository and doesn't clone. A machine you pick there
gets the issue. `a` assigns the issue whose card has the focus. Without a machine that can take it,
the button says why.

The issue is **Starting** on that machine at once: the hub holds its place there, and the machine
clones or fetches the repository and starts the agent after, which for a big repository can take
minutes. The card then shows the agent working, or the job failed with why. A machine that turns out
to be full hands the issue to another the hub would pick, unless you picked it by hand.

#### An issue's detail

| An issue in review | A failed job |
| --- | --- |
| ![The detail of an issue in review, with the messages it was sent](screenshots/dispatch-review.png) | ![A failed job: why, its log, and Retry](screenshots/dispatch-failed.png) |

A card opens the issue's detail, beside the board on a wide screen and over it on a narrow one. Its
top box says the issue's state in a sentence, with what its agent does or asks now, and the actions:
**Retry with …**, **Answer in the chat**, **Review #…**, **Open chat**; then **Switch agent**,
which keeps the branch, the worktree and the pull request, and **Stop**, which deletes the branch.
Both ask first:

| Switch agent | Stop |
| --- | --- |
| ![Switch asks first](screenshots/dispatch-confirm-switch.png) | ![Stop asks first](screenshots/dispatch-confirm-stop.png) |

Below that are the job's spend, runs, agent time and tokens, and its timeline, the latest first:
each run (the agent and machine, when it ran, what it cost, how it ended), and each message its
agents were sent, from this page or from a review or `/agent` comment on GitHub. The hub keeps the
last 50 messages of each job. **On the machine** has the branch and the worktree to copy. While
the agent works, the box at the bottom sends it a message (⌘/Ctrl+Enter).

A run counts the task events of its own AgentAPI process only (the `server_id` the launcher reported
for it), so a late event from an earlier run can't move or charge the current one. A launcher older
than this hub reports no `server_id`: the hub then ignores its jobs' task events, which leaves their
state and cost where they were. Upgrade the launchers along with the hub; an upgraded launcher reports
the identity of the jobs it takes over on its next report.

The hub also needs to read that AgentAPI itself: it takes a task event only while it reads the
instance the event names, as that run, at an address of the job's machine (the host of its launcher's
URL, or a name with the same address). An event that comes before the hub reaches the instance, such
as just after it started, is dropped, and the cost comment then says the hub may have missed some. An
event for an instance the hub reads on another machine changes nothing; the hub logs "Refused a task
event of an instance the hub doesn't reach on its job's machine". A launcher and its AgentAPIs that
name the machine differently in their `public-url` (an address and a name with another address) hit
this too. The events are still signed with the secret every machine has, so this doesn't stop a
machine that copies the whole identity of the job's AgentAPI ([Tokens and security](#tokens-and-security)).

#### The list's rows

![The agents of issues in the list, each with its issue](screenshots/dispatch-list.png)

The row of an agent that works on an issue has a chip under it: the issue's stage and its pull
request, or the issue itself where the row isn't named after it. The chip opens the issue in
Issues.

#### New issue, and a hub without GitHub

**New issue** (or `n`) opens an issue on GitHub as the hub's app, in the repository picked, and with
**Assign to** on, hands it to an agent at once (⌘/Ctrl+Enter).

![Issues before GitHub is connected](screenshots/dispatch-no-github.png)

Before the hub has a GitHub App, Issues shows **Connect GitHub**, which registers one from a
manifest, for your account or an organization. [GitHub settings](#github-settings) changes the
app's repositories, connects another app, or disconnects it. An issue can still be started by its reference
(`owner/repo#12`, or its URL); the machine then works with its own GitHub login. The machines show
above it, with **×** on an offline one, as on the board. Without a launcher on any machine, the page
shows the command that installs one. A machine that joined the hub needs no launcher for its
AgentAPIs to show in the list; it needs one only to take issues
([`agentapi host`](cli.md#agentapi-host), which also covers machines without systemd).

| The board on a phone | An issue on a phone |
| --- | --- |
| ![Issues on a phone](screenshots/dispatch-phone.png) | ![An issue's detail on a phone](screenshots/dispatch-phone-detail.png) |

### Keys and notifications

| Key | Does |
| --- | --- |
| `/` | Search |
| `i` | Open [Issues](#issues) |
| `j`, `k` (or the arrows) | Move between the rows ([By machine](#by-machine): the cards) |
| ←, → | [By machine](#by-machine): show a card's other agents |
| `Enter` | Open the row |
| `p` | Pin or unpin the row ([Pinning](#pinning)) |
| `Esc` | Clear the search, or go back: from an agent opened from Issues to Issues, then to the overview |

On a desktop, drag the list's edge, or focus it and press ← or →, to change the list's width; each
browser remembers it, and a double-click on the edge resets it. When the list is narrower than
the default, its first row keeps the hub's mark and leaves out the name, so its buttons fit.

`/`, `i` and `Esc` work anywhere on the hub's page but in a field; `j`, `k`, the arrows and `p` work with the focus in
the list or on nothing, so elsewhere the arrows scroll the overview as usual. In Issues, `j` and `k` move between the
cards, `Enter` opens one, `a` assigns it, `c` opens its chat, `n` opens a new issue and `r` reads
the issues again. Inside an open chat, keys go to the chat. The bell's menu turns on the alerts for an agent
that starts needing you: **Notifications**, which the browser shows while the hub's tab is in the
background (on an https page only), and **Sound**, a short chime while you look away from the
page (its tab in the background, or another window in front), which works over plain http too. The
browser keeps both choices. A screen reader hears the same as it happens.

### On a phone

| The list | An instance |
| --- | --- |
| ![The hub's list on a phone](screenshots/hub-phone.png) | ![An instance open on a phone](screenshots/hub-phone-chat.png) |

On a narrow screen the list and the chat take turns; the back arrow at the top, or the phone's
back gesture, goes back to the list. The chat keeps the screen for the conversation: one bar above
it, with only the marks of the machine's other agents, and the composer one row, with ☆ for the
[favorites](#favorites) beside ⋯. Answering an approval from the phone:

![Answering an approval from the hub on a phone](screenshots/demo-hub.gif)

### Connecting the first agents

![The guide an empty hub shows](screenshots/hub-empty.png)

A hub no agent has joined yet shows how to join a machine, as the
[Add dialog](#joining-a-machine-from-the-page) does, and then to restart the agents once. An
AgentAPI's own chat has steps for its machine too: **Add to an AgentAPI hub…** in the menu of the
status in its header ([Session details](chat-ui.md#session-details-and-alerts)).

### Joining a machine from the page

![The Add dialog: the hub's address, the join command and a prompt for an agent, the secret masked](screenshots/hub-add.png)

**+** opens the Add dialog. Its **Join a machine** section has
[step 3](#3-join-each-machines-agentapis) ready to copy:

- **Hub address** lists where machines reach the hub: the `--bind` address, or each address of the
  hub's machine with its interface, as the startup log has them. Pick the one on the network the
  machine shares with the hub. A hub that found none offers the page's own address, which works only
  without a sign-in page in front of it, unlike a Coder port URL.
- **Allow updates from the hub** adds `--allow-update` ([Updating from the hub](#updating-from-the-hub)).
- **Run on the machine** is the `agentapi hub join` command, with the secret in
  `AGENTAPI_HUB_SECRET` and `--public-url 'http://{ip}:{port}'`: `{ip}` follows the machine's
  address, so there is nothing to fill in.
- **Prompt for an agent on that machine** has an agent there join it: the hub's URL and the others
  to try, the secret, `--public-url` with `{ip}` as it is, and the steps: the version, a check that
  the hub answers, `join` and `agentapi hub status`. The agent restarts no AgentAPI and never shows
  the secret. With updates allowed, it explains the one-time setup `join` may print and suggests a
  state DB, asking you before either. An agent with the
  [agentapi-hub-join skill](../skills/agentapi-hub-join/SKILL.md) follows the skill; one without
  follows the steps.

The secret shows masked until **Show the secret**; the copy buttons copy it as it is. Only a hub with
a token shows its secret, to whoever signs in. A hub with `--no-token` never does, since everyone
who reaches it would see it: the command has `<hub secret>` in its place, and the prompt has the
agent ask you for it.

### Adding and forgetting instances

**Add one by its URL**, at the bottom of the Add dialog, adds an AgentAPI that can't run
`agentapi hub join` (an older version, or one you don't log in to). The hub asks it for its summary
first, so it must be reachable and run AgentAPI 1.1 or later. A token given here is kept for that
instance and sent instead of `--instance-token`. An instance added this way doesn't report starting
or stopping; the hub just finds it reachable or not.

The **⋯** menu of a stopped or unreachable row has **Forget**, which removes it from the hub. An
instance that joined with its webhook comes back the next time it starts.

To forget every stopped agent at once, such as the test AgentAPIs a run left behind, use **Forget N
stopped** under the overview's Offline tile, or, on a phone, pick the Offline filter: the top of the
list says how many of the agents it shows stopped, with **Forget all**. A dialog lists them by name
and machine before the hub forgets them. Only stopped agents go; unreachable ones stay, since they
may come back on their own, and one that started again meanwhile is kept. If the agent open on the
right is forgotten, it closes.

## Tokens and security

| Value | Who holds it | What it does |
| --- | --- | --- |
| Hub token (`--token`) | You, and the hub | Signs a browser in to the hub |
| Hub secret (`--secret`) | The hub, and each machine's config file | Signs the AgentAPIs' events, so only your machines can add instances; gets the [model providers](#model-providers) and their keys |
| Instance token (`--instance-token`, or one per added instance) | The hub, and the AgentAPIs | Lets the hub call the AgentAPIs; browsers never get it |

- The hub takes an event only with a valid `X-AgentAPI-Signature-256` (HMAC-SHA256 of
  `<timestamp>.<body>`), a timestamp at most 10 minutes off, and a delivery ID (`id`) it hasn't
  taken yet. AgentAPI always sends one; an event without it is refused, since a copy of it would
  work again for those 10 minutes. This matters because an event names the URL the hub sends an
  instance token to. The hub keeps the IDs for as long as their event's signature holds, in
  `instances.deliveries.json`, so a copy is refused after a restart too. It reads an event for at
  most 10 seconds, and 1 MiB. The other requests signed with the secret instead of a session, an
  AgentAPI's provider sync and a launcher's token request, get the same 10 seconds to send their
  body.
- A signed event can add an instance the hub doesn't have, and move one whose URL is dead to the
  hub (unreachable past the 10 s grace, stopped after missed heartbeats, or another AgentAPI
  answers there) to the event's URL. It never moves an instance the hub is connected or connecting
  to, whatever run it names, or one added by hand. An `instance.stopped` is no sign the URL is
  dead: the hub tries the old URL first. An `instance.*` event must name its run (`server_id`,
  which AgentAPI 1.0 and later send). An instance that moves leaves the token given for its old URL
  behind; `--instance-token` still applies.
- Every joined machine has the same secret, in a file any agent there can read (one in the bypass
  permission mode reads files without asking). Such an agent, steered by something it read, can
  still announce, at an address of its own, a new instance ID, or one whose address stopped
  answering (such as while its machine restarts). The hub then sends that address
  `--instance-token`, and once it answers as that instance, the messages and approvals you send
  it. Join only machines you would give the instance token, and add an instance by hand if
  it must never move. A key per machine would close this; the hub doesn't have one yet.
- A job's task events move its state and add to its cost. The hub takes them only from the job's
  AgentAPI as it reads it on the job's machine ([An issue's detail](#an-issues-detail)), so a
  machine that announces a job's instance ID at its own address can't move or charge the job. One that
  sends events with the instance ID and run (`server_id`) of the job's AgentAPI, both in its
  summary, still can: there too the secret is the boundary.
- A launcher's events name the URL the hub calls it at with that launcher's token, and the hub
  calls it by itself to stop a job no start asked for. So the hub calls only another machine's
  address there, checked at every connection, directly and without following redirects: never its
  own loopback (unless `--allow-loopback-launchers`), a link-local address such as a cloud's
  metadata, or `0.0.0.0` ([`agentapi host`](#agentapi-host)). Any LAN address stays reachable, so
  a machine with the secret can still have the hub send requests (`POST /jobs`, `DELETE
  /jobs/<key>`) to another machine on the network.
- The hub forwards an instance's requests (the chat, approvals, updates) only while it is
  connected: it reads that instance's summary at its URL. While it connects, after the hub
  restarts, a stream drops or an event moved the instance, the proxy answers 503 and sends
  nothing.
- The session cookie is HttpOnly and SameSite=Lax, and holds the time it was issued, signed with
  the hub token (HMAC), rather than the token. The hub refuses one older than 30 days, whatever the
  browser keeps. Signing out only clears the browser's copy: a cookie copied elsewhere still works
  until it expires or `--token` changes. The proxy drops the browser's cookies and its `Authorization`, `Origin`, `Referer` and
  `Sec-Fetch-*` headers, and drops the instance's `Set-Cookie`, so neither side's credentials
  reach the other.
- A page of another site can't make the hub change anything or send an instance a task, with or
  without a token: the hub refuses requests that change something unless the browser says they
  come from the hub's own page.
- Nothing an instance answers runs as a page of the hub's (`Content-Security-Policy: sandbox`),
  so a workspace someone took over can't reach the others through your browser. Through the hub,
  an instance's webhook can be read but not changed: it names where the hub sends the token.
- Adding a URL never moves an instance the hub has; forget it first.
- `~/.agentapi/hub/instances.json` and its `.bak` hold the tokens of instances added by hand; they
  are created readable by their owner only. `~/.agentapi/config.yaml` holds the hub secret;
  `agentapi hub join` writes it the same way.
- Signing in sends the hub token. Serve the hub only on the VPN, or behind HTTPS.
- Anyone signed in to the hub can do what the AgentAPIs allow: send tasks, answer approvals, use
  their terminals, update those whose machine allows it. Give the hub token only to people you
  would give the instance token.
- Anyone signed in also gets the hub secret, which joins a machine (`GET /api/join`, for the
  [Add dialog](#joining-a-machine-from-the-page)). A hub with `--no-token` never sends it, since
  everyone who reaches it would get it.
- An AgentAPI the hub asks to update installs only the latest official release, checked against the
  release's `checksums.txt`: the hub can't choose a version or a URL. It never runs `sudo`, and
  does nothing unless its machine allows it (`allow-update: true`).
- With `--no-token`, everyone who reaches the hub's port is signed in: keep it behind a sign-in
  of its own, and never on a public address. Set `--allowed-hosts` to the names you open it by (a
  Coder port URL's host, a VPN name): a site's page can otherwise reach the hub through a name of
  the site's that it points at the hub's address (DNS rebinding). IP addresses and `localhost`
  always work.

  ```bash
  agentapi hub serve --no-token --secret "$AGENTAPI_HUB_SECRET" \
    --allowed-hosts 3290--app--dev--me.coder.example.com
  ```

## Troubleshooting

| What you see | Why | What to do |
| --- | --- | --- |
| An instance never appears | Its AgentAPI started before `join`, or before 1.2 | Restart it, or its workspace |
| `join` says a sign-in page is in front of the hub | The hub's URL is a Coder port URL (or another behind a login) | Join with the hub's address on your network or VPN; the AgentAPIs send events themselves |
| `join` says the hub is on another machine | `--public-url` names `localhost` | Give the address the hub reaches this machine at |
| The Add dialog's command has `<hub secret>` | The hub runs with `--no-token`, so its page never shows the secret | Put in the hub's `--secret` |
| The Add dialog lists no address of the network the machine is on | The hub binds one address (`--bind`), or machines reach it at an address its machine doesn't have (NAT, a port forward) | Use the address the machine reaches the hub at, plus `/hooks` |
| An instance never appears; the hub logs "Refused an event" (at most once a minute for a bad signature, with `refused_since_last_logged`, the events refused in between) | `--secret` of `join` and of the hub differ, or the clocks are more than 10 minutes apart | Run `agentapi hub join` again with the hub's secret; check the clocks |
| An instance never appears; `agentapi hub status` shows no hub | `join` wrote to another config file than the one the AgentAPI reads | Use the same user, or pass the same `--config` to both |
| Unreachable: `400 Bad Request` | The AgentAPI refuses the VPN name as `Host` | Allow it in `--allowed-hosts` (step 3) |
| Unreachable: `needs an API token` | The AgentAPI runs with `--api-token` and the hub has another | Give the hub `--instance-token`, or the token when adding |
| Unreachable: connection refused or timed out | Wrong `--public-url`, or the hub can't reach the VPN | Check the URL from the hub's machine with `curl <url>/summary` |
| Unreachable (`no route to host`, or timed out) after the machine got a new IP address | It joined with a fixed IP in `--public-url`, which DHCP changed, as after a Coder workspace restarts | Run `agentapi hub join` again with `--public-url 'http://{ip}:{port}'`; the hub follows it with its next heartbeat, and after every later change |
| Unreachable: `another AgentAPI answers at this URL` | The instance's address now belongs to another machine (a new DHCP lease), or `--public-url` names another machine | Check `--public-url`; an instance that moved sends its new address with its next heartbeat, within a minute |
| The hub logs "Two AgentAPIs report the same instance ID" | Two machines have the same name, so their AgentAPIs on the same port have the same ID; the hub keeps the one it follows | Rename one machine, or give its AgentAPIs `--instance-id` |
| The hub logs "An instance's event names another URL" (or "… while the hub connects to its URL") | Its `--public-url` changed while the hub follows it, or still connects to it, at the old one | Nothing: the hub keeps the URL that works, and takes the new one once the old one is unreachable |
| An instance whose machine got a new address stays unreachable for a minute or two before it moves | The hub moves it only once the old address failed for 10 s (more when it doesn't answer at all: each try then takes up to 30 s, and a stream that goes silent counts as lost after 45 s), at the instance's next heartbeat, every minute. A hub before 3.0.1 moved it at the first heartbeat after the stream dropped | Nothing: it moves at most a minute later than it did |
| The hub logs "A stopped instance's event names another URL" | An instance that sent `instance.stopped` started at another URL | Nothing: the hub tries the old URL first, and moves it with the next heartbeat once that fails |
| Opening an instance answers `the hub isn't connected to the instance yet` (503) | The hub hasn't read its summary at its URL since the hub started, the stream dropped, or the instance moved | Wait until its row shows it connected |
| The hub logs "An instance added by hand keeps its URL" | It was added by one URL, and its webhook names another | Make `--public-url` the URL it was added by, or forget it and let its webhook add it |
| The hub logs "Refused an instance event that names no server ID" | Something that isn't AgentAPI 1.0 or later sent it, signed with the hub's secret | Update the sender; if you don't know it, find which machine with the secret sends it |
| Unreachable, and the hub's VPN client runs in userspace | The hub's connections don't go through the VPN | Set `ALL_PROXY` to the VPN client's SOCKS5 proxy for the hub |
| "is it AgentAPI 1.1 or later?" when adding | The instance has no summary | Update it with `agentapi update` |
| The page says the hub didn't answer | Wrong address, or the hub isn't running | Open `http://<hub>:3290/api/session`; it answers JSON when the hub runs |
| Behind Coder's port URL, the page asks for Coder first | Coder's port URLs need a Coder login | Sign in to Coder in that browser, then to the hub |
| `the hub doesn't answer to <name>; add it to --allowed-hosts` | The hub runs with `--allowed-hosts` and you opened it by another name | Add the name to `--allowed-hosts` |
| **Update AgentAPI…** says updates are off | The machine doesn't allow updates from the hub | Add `allow-update: true` to `~/.agentapi/config.yaml` there, then run `agentapi restart --all` |
| **Update AgentAPI…** asks for a one-time setup | The AgentAPI can't write its binary's directory, such as a root-owned `/usr/local/bin` | Run the command it shows on that machine, once |
| A row says **Update failed** | Checking or downloading the release failed, or the new version wouldn't start with this AgentAPI's settings, such as an AgentAPI 2 that runs a PTY agent | Hover it, or open **Update AgentAPI…**, for the error; then try again. An AgentAPI 4.3.1 or later then keeps its program. An older one already has the new version on disk: if the error names a setting (`unknown flag: --transport`, say, which AgentAPI 4 removed), drop it from how that AgentAPI starts before it restarts, or it won't start. For an AgentAPI 2 that must keep its agent, put v2.5.1 back on its machine ([Updating from the hub](#updating-from-the-hub)) |
| A connected agent's **⋯** menu has nothing about updating it | Its AgentAPI is too old to update from the hub | Update it by hand ([Updating by hand](#updating-by-hand)) |
| The hub logs "The hub's file of instances doesn't parse" | The file was damaged, such as by an edit by hand, or a hub of 2.2 or earlier started on a newer hub's file | The hub moved it aside (`instances.json.corrupt-<time>`) and started empty. Joined instances come back with their next heartbeat. To get the rest back, stop the hub and copy `instances.json.bak` (the file as the hub's previous start found it) over `instances.json`; for a hub of 2.2 or earlier, see [Files](#files) |
| The hub doesn't start: `instances.json is of a newer hub` | A newer hub wrote the file | Run the newer hub, or start from `instances.json.bak` |
| A machine stays offline in Issues; the hub logs "Refused a host event with another launcher's token than the machine's" | The hub pins a machine's name to the launcher token it first came with. The launcher has a new token (its `~/.agentapi/host.token` was deleted, or the machine was set up again), or another machine with the hub's secret uses the name | If it is your machine: stop its jobs, and once it shows offline, forget it with its **×** in Issues ([Issues](#issues)); it joins with its next heartbeat. If it isn't, change the hub's `--secret` and join your machines again |
| A machine doesn't show in Issues, or stays offline; the hub logs "Refused a host event with a URL the hub doesn't call" | The launcher reports a URL the hub doesn't call a launcher at: on loopback (it joined the hub by `localhost` on the hub's machine), link-local, or with a user and password in it ([`agentapi host`](#agentapi-host)) | Join the machine with the hub's LAN address (`agentapi hub join http://<hub's LAN address>:3290/hooks …`) and restart the launcher, or set the `host` section's `public-url`; for a launcher on the hub's machine, starting the hub with `--allow-loopback-launchers` also works |
| An issue stays **Starting** for minutes | The machine clones or fetches a big repository (up to 10 minutes), then waits up to a minute for the agent | Wait; `~/.agentapi/jobs/<owner>~<repo>~<issue>/agentapi.log` on the machine has the agent's side. A start the hub lost the answer of works or fails with the machine's next heartbeat |

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
| `--allowed-hosts` | any | Names browsers open the hub by, besides IP addresses and `localhost` (comma-separated, `*` for any); instances' events come by any name |
| `--log-level` | `AGENTAPI_HUB_LOG_LEVEL`, else `info` | Least important level to log: `debug`, `info`, `warn` or `error` |
| `--dispatch-token` | `AGENTAPI_HUB_DISPATCH_TOKEN` | Token a client (a GitHub workflow's runner) calls `POST /api/dispatch` with, and nothing else ([Issues](#issues)) |
| `--github-app-file` | `github-app.json` next to `--state` | The hub's GitHub App, which `/github/connect` registers |
| `--allow-loopback-launchers` | `false` | Calls a launcher at a loopback address, such as one on the hub's machine that joined it by `localhost` ([`agentapi host`](#agentapi-host)) |

The hub reaches the instances through the proxy of the environment (`HTTP_PROXY`, `HTTPS_PROXY`,
`ALL_PROXY`). It gives up connecting after 30 seconds (10 for the TLS handshake) and waiting for an
answer's headers after 15; the summary streams and the pages it proxies stay open. It reaches the
launchers directly, without the proxy, and follows no redirect of theirs.

### `agentapi host`

[Issues](#issues) starts its jobs through a launcher on each machine, `agentapi host`, which reports
to the hub with the machine's webhook settings (`agentapi hub join`) and starts an AgentAPI for each
issue the hub dispatches there. On each machine:

```sh
agentapi hub join http://<hub>:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
agentapi host install --now  # a systemd user service; agentapi host serve runs it in the foreground
agentapi host status
```

The launcher's settings (the `host` section of `~/.agentapi/config.yaml`), its files and
`agentapi host uninstall` are in the [CLI reference](cli.md#agentapi-host);
[examples/issue-dispatch](../examples/issue-dispatch/README.md) sets up the hub's dispatch token,
GitHub App and workflow too. The hub pins each machine's name to the launcher token
(`~/.agentapi/host.token`) it first came with: a machine set up again, or whose token file was
deleted, joins under its name only after the hub forgets it: **×** on the machine in [Issues](#issues),
or `DELETE /api/hosts/{name}` ([Troubleshooting](#troubleshooting)).

The hub calls a launcher at the URL its events name (`public-url` in the `host` section, by
default `http://{ip}:3291`), so it takes only an http or https URL without a user or password in it,
and connects only to an address of another machine: private LAN and VPN addresses are fine; its own
machine's loopback (`127.0.0.1`, `::1`), link-local addresses (`169.254.0.0/16`, `fe80::/10`, such as
a cloud's metadata at `169.254.169.254`) and `0.0.0.0` aren't. It checks the address a name resolves
to at every connection. A launcher on the hub's machine joins with the hub's LAN address, as the
startup log has it, and then reports that address. One that joined by `localhost` reports
`127.0.0.1`, which the hub refuses unless it was started with `--allow-loopback-launchers`.

### `agentapi hub join`, `leave`, `status`

| Flag | Default | Description |
| --- | --- | --- |
| `--secret` | `AGENTAPI_HUB_SECRET` | Secret that signs the events; without it the hub refuses them |
| `--public-url` | | Where the hub reaches the AgentAPIs, such as `http://{ip}:{port}`; `{port}` and `{hostname}` are filled in, and `{ip}` with the machine's IPv4 address on the route to the hub, which each AgentAPI looks up again while it runs |
| `--events` | `instance.*` | Events sent to the hub; it needs only these |
| `--api-token` | `AGENTAPI_API_TOKEN` | Token for running AgentAPIs that need one |
| `--config` | `~/.agentapi/config.yaml` | Config file to write |
| `--registry-dir` | `~/.agentapi/instances` | Where the running AgentAPIs are registered |
| `--no-check` | `false` | Join without checking that the hub takes the events (for a hub that doesn't run yet) |
| `--allow-update` | `false` | Let the hub update this machine's AgentAPIs (`allow-update: true` in the config file); see [Updating from the hub](#updating-from-the-hub) |

Before it writes anything, `join` checks that the hub takes this machine's events: it sends the hub
a signed `webhook.test` event and stops, saying why, when the hub isn't reached, a sign-in page is
in front of it (a Coder port URL redirects to Coder's login), or the secret differs. It also stops
when the hub is on another machine and `--public-url` names `localhost`, which the hub can't reach.
For these checks it fills in `{ip}` toward the hub, and prints the address.

`join` writes `webhook-url`, `webhook-secret`, `webhook-events` and `public-url` into the config
file and applies them to the running AgentAPIs with `PUT /webhook`, keeping their timeout and
attempts. The file keeps `{ip}`, and so do the running AgentAPIs that fill it in themselves (newer
than 2.1.2, by their registry entry); an older one gets the address now, keeps it until it restarts,
and `join` says so. `join` reaches each one at its registry URL, or else at its port on localhost.
`leave` removes them and turns the running AgentAPIs' webhook off. An AgentAPI that can't be changed
now applies the file when it restarts. With `--allow-update`, `join` also writes
`allow-update: true`, which AgentAPIs read when they start.

A machine that joined with an earlier version still sends `task.*`, `request.*`, `login.*`,
`limit.*` and `agent.error` as well, up to 64 KiB each; the hub only takes them as signs of life.
Run the same `agentapi hub join` again to send only `instance.*`: it rewrites the file and the
running AgentAPIs, which then no longer follow their conversation for the webhook.

### The hub's HTTP API

| Endpoint | Description |
| --- | --- |
| `POST /hooks` | AgentAPI webhook events; signed with `--secret`, no session needed |
| `GET /api/session` | `{"authenticated": true}` when the request carries the session or the hub token |
| `POST /api/session` | `{"token": …}` signs in and sets the session cookie |
| `DELETE /api/session` | Signs out |
| `GET /api/instances` | The instances: who each one is, its summary, its connection, the state its row shows (`state`: `needs-you`, `working`, `unread`, `idle`, `muted`, `connecting`, `unreachable` or `stopped`), when what its row shows last changed (`changed_at`, RFC 3339; see [Recent changes](#recent-changes)), its place among the pinned ones (`pin`, from 1; none when it isn't pinned) and how long the hub saw it working (`work`: `last_24h_seconds` and `last_7d_seconds` of the spans that ended, as of the response, and `working_since` while it works; see [Work time](#work-time)); and the hub's own version (`hub_version`) |
| `POST /api/instances` | `{"url": …, "token": …}` adds an instance by its URL |
| `PATCH /api/instances/{id}` | `{"group": …}` puts an instance in a group, `""` in none; `{"muted": true}` mutes what it waits for now, until that changes (409 when it waits for nothing), `false` ends the mute; `{"pinned": true}` pins it below the pinned ones, `false` unpins it |
| `DELETE /api/instances/{id}` | Forgets an instance, and its pin |
| `POST /api/instances/forget` | `{"ids": […]}` forgets, at once, those of the instances listed whose `state` is `stopped` now, and their pins; the answer lists those it forgot (`{"ids": […]}`). One that isn't stopped, or that the hub doesn't have, stays as it is. Each one forgotten is a `removed` event |
| `PUT /api/pins` | `{"ids": […]}` orders the pinned instances, from the top. It lists each pinned instance once and no other; 409, with the pinned ones in their order (`ids`), when it doesn't, such as after one was pinned, unpinned or forgotten; 404 for an instance the hub doesn't have |
| `GET /api/favorites` | The [favorites](#favorites), in their order: `{"favorites": [{"id", "title", "text"}, …]}`; a favorite without a title has none |
| `PUT /api/favorites` | `{"favorites": […]}` replaces them, in that order. At most 100; a title has at most 100 characters, on one line; a text isn't blank and has at most 20,000; an ID has 1 to 64 letters, digits, `-` or `_`, once. A favorite sent without an ID gets one; the answer is the list as the hub keeps it. 400, changing nothing, for a list it refuses |
| `GET /api/providers` | The [model providers](#model-providers), without their keys, and the list's revision: `{"revision", "providers": [{"id", "name", "base_url", "anthropic_base_url", "models": [{"id", "name"}], "has_key", "openrouter", "model_prices"}, …], "prices"}`. The revision is an opaque token. `openrouter` is the price picked for a model, by its ID: an OpenRouter model ID, or `none`. `model_prices` has each model (those defined, else those the provider listed at the last refresh, then those with a pick): `{"model", "name", "choice", "openrouter", "openrouter_name", "price"}`, `choice` `auto`, `chosen` or `none`, `price` `{"input", "output", "cache_read", "cache_write", "source"}` in USD per million tokens, missing without one. `prices` is `{"enabled", "fetched_at", "models", "error"}`: whether the hub looks prices up, when it last got OpenRouter's list, how many models it has, and why the last try failed |
| `PUT /api/providers` | `{"revision", "providers": […]}` replaces them, with the revision of the last `GET` or `PUT`. A provider's `api_key` sets its key; missing keeps the key the provider with its ID had only while its `base_url` and `anthropic_base_url` stay the same, `""` removes it. Its `openrouter` replaces its price picks; missing keeps them, `{}` removes them. At most 50; an ID once, not `default`; the URLs http or https. The answer is the list and its new revision, as `GET` has them. 409, changing nothing, when the list changed since that revision: read it again; 400, changing nothing, for a list it refuses, a missing revision, or a changed URL without its key |
| `POST /api/providers/check` | `{"id", "base_url", "anthropic_base_url", "api_key"}` asks the provider at `base_url` for its models (`GET <base_url>/models`) from the hub: `{"models": [{"id", "name"}], "latency_ms"}`. Without `api_key`, with the key the provider with `id` has, if its URLs are unchanged; 400 without asking when they changed. 502 with the reason when the provider doesn't answer with its models |
| `POST /api/providers/prices` | Gets OpenRouter's list again and has the providers without models list theirs, then answers as `GET /api/providers`; OpenRouter failing is in `prices.error`. 409 for a hub that doesn't look prices up |
| `GET /api/providers/openrouter?q=` | The OpenRouter models of the hub's copy whose ID or name has every word of `q`, at most 50: `{"models": [{"id", "name", "price"}], "fetched_at"}` |
| `POST /api/providers/sync` | An AgentAPI gets the providers with their keys and their models' prices, sealed with the hub's secret: `{"sealed", "nonce", "proof"}`, `proof` binding `sealed` to the request's `nonce`. Signed with `--secret` as events are, no session needed. AgentAPIs before the prices ignore them |
| `GET /api/events` | Server-sent events: `instances` (all of them and `hub_version`, as `GET /api/instances` has them) and `favorites` (as `GET /api/favorites` has them), then `instance` (one changed what the page shows, or its work time moved by a minute; when it was last heard of counts only while it is offline), `removed` (`{"id"}`), `favorites` (all of them, after a change), `dispatch` (the [issue dispatch's](#issues) machines and jobs, as `GET /api/hosts` and `GET /api/jobs` have them, at first and after each change) and a `heartbeat` every 15 s. A browser that takes no event for 30 s loses the stream |
| `GET /api/hosts` | The machines whose launchers report: their agents, repositories, jobs and room, and whether each is `online` |
| `DELETE /api/hosts/{name}` | Forgets a machine and the launcher token the hub pinned its name to, so a launcher with a new token can join under the name. 409 while the machine is online (`host_online`) or has a job that works (`host_busy`): stop its jobs first; 404 for a machine the hub doesn't have |
| `GET /api/jobs` | The jobs, each an issue's work: its state, agent and machine, pull request, runs with their cost, and the last 50 messages its agents were sent (`messages`: `at`, `source` `hub`, `review` or `comment`, `author`, `agent`, `text`, up to 2,000 characters) |
| `GET /api/github` | The hub's GitHub App, if any (`slug`, `owner`, `owner_type` `User` or `Organization` when the hub knows it, `html_url`, `settings_url`, `install_url`), and where it is installed (`installations`: `account`, `account_type`, `repository_selection` `all` or `selected`, `avatar_url`, `settings_url`); `DELETE` forgets the app, which stays on GitHub |
| `GET /api/github/repos` | The repositories the app can see, the latest pushed to or changed first (`pushed_at`, `updated_at`) |
| `GET /api/github/issues?repo=` | A repository's open issues; `POST` opens one, and with `assign`, starts work on it |
| `POST /api/dispatch` | Starts, messages, links and stops the work on an issue (`action`: `start`, `send`, `link_pr`, `stop`); also takes `--dispatch-token`. A start answers once its job is `starting` on a machine; the job's state then follows on the stream and in `GET /api/jobs` |
| `GET /api/activity?tz=&days=` | The [activity](#activity) of each of the last `days` days (1 to 371, 371 by default: 53 weeks), the earliest first, in the time zone `tz` names (an IANA name such as `Asia/Taipei`; UTC by default): `{"days": [{"date": "2026-11-10", "work_seconds", "usd", "tokens": {"input", "output", "cache_write", "cache_read", "total"}}, …], "since", "time_zone"}`. `since` is the first day the hub kept, in that zone; an hour counts in the day it starts in there. 400 for a time zone or a count it doesn't take |
| `GET /api/join` | Where machines join the hub: `urls`, each a webhook URL (`url`) with the interface of its address (`interface`; none for `--bind`'s), in the order the startup log has them; and the secret (`secret`, with `secret_shown: true`) only on a hub with a token |
| `/i/{id}/…` | The instance's own API and chat, through the hub; the page updates an instance with `POST /i/{id}/update` and cancels with `DELETE` |

Every endpoint but `/hooks`, `/api/providers/sync` and `/api/session` needs the session cookie or
`Authorization: Bearer <hub token>`. Instance tokens never appear in any response. A refused or
failed request answers `{"code": …, "message": …, "error": …}`: `code` is the route's own (such as
`host_online`) or the HTTP status in snake case (such as `not_found`), and `error` repeats
`message`, the only field older hubs answered on their own routes.

### Files

| File | Written by | Holds |
| --- | --- | --- |
| `~/.agentapi/config.yaml` | `agentapi hub join` | The hub's webhook and `allow-update`, read by every `agentapi server` |
| `~/.agentapi/instances/` | `agentapi server` | The AgentAPIs running on this machine, for `join` and `agentapi list` |
| `~/.agentapi/hub/instances.json` | `agentapi hub serve` | The instances the hub knows, their groups and pins, when each last changed, when each worked in the last 7 days, the URL where the hub last read each one's summary, and tokens of the ones added by hand; and the [favorites](#favorites) |
| `~/.agentapi/hub/instances.json.bak` | `agentapi hub serve` | The file of instances as the hub found it when it last started |
| `~/.agentapi/hub/activity.json` | `agentapi hub serve` | The [activity](#activity) of the last 366 days, hour by hour (UTC): the work time, and each scope's spend and tokens; written at most every 15 seconds. `activity.json.bak` is the file as the hub found it when it last started; one that doesn't parse is moved aside (`.corrupt-<time>`) and the hub starts a new one |
| `~/.agentapi/hub/instances.deliveries.json` | `agentapi hub serve` | The delivery IDs of the events the hub took whose signature still holds, to refuse copies |
| `~/.agentapi/hub/dispatch.json` | `agentapi hub serve` | The [issue dispatch's](#issues) machines with their launchers' tokens, and the jobs: their runs, costs and last 50 messages |
| `~/.agentapi/hub/github-app.json` | `agentapi hub serve` | The hub's GitHub App and its private key |
| `~/.agentapi/hub/providers.json` | `agentapi hub serve` | The [model providers](#model-providers), their keys and their price picks (0600) |
| `~/.agentapi/hub/openrouter.json` | `agentapi hub serve` | The hub's copy of OpenRouter's models and their prices, as it last got them, and the models the providers without models listed |
| `~/.agentapi/hub-providers.json` | `agentapi server` | On each machine, beside its config file: the providers the hub last answered, sealed with the hub's secret, which stand in at the next start until the hub answers |

`instances.json` is `{"version": 1, "instances": [...]}`. Hubs of 2.2 and earlier wrote, and read,
only the array: the hub reads that too, but such a hub sets the newer file aside as damaged and
starts empty. To go back to one, stop the hub and keep only the array first:

```sh
cd ~/.agentapi/hub && jq .instances instances.json > instances.json.new && mv instances.json.new instances.json
```

Each instance's work time is an extra field (`work`) inside the same version 1. A hub of 2.3 or 2.4
ignores it, and drops it the next time it writes the file. So does a hub before 3.0.1 with
`confirmed_url`, the URL where the hub last read the instance's summary, and a hub before 3.5 with
the favorites (`"favorites": [...]` next to `instances`). After going back to such a hub, the
`.bak` it made as it started still has them. The hub keeps the favorites in `"settings":
{"favorites": [...]}`, and reads the top-level `"favorites"` of a hub of 3.5 to 3.7 when there are
no settings. Hubs of 3.8 to 3.15.1 wrote that copy too; this one doesn't, so a hub of 3.5 to 3.7 run
on its file starts without favorites; the `.bak` it makes as it starts keeps them for this one.

## Limits

- The hub serves the chat of its own version to every instance. Run the same AgentAPI version on
  the hub and the instances; [Versions](#versions) shows the ones that differ. The hub doesn't
  show an AgentAPI 2's PTY agent waiting on a dialog in its terminal: the row shows it idle or
  working. Answer the dialog in that AgentAPI's own chat, at its URL.
- One hub token for everyone: there are no per-user accounts or permissions, and everyone signed
  in shares the [favorites](#favorites).
- [Work time](#work-time) is what the hub saw: not before it had the feature, nor while it was down
  or couldn't reach an agent. One hub doesn't know what another counted.
- The [Activity](#activity) card starts on the day the hub runs a release with it, and its spend
  and tokens count only AgentAPI 4.2 and later. An agent's spend in hours the hub didn't hear of
  (it was down, or couldn't reach the agent, for more than a day) is missing.
- The hub watches and opens AgentAPIs, and updates those whose machine allows it; it doesn't start
  or stop them.
- Nothing reaches your phone while the hub's page is closed: the hub doesn't send push
  notifications yet. Each AgentAPI has one webhook, and the hub uses it.
