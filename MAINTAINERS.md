# Information for Maintainers

## Release Process

Releases are automated by `.github/workflows/release.yml`. Every push to `main` runs
`next-version.sh`, which determines the next version from
[Conventional Commit](https://www.conventionalcommits.org/) messages since the last `vX.Y.Z` tag.
The highest applicable bump wins:

| Commits since the last release                                   | Bump  |
|------------------------------------------------------------------|-------|
| `type!:` subject or a `BREAKING CHANGE:` footer                  | major |
| `feat:`                                                          | minor |
| Other commits (`fix:`, `perf:`, `refactor:`, or non-conventional messages), excluding merge commits | patch |
| Only `docs:`, `chore:`, `test:`, `ci:`, `style:`, `build:`, or merge commits | none |

When a release is needed, the workflow builds versioned binaries (`make build VERSION=X.Y.Z`),
creates the `vX.Y.Z` tag and a GitHub release with generated notes, and attaches the binaries and
`checksums.txt`. The running version is shown in the chat UI header and reported as
`agentapi_version` by `GET /status` and the `status_change` SSE event.

To force a release or a specific bump, run the workflow manually (Actions → Release → Run workflow)
and pick `patch`, `minor` or `major`.

Before merging agent-specific changes, perform a local smoke test (`go test ./e2e`, plus manual
testing where needed).

After the Release workflow publishes `vX.Y.Z`, rename `## Unreleased` in `CHANGELOG.md` to
`## vX.Y.Z` and start a new, empty `## Unreleased` section above it. Make this change in a `docs:`
commit: `docs:` commits don't cut a release.
