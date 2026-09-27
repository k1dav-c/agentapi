# Information for Maintainers

## Release Process

Releases are automated by `.github/workflows/release.yml`. Every push to `main`
runs `next-version.sh`, which picks the next version from the
[Conventional Commits](https://www.conventionalcommits.org/) since the last
`vX.Y.Z` tag:

| Commits since the last release                                   | Bump  |
|------------------------------------------------------------------|-------|
| `type!:` subject or a `BREAKING CHANGE:` footer                  | major |
| `feat:`                                                          | minor |
| anything else (`fix:`, `perf:`, `refactor:`, non-conventional…)  | patch |
| only `docs:` / `chore:` / `test:` / `ci:` / `style:` / `build:`  | none  |

The workflow then builds the binaries with the version stamped in
(`make build VERSION=X.Y.Z`), creates the `vX.Y.Z` tag and GitHub release with
generated notes, and attaches the binaries and `checksums.txt`. The running
version is shown in the chat UI header and reported as `agentapi_version` by
`GET /status` and the `status_change` SSE event.

To force a release or a specific bump, run the workflow manually
(Actions → Release → Run workflow) and pick `patch`, `minor` or `major`.

Before merging agent-specific changes, perform a local smoke test
(`go test ./e2e`, plus manual testing where needed), and move the
`## Unreleased` notes in `CHANGELOG.md` under the upcoming version.
