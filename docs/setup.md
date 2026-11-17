# Setting up Tendyard

[English](setup.md) | [繁體中文](setup.zh-TW.md)

This guide takes you from empty machines to a GitHub issue that an agent turns into a pull
request. Do the steps in order: each one needs the ones before it, and each ends with a check.

- **One machine, one agent:** you need only [Install the binary](#install-the-binary). Then
  `agentapi server -- claude` and open `http://localhost:3284/chat`, as in the
  [README](../README.md#quickstart).
- **Many machines on one page:** do the hub and worker steps, up to
  [Join each worker machine](#join-each-worker-machine).
- **GitHub issues handed to agents:** do every step.

## Contents

- [Four roles share the work](#roles)
- [Check these before you start](#before-you-start)
- [Make the credentials once](#credentials)
- [Install the binary where it can update itself](#install-the-binary)
- [Run the hub as a service](#run-the-hub)
- [Join each worker machine](#join-each-worker-machine)
- [Connect the hub's GitHub App](#github-app)
- [Set up the runner that forwards GitHub events](#runner)
- [Add the workflow and labels to each repository](#workflow-and-labels)
- [Dispatch a first issue](#first-issue)
- [Keep it up to date](#updates)
- [Symptoms seen during setup](#setup-problems)

<a id="roles"></a>

## Four roles share the work

| Role | Runs | Needs |
| --- | --- | --- |
| Hub machine | `agentapi hub serve`, always on | Reachable from your browser, the workers and the runner |
| Worker machines | The agents: `agentapi server` instances, and the launcher `agentapi host serve` if they take issues | The agent CLIs, logged in; `git` and `gh` for issues |
| Runner machine | A self-hosted GitHub Actions runner, labeled `agent-dispatch` | `bash`, `curl`, `jq`; reaches the hub |
| GitHub | The hub's GitHub App, a central workflow, and a caller workflow in each repository | An organization or account where you can create apps and runners |

One machine can hold several roles. The hub and a worker can share a machine, and so can the runner
and the hub. Run the runner as a separate user from the agents, though, so a workflow can't reach
the agents' logins.

<a id="before-you-start"></a>

## Check these before you start

**Network.** Each arrow is a connection one side opens. Check it with `curl` from the machine on the
left:

| From | To | Port | For |
| --- | --- | --- | --- |
| Your browser or phone | Hub | 3290 | The hub's page |
| Workers | Hub | 3290 (`/hooks`) | Instances and launchers send their signed events |
| Hub | Workers | Each instance's port (3284, …), the launcher's 3291, the jobs' 4100–4199 | The hub reads summaries, proxies the chat, starts jobs |
| Runner | Hub | 3290 (`/api/dispatch`) | The workflow's dispatches |
| Runner | github.com | 443 | The runner takes jobs from GitHub; nothing from GitHub connects in |

The hub needs no address on the internet: GitHub never calls it, and the GitHub App's setup goes
through your browser. Use addresses the machines reach each other at directly, such as a LAN or VPN
address. A URL behind a sign-in page, such as a Coder workspace's port URL, doesn't work for
`/hooks`: the events get a redirect to the sign-in page instead of the hub.

**On each worker,** as the user that runs the agents:

- The agent CLIs you want (`claude`, `codex`, `pi`) are installed and logged in. Run each one once in
  a terminal to check. A missing login shows in the chat as a login card, and a dispatched job waits
  on it.
- `git` and `gh` are installed. The agent reads the issue with `gh issue view` and opens the pull
  request with `gh pr create`. With the GitHub App, the launcher gives both a token for the job's
  repository. Without it, they use the machine's own credentials (`gh auth login`, an SSH key).
- Services keep running after you log out. `agentapi host install` and a user-level hub service
  need a systemd user instance that outlives your session: `sudo loginctl enable-linger <user>`.
  Containers without systemd, such as Coder workspaces, start the launcher another way
  ([`agentapi host`](cli.md#agentapi-host)).

<a id="credentials"></a>

## Make the credentials once

Make each value once and keep it in a password manager. Each one goes to a different place:

```bash
openssl rand -hex 32   # HUB_TOKEN: you sign in to the hub's page with it
openssl rand -hex 32   # HUB_SECRET: instances and launchers sign their events with it
openssl rand -hex 32   # HUB_DISPATCH_TOKEN: the runner calls POST /api/dispatch with it
```

| Value | Goes to | Can |
| --- | --- | --- |
| `HUB_TOKEN` | The hub (`AGENTAPI_HUB_TOKEN`), your browser | Everything the hub's page can do |
| `HUB_SECRET` | The hub (`AGENTAPI_HUB_SECRET`), each worker's `agentapi hub join` | Report instances and machines to the hub |
| `HUB_DISPATCH_TOKEN` | The hub (`AGENTAPI_HUB_DISPATCH_TOKEN`), the runner's `.env` | Call `POST /api/dispatch` only |
| Instance API token (optional) | The hub (`AGENTAPI_HUB_INSTANCE_TOKEN`), instances you start by hand with `--api-token` | Use those instances' API |

Jobs the launcher starts don't use the instance token: each launcher makes its own
(`~/.agentapi/instance.token`) and hands it to the hub. [Tokens grant shared access across trusted
machines](hub.md#tokens-and-security) says what each one exposes.

<a id="install-the-binary"></a>

## Install the binary where it can update itself

On every machine, install `agentapi` as the user that runs it, in `~/.local/bin`:

```bash
OS=$(uname -s | tr "[:upper:]" "[:lower:]")
ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/")
mkdir -p "$HOME/.local/bin"
curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" \
  -o "$HOME/.local/bin/agentapi"
chmod +x "$HOME/.local/bin/agentapi"
agentapi --version
```

`agentapi update`, and updates from the hub, replace the binary where it is. A binary in a directory
the user can't write, such as a root-owned `/usr/local/bin`, can't update without `sudo`, and the hub
shows that machine as needing a one-time setup. If something must find it in `/usr/local/bin`, link
it there: `sudo ln -sf "$HOME/.local/bin/agentapi" /usr/local/bin/agentapi`.
[Updating from the hub](hub.md#updating-from-the-hub) has the fix for a machine already set up the other way.

**Check:** `agentapi --version` prints the release you downloaded.

<a id="run-the-hub"></a>

## Run the hub as a service

On the hub machine, put the values in a file only you can read:

```bash
mkdir -p ~/.config
cat > ~/.config/agentapi-hub.env <<'EOF'
AGENTAPI_HUB_TOKEN=<HUB_TOKEN>
AGENTAPI_HUB_SECRET=<HUB_SECRET>
AGENTAPI_HUB_DISPATCH_TOKEN=<HUB_DISPATCH_TOKEN>
EOF
chmod 600 ~/.config/agentapi-hub.env
```

Then run it as a systemd user service:

```ini
# ~/.config/systemd/user/agentapi-hub.service
[Unit]
Description=Tendyard hub

[Service]
EnvironmentFile=%h/.config/agentapi-hub.env
ExecStart=%h/.local/bin/agentapi hub serve --port 3290
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now agentapi-hub
```

Without lingering (see [Check these before you start](#before-you-start)), use a system unit
instead: the same file in `/etc/systemd/system/` with `User=<you>`, the `EnvironmentFile` as an
absolute path, and `WantedBy=multi-user.target`, managed with `sudo systemctl`.

Add `--bind <address>` to listen on one interface only, such as the VPN's. The hub keeps its files in
`~/.agentapi/hub/`: the instances, the dispatch's machines and jobs, the GitHub App and the model
providers.

**Check:**

```bash
systemctl --user status agentapi-hub
journalctl --user -u agentapi-hub | grep 'agentapi hub join'   # the join command for each address
```

Open `http://<hub>:3290/` and sign in with `HUB_TOKEN`. An empty hub shows how to connect the first
agents.

<a id="join-each-worker-machine"></a>

## Join each worker machine

On each worker, once, with the secret in the environment rather than in your shell history:

```bash
read -rs AGENTAPI_HUB_SECRET && export AGENTAPI_HUB_SECRET
agentapi hub join http://<hub>:3290/hooks --public-url 'http://{ip}:{port}' --allow-update
```

- `join` sends a signed test event first and stops if the hub doesn't take it.
- It writes the hub into `~/.agentapi/config.yaml`, which every `agentapi server` of this user reads
  at start. Instances that run now join at once.
- `{ip}` becomes the machine's address on the route to the hub and follows it when it changes.
- `--allow-update` lets the hub update this machine's instances. Leave it out to update by hand.

Keep each instance's conversation across restarts and updates by adding this line to
`~/.agentapi/config.yaml`:

```yaml
state-db: ~/.agentapi/state/{port}.db
```

If the machine takes issues, start its launcher too:

```bash
agentapi host install --now   # a systemd user service; see agentapi host for containers
agentapi host status
```

The launcher listens on port 3291, and the hub reaches it at `http://{ip}:3291`. The `host` section
of the config file sets where it clones repositories, which agents it offers and how many jobs it
runs at once ([`agentapi host`](cli.md#agentapi-host)).

**Check:**

```bash
agentapi hub status   # the hub, and where each running instance reports
agentapi host status  # the launcher: its hub, agents, GitHub credentials, repositories, jobs
```

The hub's list shows the machine's instances, and its Issues page lists the machine with its agents.
A launcher whose machine is the hub's own, joined by `127.0.0.1`, needs the hub started with
`--allow-loopback-launchers`.

<a id="github-app"></a>

## Connect the hub's GitHub App

Signed in to the hub, open:

```text
http://<hub>:3290/github/connect?org=<organization>   # leave out ?org= for your own account
```

Confirm on GitHub, then pick the repositories to install it on. Only your browser goes between GitHub
and the hub, so the hub can stay on a private network. The app asks for contents, pull requests,
issues and workflows (write) and metadata (read). With it, the agent pushes and opens pull requests
as `<app>[bot]`, and the hub comments on issues and pull requests.

**Check:** the hub's settings, GitHub tab, shows the app and its installations
([GitHub settings](hub.md#github-settings)), and Issues lists the repositories' open issues.

<a id="runner"></a>

## Set up the runner that forwards GitHub events

GitHub doesn't call the hub. A self-hosted runner that can reach the hub takes the workflow's jobs
and posts each event to the hub. On the runner machine:

1. **Make a user for it,** not the one the agents run as: `sudo useradd -m gh-runner`. Install
   `curl` and `jq`.
2. **Limit who can use it.**
   - In an organization: Settings → Actions → Runner groups, a group open only to the private
     repositories that dispatch issues. Leave **Allow public repositories** off: a pull request from
     a fork of a public repository could run anything on the runner.
   - On a personal account there are no runner groups: add the runner to each repository
     (Settings → Actions → Runners).
   - In Settings → Actions → General, keep **Run workflows from fork pull requests** off.
3. **Register it.** Under Settings → Actions → Runners → New self-hosted runner, follow GitHub's
   download and `./config.sh` steps as that user, adding `--labels agent-dispatch` (and
   `--runnergroup <group>` in an organization).
4. **Give it the hub's address and dispatch token,** in `.env` in the runner's directory, which the
   runner passes to its jobs:

   ```bash
   cat > .env <<'EOF'
   HUB_URL=http://<hub>:3290
   HUB_DISPATCH_TOKEN=<HUB_DISPATCH_TOKEN>
   EOF
   chmod 600 .env
   ```

5. **Run it as a service:** `sudo ./svc.sh install gh-runner && sudo ./svc.sh start`. It reads
   `.env` when it starts: after changing `.env`, run `sudo ./svc.sh stop && sudo ./svc.sh start`.

**Check,** from the runner machine as its user. A `400` means the hub took the token and refused
the empty request. A `401` means a wrong token, and no answer means the hub isn't reachable:

```bash
set -a; . ./.env; set +a
curl -s -o /dev/null -w '%{http_code}\n' -X POST "$HUB_URL/api/dispatch" \
  -H "Authorization: Bearer $HUB_DISPATCH_TOKEN" -H 'Content-Type: application/json' -d '{}'
```

GitHub lists the runner as Idle under Settings → Actions → Runners. Every workflow that may use the
runner can read `HUB_DISPATCH_TOKEN`. The token can only dispatch, but open the runner to trusted
repositories alone.

<a id="workflow-and-labels"></a>

## Add the workflow and labels to each repository

1. Put [`agent-dispatch.yml`](../examples/issue-dispatch/agent-dispatch.yml) in a central
   repository, such as `<organization>/.github`, as `.github/workflows/agent-dispatch.yml`. If it is
   private, allow the other repositories to use it: its Settings → Actions → General → Access.
2. In each repository that dispatches issues, add
   [`caller.yml`](../examples/issue-dispatch/caller.yml) as `.github/workflows/agent.yml`, with
   `k1dav-c/.github` changed to your central repository.
3. Create the labels the workflow reacts to:

   ```bash
   for label in agent agent:claude agent:codex agent:pi; do
     gh label create "$label" --repo <owner>/<repo> --color 5319e7 \
       --description 'Hand this issue to an agent' --force
   done
   ```

   Leave out the agents your workers don't run. `agent` alone uses the launcher's default agent.

[The workflow's events](../examples/issue-dispatch/README.md#使用) lists what each label, review and
comment does. Only people with write access to the repository count.

<a id="first-issue"></a>

## Dispatch a first issue

Open a small issue in a repository with the workflow, and add the `agent` label. Then follow it:

1. The **Actions** tab runs the `agent` workflow on the runner. A failed run that comments "Tendyard
   hub 沒有接受這次派工" means the hub refused the dispatch: no machine online, no machine with that
   agent, or the app isn't installed on the repository.
2. The hub's Issues page shows the issue as Starting, then working, on the machine it picked. The
   hub comments on the issue that the agent started.
3. Open the job's chat from its detail to follow it or answer a request. The agent pushes
   `agent/issue-<n>` and opens a pull request.
4. Review the pull request. A review that requests changes, or a `/agent …` comment, goes back to
   the same agent. Merging or closing the pull request stops the job, deletes the branch and posts
   the cost.

You can also start an issue from the hub alone, without the runner and workflow: **Assign** on
Issues ([Issues](hub.md#issues)). Reviews then don't come back to the agent by themselves.

<a id="updates"></a>

## Keep it up to date

- **Hub:** `agentapi update` on the hub machine, then restart its service
  (`systemctl --user restart agentapi-hub`). Update the hub before the workers: newer instances
  may need the newer hub, as for model providers.
- **Instances:** the overview's Versions card updates the machines joined with `--allow-update`,
  each once its agent is idle ([Updating from the hub](hub.md#updating-from-the-hub)). Elsewhere, run
  `agentapi update --restart` on the machine.
- **Launchers:** after `agentapi update`, `systemctl --user restart agentapi-host`. Its jobs keep
  running, and the new launcher takes them over.

<a id="setup-problems"></a>

## Symptoms seen during setup

| Symptom | Cause | Fix |
| --- | --- | --- |
| `hub join` says the hub didn't take the test event | Wrong secret, the hub isn't reachable, or a sign-in page answers instead | Check `HUB_SECRET`; from the worker, `curl -s -o /dev/null -w '%{http_code}' http://<hub>:3290/` should print a status at once; use a direct address |
| Services stop when you log out | No lingering for the user | `sudo loginctl enable-linger <user>`, or a system unit |
| `agentapi host install` says `no systemd user instance` | A container without systemd | Start `agentapi host serve` from the workspace's start script ([`agentapi host`](cli.md#agentapi-host)) |
| The hub's Versions card asks for a one-time setup | The binary is in a directory the user can't write | Move it to `~/.local/bin` ([Updating from the hub](hub.md#updating-from-the-hub)) |
| `agentapi update` fails with GitHub's rate limit | Many machines behind one public address | Wait an hour, or download the release with `gh release download` and check it against `checksums.txt` |
| The runner check answers `401` | `.env` has another token, or the runner hasn't restarted since `.env` changed | Fix `.env`, then stop and start the runner service |
| The workflow never runs | The runner's label or group doesn't match, or the central workflow isn't shared | Check the runner's labels, its group's repositories, and the central repository's Access setting |
| A job's push to `.github/workflows/` is refused | The app lacks the workflows permission | Add it in the app's settings and accept it on each installation |
| The issue stays Starting, then fails | The launcher can't clone, the agent isn't logged in, or the job's instance exits | The issue's detail shows why and its log; `agentapi host status` on the machine |

[Match the visible failure to its cause before changing setup](hub.md#troubleshooting) covers the
hub once it runs.
