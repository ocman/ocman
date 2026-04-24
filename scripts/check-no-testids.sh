#!/bin/bash
# Verify that the production frontend bundle contains no test-only attributes.
#
# `make build-frontend` runs Vite with STRIP_TESTIDS=1, which invokes
# babel-plugin-jsx-remove-data-test-id to drop data-testid / data-test /
# data-test-id attrs from JSX at build time. This script is the belt-and-suspenders
# check: if anything slips through (e.g. an attribute spelled via a spread,
# a template string, or a new attr name the plugin isn't configured for) we
# fail the build rather than ship them to users.
#
# Run from the repo root. Exits non-zero if any test attr is found in the
# built bundle.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUNDLE_DIR="$SCRIPT_DIR/../internal/server/static"

if [ ! -d "$BUNDLE_DIR" ]; then
	echo "check-no-testids: bundle dir not found: $BUNDLE_DIR" >&2
	echo "check-no-testids: did you run 'make build-frontend' first?" >&2
	exit 2
fi

# Match attribute names only (quoted, so we don't false-positive on prose).
# Patterns cover:
#   - HTML:  data-testid="...", data-test="...", data-test-id="..."
#   - JSX (not expected in a built bundle, but cheap insurance): same patterns.
# We scan every asset type Vite may emit: .js, .html, .css, .map, .json.
PATTERN='data-test(id|-id)?[="]'

# rg returns 1 when no matches, which is exactly what we want. Invert that.
# --no-ignore because internal/server/static/ is gitignored (it's build output).
# -c (count) + -o prints one line per file with a match count, keeping the
# output readable even when the offending line is a minified megabyte.
if rg -c --no-ignore \
	--glob '*.js' --glob '*.html' --glob '*.css' --glob '*.map' --glob '*.json' \
	"$PATTERN" "$BUNDLE_DIR"; then
	echo "" >&2
	echo "check-no-testids: FAIL — the production bundle contains test-only attributes." >&2
	echo "check-no-testids: ensure the build ran with STRIP_TESTIDS=1 and that" >&2
	echo "check-no-testids: the strip-test-ids plugin in vite.config.ts covers the attr names above." >&2
	exit 1
fi

echo "check-no-testids: OK — bundle is free of data-testid / data-test / data-test-id."
