# Information for Maintainers

## Release Process

On every push to `main`, `.github/workflows/release.yml` first runs the tests
(`.github/workflows/go-test.yml`, as a reusable workflow) on that commit, and releases nothing
unless its jobs (test, race, lint) pass, because AgentAPIs with `--allow-update` install the
latest release. The workflow then runs `next-version.sh` to choose the next version from
[Conventional Commit](https://www.conventionalcommits.org/) messages since the last `vX.Y.Z` tag.
The highest applicable bump wins:

| Commits since the last release                                   | Bump  |
|------------------------------------------------------------------|-------|
| `type!:` subject or a `BREAKING CHANGE:` footer                  | major |
| `feat:`                                                          | minor |
| Other commits (`fix:`, `perf:`, `refactor:`, or non-conventional messages), excluding merge commits | patch |
| Only `docs:`, `chore:`, `test:`, `ci:`, `style:`, `build:`, or merge commits | none |

For a release, the workflow builds binaries with `make build VERSION=X.Y.Z`, creates the `vX.Y.Z`
tag and a GitHub release with generated notes, then attaches the binaries and `checksums.txt`.
The chat header, `GET /status` and the `status_change` SSE event report the running version
(`agentapi_version`).

To force a release or a specific bump, run the workflow manually (Actions → Release → Run workflow)
and pick `patch`, `minor` or `major`. A manual run, and a release published by hand (whose binaries
the workflow builds and attaches), wait for the tests too.

- To release after a flaky test failed, re-run the failed jobs of the Release run: the release job
  runs once the tests pass.
- Releases go out one at a time, and GitHub keeps one run waiting for its turn: a run that waits is
  cancelled when another starts waiting. The commits of a cancelled push go out with the next
  release.
- A push whose tests finish after a later push has released (that release holds its commits)
  releases nothing. Neither does a push that a force-push took off `main`.
- Pull requests run every test through the Go Tests workflow; pushes to `main` run them only in the
  Release workflow, whose checks on the commit are named `Go Tests / …`.

### The runner

Every job runs on a self-hosted runner (labels `self-hosted`, `linux`, `x64`), since the account's
GitHub Actions minutes ran out; nothing tests macOS or Windows. It runs as a user of its own,
`ghrunner`, without sudo, so a job can't reach the files of the machine's own AgentAPIs and hub,
as the systemd service `actions.runner.k1dav-c-agentapi.<machine>`
(`/home/ghrunner/actions-runner`). The jobs need `gcc` (the race detector), `shellcheck`, `file`
and `gh` on the machine; the workflows set up Go and Bun themselves.

The repository is public, so a pull request from a fork runs no job (its code would run on the
runner's machine), and the repository asks for approval before an outside contributor's workflows
run (Settings → Actions → General). To register the runner again, as `ghrunner` in its directory:
`./config.sh --url https://github.com/k1dav-c/agentapi --token <token> --replace`, with a token
from `gh api -X POST repos/k1dav-c/agentapi/actions/runners/registration-token`.

Before merging agent-specific changes, perform a local smoke test (`go test ./e2e`, plus manual
testing where needed).

After the Release workflow publishes `vX.Y.Z`, rename `## Unreleased` in `CHANGELOG.md` to
`## vX.Y.Z` and start a new, empty `## Unreleased` section above it. Make this change in a `docs:`
commit: `docs:` commits don't cut a release.
