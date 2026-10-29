#!/usr/bin/env bash
# Prints the next sdk/vX.Y.Z tag for the nested Go module in sdk/
# (github.com/ocman/ocman/sdk), or nothing when sdk/ has no change since the
# latest sdk tag reachable from REV (default HEAD). Markdown-only changes do
# not release.
#
# Versioning: the first tag is sdk/v0.1.0. Before v1, a breaking change
# (`type!:` or a `BREAKING CHANGE:` footer) or a `feat` bumps the minor, and
# anything else bumps the patch. From v1 on, a breaking change needs a /v2
# module path, so the script refuses to choose a version.
set -euo pipefail

rev="${1:-HEAD}"
pathspec=(-- sdk ':(exclude,glob)sdk/**/*.md')

# Drain the pipe so git cannot fail with SIGPIPE under pipefail.
latest="$(git tag -l 'sdk/v*' --merged "$rev" --sort=-version:refname | sed -n '1p')"
if [ -z "$latest" ]; then
	echo "sdk/v0.1.0"
	exit 0
fi

range="${latest}..${rev}"
if [ -z "$(git rev-list "$range" "${pathspec[@]}")" ]; then
	exit 0
fi

log="$(git log --format='%s%n%b' "$range" "${pathspec[@]}")"
IFS=. read -r major minor patch <<< "${latest#sdk/v}"

if grep -qE '^[a-z]+(\([^)]*\))?!:|^BREAKING[ -]CHANGE:' <<< "$log"; then
	if [ "$major" -ne 0 ]; then
		echo "sdk-next-tag: breaking SDK change after ${latest} requires a new /v$((major + 1)) module path" >&2
		exit 1
	fi
	minor=$((minor + 1))
	patch=0
elif grep -qE '^feat(\([^)]*\))?:' <<< "$log"; then
	minor=$((minor + 1))
	patch=0
else
	patch=$((patch + 1))
fi
echo "sdk/v${major}.${minor}.${patch}"
