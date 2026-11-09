#!/usr/bin/env bash
# Prints the version the Release workflow releases for the commit checked
# out (with its tags and full history), or nothing when it releases none,
# and why on stderr:
#
#   release            the published release's tag (RELEASE_TAG)
#   push               nothing if a later push's release holds the commit
#                      already, or a force-push took it off main; else
#                      next-version.sh's answer
#   workflow_dispatch  next-version.sh with the bump picked (BUMP)
#
# Usage: EVENT_NAME=push [RELEASE_TAG=vX.Y.Z] [BUMP=auto] ./release-version.sh
set -euo pipefail

event=${EVENT_NAME:?}
if [[ $event == push ]]; then
	# main as it is now: checkout points origin/main at the commit it checks
	# out.
	git fetch --quiet origin main
fi
version=""
if [[ $event == release ]]; then
	version=${RELEASE_TAG#v}
elif [[ $event == push ]] && released=$(git tag --contains HEAD --list 'v[0-9]*.[0-9]*.[0-9]*') && [[ -n $released ]]; then
	# The tests of a later push finished first, and its release holds this
	# commit.
	echo "Released already in ${released//$'\n'/, }; skipping." >&2
elif [[ $event == push ]] && ! git merge-base --is-ancestor HEAD FETCH_HEAD; then
	echo "$(git rev-parse HEAD) is no longer on main (force-pushed); skipping." >&2
else
	version=$(./next-version.sh "${BUMP:-auto}")
	if [[ -z $version ]]; then
		echo "No release-worthy commits since the last release; skipping." >&2
	fi
fi
if [[ -n $version ]]; then
	echo "Version: $version" >&2
fi
echo "$version"
