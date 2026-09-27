#!/usr/bin/env bash
# Prints the next release version (without a leading "v") based on the
# Conventional Commits since the latest vX.Y.Z tag, or nothing when no commit
# since that tag warrants a release.
#
#   BREAKING CHANGE footer or "type!:" subject    -> major
#   feat:                                         -> minor
#   anything else except docs/chore/test/ci/style/build and merge commits
#                                                 -> patch
#
# Usage: ./next-version.sh [auto|patch|minor|major]
# A bump other than "auto" skips commit analysis and always releases.
set -euo pipefail

# First version when no vX.Y.Z tag exists yet: one minor above the last
# hand-maintained version (0.13.0).
INITIAL_BASE="0.13.0"

bump="${1:-auto}"
case "${bump}" in
auto | patch | minor | major) ;;
*)
	echo "unknown bump \"${bump}\" (want auto, patch, minor or major)" >&2
	exit 1
	;;
esac

last_tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)

if [[ -z "${last_tag}" ]]; then
	base="${INITIAL_BASE}"
	[[ "${bump}" == "auto" ]] && bump="minor"
else
	base="${last_tag#v}"
	base="${base%%-*}"
fi

if [[ "${bump}" == "auto" ]]; then
	bump=""
	while IFS= read -r -d $'\x1e' commit; do
		# git log separates records with a newline, so later records start with one.
		commit="${commit#$'\n'}"
		subject="${commit%%$'\n'*}"
		if [[ "${subject}" =~ ^[a-z]+(\([^\)]*\))?!: ]] || grep -qE '^BREAKING[ -]CHANGE:' <<<"${commit}"; then
			bump="major"
			break
		elif [[ "${subject}" =~ ^feat(\([^\)]*\))?: ]]; then
			bump="minor"
		elif [[ "${subject}" =~ ^(docs|chore|test|ci|style|build)(\([^\)]*\))?: ]] || [[ "${subject}" == Merge\ * ]]; then
			continue
		elif [[ -z "${bump}" ]]; then
			bump="patch"
		fi
	done < <(git log --no-merges --format='%s%n%b%x1e' "${last_tag}..HEAD")
fi

if [[ -z "${bump}" ]]; then
	exit 0
fi

IFS=. read -r major minor patch <<<"${base}"
case "${bump}" in
major) echo -n "$((major + 1)).0.0" ;;
minor) echo -n "${major}.$((minor + 1)).0" ;;
patch) echo -n "${major}.${minor}.$((patch + 1))" ;;
esac
