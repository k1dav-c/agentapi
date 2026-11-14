# CLI reference

English | [繁體中文](cli.zh-TW.md)

This page lists the `agentapi` commands and their flags; [Resource usage](#resource-usage) at the
end gives what a running AgentAPI costs in memory and CPU.

## `agentapi server`

Start an HTTP server to control an agent. To pass startup arguments to the agent, place the full
agent command after the `--` separator.

```bash
agentapi server -- claude --allowedTools "Bash(git*) Edit Replace"
```

The agent is Claude Code, Codex or Pi (see [Agents](../README.md#agents)):

```bash
agentapi server -- codex -a on-request
agentapi server -- pi
```

### Flags

Flags can also be set with environment variables: `AGENTAPI_` and the flag's name in capitals, with
`_` for `-` (`AGENTAPI_ALLOWED_HOSTS`). Lists are comma-separated in a flag and space-separated in a
variable.

They can also be set by the flag's name in `~/.agentapi/config.yaml` (`--config` names another
file) for every server the user starts; a flag or a variable overrides the file. A setting that
names no flag is reported in the log, and a file AgentAPI can't read is reported and ignored, so the
agent still starts. [`agentapi hub join`](#agentapi-hub) writes it; keep it readable by its owner
only (`chmod 600`) when it holds a secret. A leading `~` in a file or directory setting is the home
directory, wherever the setting comes from. `state-db` and `pid-file` name a file that only one
server can use, so in the file each needs `{port}` in its path (`{hostname}` is filled in too);
without it, every server ignores the setting with a warning, and a flag or a variable still sets it.

```yaml
webhook-url: http://hub.vpn:3290/hooks
webhook-secret: s3cret
webhook-events: [instance.*, task.*, request.*]
public-url: http://dev-1.vpn:{port}
state-db: ~/.agentapi/state/{port}.db
```

| Flag | Default | Description |
| --- | --- | --- |
| `-t`, `--type` | from the agent command | Agent type: `claude`, `codex`, `pi` or `native-demo`; see [Agents](../README.md#agents) |
| `-p`, `--port` | `3284` | Port to listen on |
| `--bind` | all interfaces | Address to listen on; see [Bind address](security.md#bind-address) |
| `--state-db` | | SQLite file that keeps the conversation and resumes the agent's session; `{port}` and `{hostname}` are filled in; see [Persistent sessions](agents.md#persistent-sessions) |
| `--terminal` | `true` | Shell in the agent's working directory for the chat; see [Terminal](agents.md#terminal) |
| `--auto-continue` | `false` | Continue a turn stopped by a usage limit once the limit resets; see [Usage limits](agents.md#usage-limits) |
| `--retry-delays` | `30s,1m,2m,5m*12` | Waits before a turn that failed on a transient API error is tried again, one per retry (`5m*12`: twelve times); `off` turns retries off; see [Transient API errors](agents.md#transient-api-errors) |
| `--prices` | | JSON file of model prices per million tokens, which add to or replace the built-in ones; see [Session costs](agents.md#session-costs) |
| `-I`, `--initial-prompt` | | First message for the agent; read from stdin when piped |
| `-W`, `--term-width`, `-H`, `--term-height` | | Deprecated and without effect: the PTY transport's terminal size |
| `--api-token` | off | Require a Bearer token; without a value, a random token is generated and printed; see [API token authentication](security.md#api-token-authentication) |
| `-a`, `--allowed-hosts` | `localhost`, `127.0.0.1`, `[::1]` | Accepted `Host` headers, `*` for any; see [Allowed hosts](security.md#allowed-hosts) |
| `-o`, `--allowed-origins` | `http://localhost:3284`, `http://localhost:3000`, `http://localhost:3001` | Origins allowed to call the API from a browser, `*` for any; see [Allowed origins](security.md#allowed-origins) |
| `--webhook-url` | | URL the webhook events are POSTed to; see [Webhooks](api.md#webhooks) |
| `--webhook-events` | `run.status_changed` | Events to send: types, groups (`task.*`) or `*` |
| `--webhook-secret` | | Secret that signs every delivery (`X-AgentAPI-Signature-256`) |
| `--webhook-heartbeat` | `60s` | How often `instance.heartbeat` is sent; `0` turns it off |
| `--instance-id` | `<hostname>:<port>` | Identifies this AgentAPI in `GET /summary` and every webhook event; see [Watching many instances](api.md#watching-many-instances) |
| `--name` | the working directory's name | What clients that watch many AgentAPIs call this one |
| `--registry` | `true` | Keep an entry in the registry directory while serving; see [Watching many instances](api.md#watching-many-instances) |
| `--registry-dir` | `~/.agentapi/instances` | Registry directory |
| `--public-url` | | URL this AgentAPI is reached at, in `GET /summary` and every webhook event; `{port}` and `{hostname}` are filled in, and `{ip}` with the machine's IPv4 address on the route to the webhook URL (without one, its first private, then CGNAT, address), looked up again at most every 30 seconds while it runs; when none is found the last one stays. The address `{ip}` stands for is an allowed host |
| `--config` | `~/.agentapi/config.yaml` | Settings file: flags by name, below flags and variables |
| `--allow-update` | `false` | Let API clients, such as the hub, update AgentAPI to the latest release, which then restarts in place; see [Updating through the API](api.md#updating-through-the-api) |
| `--webhook-timeout` | `10s` | Timeout of each delivery attempt |
| `--webhook-max-attempts` | `3` | Delivery attempts |
| `--webhook-payload-template` | | Go `text/template` for the POST body |
| `-c`, `--chat-base-path` | `/chat` | Base path of the chat UI |
| `--pid-file` | | File to write the server's process ID to, for shutdown scripts; `{port}` and `{hostname}` are filled in |
| `--log-level` | `info` | Least important level to log: `debug`, `info`, `warn` or `error` |
| `-P`, `--print-openapi` | | Print the OpenAPI schema and exit |

AgentAPI logs to standard output, one `time=… level=… msg=…` line per entry. `--log-level debug`
(or `AGENTAPI_LOG_LEVEL`, or `log-level` in the config file) adds what it logs for troubleshooting,
such as the agent's command line, its login checks and the reads that worked (`GET`, `HEAD` and
`OPTIONS` requests below 400, which Info leaves out because clients poll `GET /status`); `warn` and
`error` leave out the routine lines. An unknown level stops the server before it starts.

## `agentapi update`

Update the AgentAPI binary to the latest release from GitHub.

```bash
agentapi update            # download and install the latest version
agentapi update --check    # only check if an update is available
agentapi update --force    # update even if already at the latest version
agentapi update --restart  # then restart the running AgentAPIs into it
agentapi update --restart --allow-update  # and let a hub update them from now on
```

Release binaries are verified against the release's `checksums.txt` SHA-256 manifest before the
running executable is replaced. An update is rejected if the manifest is missing, malformed, or does
not match the download, or if the download doesn't run as the release's version. The binary is
replaced under the lock file that [updates through the API](api.md#updating-through-the-api) take too.
The release lookup gets 1 minute; waiting for the lock, the download and the replacement get 10.

Running AgentAPIs keep the old version until they restart. `--restart` restarts the ones that run
the binary it replaced, as [`agentapi restart --all`](#agentapi-restart) does, and restarts them
when the binary is already up to date too: another update may have replaced it. Under `sudo` (an
install in `/usr/local/bin`), it restarts the AgentAPIs of the user who ran `sudo`, from that user's
registry. The hub isn't one of them: restart it with its service manager, such as
`systemctl restart agentapi-hub`.

`--allow-update` also writes `allow-update: true` into the config file (`--config`, by default
`~/.agentapi/config.yaml` of the user who ran `sudo`, which stays theirs), before `--restart`, so
the restarted AgentAPIs take updates from a [hub](hub.md#updating-from-the-hub) from then on.
It keeps the rest of the file, as `agentapi hub join --allow-update` does. With `--check` it writes
nothing. An older `agentapi` has no such flag: add the line to the file, then run
`agentapi update --restart`.

## `agentapi restart`

Restart running AgentAPIs in place, for example after an update replaced the binary:

```bash
agentapi restart 3285 3286  # the AgentAPIs on these ports
agentapi restart --all      # every AgentAPI of yours on this machine
```

`agentapi restart` finds them in the [registry](api.md#watching-many-instances) and sends each SIGUSR2.
An AgentAPI first runs the program it was started from with `--version`, then with the arguments
and environment it will run with and a check that it would start with them: its flags, `AGENTAPI_*`
variables and config file, its agent's arguments, and a state database it can write. If either
fails (an update brought a release that refuses a setting, say), it logs why and goes on serving,
and `agentapi restart` reports that it still runs as before. Otherwise it stops as for a shutdown (the agent is interrupted and closed, the
state database written, the registry entry removed, and the webhook sends `instance.stopped`), then
runs the same path again with the same arguments and environment, in the same process. Its PID
stays, so a PID file and a parent shell that waits for it, such as a Coder workspace's start
script, stay valid, and its output goes to the same place. The command waits until each AgentAPI
answers on its port again with a new `server_id`, and prints its new version.

- With `--state-db`, the conversation stays and the agent resumes its
  session. Without it, the agent resumes its session too: the restart
  carries the session's ID to the new program, which reads the conversation
  back from the session's log, as when you open it from the
  [earlier sessions](agents.md#earlier-sessions). Claude Code, Codex and Pi only. The
  queue, open requests, chosen settings and a usage-limit stop or retry wait
  start anew, and the `--initial-prompt` isn't sent again. A session whose
  log is gone starts anew, with a warning in the chat. The restart that
  updates from an earlier release starts a new session: the earlier release
  doesn't carry it.
- Only your own AgentAPIs restart, and only when the registry entry names a
  running `agentapi server`. Under `sudo`, they are those of the user who ran
  `sudo`. An AgentAPI of an earlier release can't restart in place:
  `agentapi restart` says so, and you restart that one yourself, once.
- The AgentAPI this command runs under, if any (an agent that runs
  `agentapi update --restart`), restarts last. Restarting it closes the
  agent, which can end the command.
- `--registry-dir` reads another registry, and `--timeout` (default `1m`)
  bounds the wait for each one.
- Not available on Windows.

## `agentapi list`

List the AgentAPIs running on this machine from the [registry](api.md#watching-many-instances), with
what each `GET /summary` says: idle, working, what it needs from you (`needs request`), and an
unread task.

```bash
agentapi list
NAME      AGENT   STATE                  URL                    TITLE
api       claude  needs request, unread  http://localhost:3284  Fix the failing test
frontend  codex   working                http://localhost:3285  Add the settings page
```

`--json` prints the entries and summaries as JSON, `--registry-dir` reads another directory, and
`--api-token` (or `AGENTAPI_API_TOKEN`) is sent to the instances that need a token.

## `agentapi hub`

Run a hub that watches many AgentAPIs, or connect this machine's AgentAPIs to one; see
[Running a hub](api.md#running-a-hub) and [Joining a hub](api.md#joining-a-hub).

```bash
agentapi hub serve --token "$HUB_TOKEN" --secret "$HUB_SECRET" --instance-token "$AGENTAPI_TOKEN"
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
agentapi hub status
agentapi hub leave
```

`serve` runs the hub: `--token` (or `AGENTAPI_HUB_TOKEN`) is what you sign in with (`--no-token`
instead skips signing in, for a hub behind a sign-in of its own such as a Coder workspace app),
`--secret` (or `AGENTAPI_HUB_SECRET`) checks the AgentAPIs' events, `--instance-token` (or
`AGENTAPI_HUB_INSTANCE_TOKEN`, else `AGENTAPI_API_TOKEN`) is sent to them. `--state` keeps the
instances, `--port` and `--bind` set where it listens, `--allowed-hosts` the names browsers may
open it by (any without it), and `--log-level` (or
`AGENTAPI_HUB_LOG_LEVEL`) how much it logs. For the [issue dispatch](hub.md#issues),
`--dispatch-token` (or `AGENTAPI_HUB_DISPATCH_TOKEN`) is the token a client (a GitHub workflow's
runner) calls `POST /api/dispatch` with, and nothing else, and `--github-app-file` the file of the
hub's GitHub App (default `github-app.json` next to `--state`).

`join` writes the hub's webhook into `~/.agentapi/config.yaml` and applies it to the registered
AgentAPIs running now. `--secret` (or `AGENTAPI_HUB_SECRET`) signs the events, `--events` picks them
(default `instance.*`, all the hub needs: it follows the rest from each summary), `--public-url`
is where the hub reaches them (`{ip}` follows the machine's address; the config file and the
AgentAPIs newer than 2.1.2 keep it, older ones get the address now), `--api-token` (or `AGENTAPI_API_TOKEN`) is sent to the ones that need
a token, and `--config` and `--registry-dir` name other files. An AgentAPI that `join` can't
change now applies the file when it restarts. `--allow-update` also writes `allow-update: true`, so
the hub can [update the AgentAPIs through their API](api.md#updating-through-the-api) once they restart;
`leave` keeps it.

## `agentapi host`

Run the launcher of the [issue dispatch](hub.md#issues) on a machine that joined a hub: it
starts an AgentAPI for each GitHub issue the hub dispatches here, in a git worktree on the issue's
branch, and stops it again. [examples/issue-dispatch](../examples/issue-dispatch/README.md) sets up
the whole dispatch.

A machine needs the launcher only to take issues. `agentapi hub join` alone has its AgentAPIs report
to the hub, which lists them and opens them. The launcher reports the machine itself (its agents,
repositories, room and jobs), which Issues shows among the machines, and lets the hub start
AgentAPIs there; those report like any other. The launcher needs no hub settings of its own: it
reads the ones `hub join` wrote. The two are separate because the launcher lets the hub start
programs on the machine, which joining doesn't.

```bash
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
echo "$INSTANCE_TOKEN" > ~/.agentapi/instance.token && chmod 600 ~/.agentapi/instance.token
agentapi host install --now
agentapi host status
```

| Command | Flags | Does |
| --- | --- | --- |
| `serve` | `--config`, `--log-level` (`info`) | Runs the launcher in the foreground |
| `status` | `--config` | Shows the launcher, its hub, agents and repositories, and its jobs |
| `install` | `--config`, `--now` | Writes `~/.config/systemd/user/agentapi-host.service` (`agentapi host serve`) and enables it; `--now` also starts it, or restarts it if it runs. Running it again rewrites the unit, as after the program moved |
| `uninstall` | | Disables, stops and removes the service; the jobs' AgentAPIs keep running, and their files stay |

The service has `KillMode=process`: restarting or stopping the launcher leaves the jobs' AgentAPIs
running, and the next launcher takes them over. `install` needs a systemd user instance; when your
services stop at logout, it says so, and `sudo loginctl enable-linger <user>` keeps them running.

Without systemd, as in a container such as a Coder workspace, `install` stops with
`no systemd user instance`. Start `agentapi host serve` the way the machine starts its other
programs instead, as yourself rather than with `sudo` (the launcher reads your
`~/.agentapi/config.yaml` and keeps its token in your `~/.agentapi`):

```bash
nohup agentapi host serve >~/.agentapi/host.log 2>&1 &
```

That lasts until the workspace stops. For a Coder workspace, start it from the template, so it runs
on every start:

```hcl
resource "coder_script" "agentapi_host" {
  agent_id     = coder_agent.main.id
  display_name = "AgentAPI launcher"
  run_on_start = true
  script       = "nohup agentapi host serve >\"$HOME/.agentapi/host.log\" 2>&1 &"
}
```

The launcher reads the hub's `webhook-url` and `webhook-secret`, which `agentapi hub join` writes,
and the `host` section of `~/.agentapi/config.yaml` (`--config` names another file; the launcher
keeps its files next to it):

```yaml
host:
  port: 3291                    # the launcher's API, which the hub calls
  bind: ""                      # all interfaces; better the VPN address
  public-url: http://{ip}:3291  # where the hub reaches it
  workdir: ~/work               # clones and worktrees
  scan: [~/work, ~/src]         # where it looks for repositories it has (two levels deep)
  ports: 4100-4199              # the jobs' AgentAPIs
  max-jobs: 4
  agents: [claude, codex]       # default: those installed (claude, codex, pi)
  agent: claude                 # an issue's agent when the dispatch names none
  clone: true                   # clone a repository it doesn't have
```

| File | Holds |
| --- | --- |
| `~/.agentapi/host.token` | The token the hub calls the launcher with, made on first start. The hub pins the machine's name to it: a launcher with a new one (the file deleted, the machine set up again) joins only after the hub forgets the machine: **×** on it in the hub's Issues, or `DELETE /api/hosts/{name}` ([Troubleshooting](hub.md#troubleshooting)) |
| `~/.agentapi/instance.token` | The API token of every job's AgentAPI; it must be the hub's `--instance-token` |
| `~/.agentapi/host.sock` | Where the jobs' `git` and `gh` get their GitHub token from the launcher |
| `~/.agentapi/jobs.json`, `~/.agentapi/jobs/` | The jobs, and each one's state database, log and `gh` shim |

## Resource usage

AgentAPI itself is small: one Go process of 31–34 MiB that uses less than 0.1% of a CPU core while
the agent is idle. Most of the cost is the agent CLI it runs, at 120–285 MiB.

These numbers were measured on 2026-11-13 with AgentAPI v5.0.0 (a 33 MiB binary), Claude Code
2.1.302, codex-cli 0.167.0 and Pi 1.3.0, on Linux with a 4-core Intel Xeon Silver 4210 (2.2 GHz)
and one chat (an SSE client) connected. Each agent had three short turns: a reply of about 200
words, without tools. Memory is resident memory (RSS); CPU is a share of one core over an idle
minute, and CPU seconds for a whole turn, the agent's processes together.

| Agent | AgentAPI memory | AgentAPI CPU, idle | AgentAPI CPU per turn | Agent memory | Agent CPU per turn |
| --- | --- | --- | --- | --- | --- |
| Claude Code | 31 MiB | 0.08% | 0.06–0.08 s | 230–285 MiB | 0.5–1.2 s |
| Codex | 34 MiB | 0.07% | 0.09–0.13 s | 190–265 MiB | 0.3–0.8 s |
| Pi | 31–32 MiB | 0.05% | 0.11–0.13 s | 120–130 MiB | 0.2–0.3 s |
| `native-demo` (no agent process) | 31 MiB | 0.05% | 0.01–0.02 s | | |

- An agent's own memory moves during a session: Claude Code went from 230 MiB idle to 265–285 MiB
  over its turns, and Codex grew to 265 MiB on its first turn, then gave back about 70 MiB.
- AgentAPI's code (about 17 MiB of its memory) is shared by instances started from the same binary:
  with four running, each costs about 17–21 MiB (PSS).
- Against the same measurement with v0.24.0 (2026-10-18, a 29.5 MiB binary), AgentAPI takes about
  4 MiB more (27–30 MiB then) and the same CPU; the agents' own figures changed little.
- Not counted: MCP servers the agent starts, the commands it runs, the shell terminal (one shell
  while it is open) and the browser tab.

A long conversation grows by about 100 KB a task in its state database. On 2026-11-10, with a
conversation of 100 KB tasks restored from `--state-db` (`native-demo`, the same machine), AgentAPI
4.2.0 held all of it, and 4.3.0 holds the latest 100 tasks
([Long conversations](api.md#long-conversations)):

| Tasks | State database | Memory, 4.2.0 | Memory, 4.3.0 | First `/events?sync=1` replay, 4.2.0 | 4.3.0 |
| --- | --- | --- | --- | --- | --- |
| 0 | | 38 MiB | 34 MiB | 0.6 KB | 0.6 KB |
| 250 | 26 MB | 71–86 MiB | 49–52 MiB | 11 MB | 0.9 MB |
| 1,000 | 105 MB | 193–245 MiB | 55 MiB | 44 MB | 0.9 MB |

The memory that remains grows with the messages and tasks, a few KB a task. Messages stay in memory
whole, so very long prompts count in full: on 2026-11-13, in a conversation of 1,000 tasks made up
almost entirely of 90 KB prompts (97 MB in its state database), AgentAPI 5.0.0 held 148 MiB (67 MiB
at 250 tasks), while its first replay, the latest 20 tasks, stayed at 1.8 MB.

To size a machine, plan per session about 35 MiB for AgentAPI plus the agent (about 350 MiB for
Claude Code or Codex, 150 MiB for Pi) and whatever its MCP servers and commands need.

To measure again, run `bun scripts/measure-resources.mjs` (Linux only) on an isolated AgentAPI: it
measures `native-demo` by default, the agents you name with `--agents claude,codex,pi` (their turns
go to your accounts), and with `--long 0,250,1000` a long conversation, and prints the results as
these tables.
