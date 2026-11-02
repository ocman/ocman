#!/usr/bin/env bash
# Print the tool versions pinned for local development so CI installs the
# same ones: node and hugo from mise.toml, pnpm from frontend/package.json's
# packageManager. Appends them to $GITHUB_OUTPUT when it is set.
#
#   scripts/ci-tool-versions.sh [repo-root]
set -euo pipefail

root="${1:-$(cd "$(dirname "$0")/.." && pwd)}"

mise_version() {
	sed -n "s/^$1 = \"\([^\"]*\)\"\$/\1/p" "$root/mise.toml"
}

node="$(mise_version node)"
hugo="$(mise_version hugo)"
pnpm="$(sed -n 's/^ *"packageManager": "pnpm@\([^"+]*\).*$/\1/p' "$root/frontend/package.json")"

for pair in "node=$node" "hugo=$hugo" "pnpm=$pnpm"; do
	if [ -z "${pair#*=}" ]; then
		echo "ci-tool-versions: no ${pair%%=*} version found" >&2
		exit 1
	fi
done

versions="$(printf 'node=%s\nhugo=%s\npnpm=%s\n' "$node" "$hugo" "$pnpm")"
printf '%s\n' "$versions"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
	printf '%s\n' "$versions" >>"$GITHUB_OUTPUT"
fi
