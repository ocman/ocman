#!/usr/bin/env bash
# Fetches the coverage baseline from the gh-pages branch into a local
# directory for `make coverage-check`. A missing gh-pages branch or
# missing files is fine — coverage-ratchet treats that as a pass
# (spec/ci-coverage-ratchet R5).
#
# Usage: scripts/coverage-fetch-baseline.sh <DEST_DIR>

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

DEST="${1:-baseline}"
mkdir -p "$DEST"

if ! git fetch --depth=1 origin gh-pages >/dev/null 2>&1; then
	echo "fetch-baseline: gh-pages not found; baseline empty (first run)"
	exit 0
fi

# Extract just the coverage JSON files from the fetched ref.
for f in go.json frontend.json; do
	if git cat-file -e "origin/gh-pages:coverage/${f}" 2>/dev/null; then
		git show "origin/gh-pages:coverage/${f}" > "${DEST}/${f}"
		echo "fetch-baseline: got coverage/${f}"
	else
		echo "fetch-baseline: coverage/${f} absent on gh-pages"
	fi
done

exit 0
