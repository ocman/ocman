#!/usr/bin/env bash
# Fails when a `var(--x)` in frontend/src names a custom property that is
# defined nowhere. An undefined token silently falls back to inherited
# styles, which is how off-theme UI gets in (`--text-muted`, `--surface`).
#
# A property counts as defined when frontend/src declares it in CSS
# (`--x:`), or sets it from TS/TSX (`'--x'` as a style key or
# setProperty argument). Third-party properties set by libraries are
# listed in EXTERNAL below.
#
# Exit code: 0 on clean, 1 on violations.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${1:-$ROOT/frontend/src}"

command -v rg >/dev/null 2>&1 || {
	echo "check-css-tokens: ripgrep (rg) is required but not installed" >&2
	exit 1
}

# Properties defined outside frontend/src (libraries inject them at runtime).
EXTERNAL='^--(radix-|xy-|diffs-|trees-)'

defined=$(
	{
		rg -o --no-filename --no-messages -g '*.css' '(--[A-Za-z0-9_-]+)\s*:' -r '$1' "$SRC" || true
		rg -o --no-filename --no-messages -g '*.{ts,tsx}' "[\"'\`](--[A-Za-z0-9_-]+)[\"'\`]" -r '$1' "$SRC" || true
	} | sort -u
)

offenders=$(
	# Comment lines are skipped so prose like "e.g. `var(--my-agent)`" passes.
	(rg -n --no-messages -g '*.{css,ts,tsx}' -g '!*.test.*' 'var\(\s*--' "$SRC" || true) |
		grep -vE '^[^:]+:[0-9]+:\s*(//|/?\*)' |
		while IFS= read -r line; do
			loc="${line%%:*}:$(cut -d: -f2 <<<"$line")"
			for name in $(grep -oE 'var\(\s*--[A-Za-z0-9_-]+' <<<"$line" | sed -E 's/var\(\s*//'); do
				[[ "$name" =~ $EXTERNAL ]] && continue
				grep -qxF -- "$name" <<<"$defined" || echo "$loc:$name"
			done
		done
)

if [[ -n "$offenders" ]]; then
	echo "Undefined CSS custom properties (add the token to frontend/src/tokens.css or use an existing one):" >&2
	echo "$offenders" | sed "s|^$ROOT/||" >&2
	exit 1
fi

echo "check-css-tokens: every var() names a defined custom property."
