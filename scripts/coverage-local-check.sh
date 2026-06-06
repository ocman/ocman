#!/usr/bin/env bash
# Local coverage ratchet for the pre-push git hook (make install-hooks).
# Collects coverage for a single suite, fetches the gh-pages baseline,
# and fails if that suite dropped beyond the grace tolerance.
#
# Usage:
#   scripts/coverage-local-check.sh <go|frontend|all>
#   (no arg == all)
#
# Split per-suite so the pre-commit `files:` filter can run only the
# side that changed: editing Go won't trigger the (slower) frontend
# coverage run and vice-versa. `make test` itself stays coverage-free
# and offline; this only runs at push time.
#
# A missing gh-pages baseline (first run, offline) is treated as a pass
# by coverage-ratchet.sh, so this never blocks a push spuriously.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

SUITE="${1:-all}"
case "$SUITE" in
	go|frontend|all) ;;
	*) echo "usage: $0 <go|frontend|all>" >&2; exit 2 ;;
esac

# Which suite names to pass to the ratchet check.
if [ "$SUITE" = "all" ]; then
	CHECK_SUITES=(go frontend)
else
	CHECK_SUITES=("$SUITE")
fi

BASELINE_DIR="$(mktemp -d)"
trap 'rm -rf "$BASELINE_DIR"' EXIT

echo "==> Collecting coverage ($SUITE)"
./scripts/coverage-collect.sh "$SUITE"

echo "==> Fetching baseline from gh-pages"
./scripts/coverage-fetch-baseline.sh "$BASELINE_DIR"

echo "==> Checking ratchet ($SUITE)"
./scripts/coverage-ratchet.sh "$BASELINE_DIR" "${CHECK_SUITES[@]}"
