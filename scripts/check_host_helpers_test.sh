#!/usr/bin/env bash
# Self-test for check-host-helpers.sh.
#
# The guard rotted silently: 13 of its 15 patterns matched identifiers
# that no longer existed anywhere in the repo (the packages had been
# renamed gitinfo->git and the term helpers moved), so it printed
# "no naked host-helper calls" unconditionally while a real bypass sat
# in handlers_tmux.go. A guard that cannot fail is worse than no guard,
# so prove it still fails on a planted bypass.
#
# Exit code: 0 on pass, 1 on failure.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ripgrep is not preinstalled on every CI image. Without this check the
# `rg ... || true` calls below swallow exit 127 and the guard passes
# vacuously — the exact silent rot this guard exists to prevent.
command -v rg >/dev/null 2>&1 || {
	echo "check_host_helpers_test: ripgrep (rg) is required but not installed" >&2
	exit 1
}

GUARD="$ROOT/scripts/check-host-helpers.sh"
PLANT="$ROOT/internal/server/zz_host_helper_probe.go"

fail() { echo "check-host-helpers self-test: $1" >&2; exit 1; }
cleanup() { rm -f "$PLANT"; }
trap cleanup EXIT

# 1. The repo is currently clean.
if ! "$GUARD" >/dev/null 2>&1; then
	fail "the guard reports violations on a clean tree"
fi

# 2. Every pattern must name a function that still exists, otherwise it
#    is dead weight that can never fire again. This is the exact rot that
#    happened: gitinfo.GetDiff and friends were renamed to git.GetDiff,
#    and the patterns kept matching nothing. Check the *definition*, not a
#    call site — a pattern legitimately covers helpers nobody calls yet.
patterns=$(sed -n '/^PATTERNS=(/,/^)/p' "$GUARD" | sed -n 's/^\t"\(.*\)"$/\1/p')
[ -n "$patterns" ] || fail "could not extract PATTERNS from the guard"
while IFS= read -r pattern; do
	ident=$(printf '%s' "$pattern" | sed 's/\\\\//g; s/\[A-Za-z\]\*//; s/($//; s/(//')
	case "$ident" in
	*.*)
		pkg="${ident%%.*}"
		fn="${ident#*.}"
		if [ ! -d "$ROOT/internal/$pkg" ]; then
			fail "pattern '$pattern' names package internal/$pkg, which does not exist"
		fi
		if ! rg -q --no-messages -g '*.go' "^func $fn\\(" "$ROOT/internal/$pkg"; then
			fail "pattern '$pattern': internal/$pkg has no exported func $fn — the pattern is dead"
		fi
		;;
	*)
		if ! rg -q --no-messages -g 'internal/**/*.go' "func .*$ident\\(" "$ROOT"; then
			fail "pattern '$pattern' names no func in internal/ — the pattern is dead"
		fi
		;;
	esac
done <<< "$patterns"

# 3. A planted bypass must be caught.
cat > "$PLANT" <<'GO'
package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/git"
)

func hostHelperProbe(ctx context.Context, dir string) {
	_, _ = git.ListWorktrees(ctx, dir)
}
GO
if "$GUARD" >/dev/null 2>&1; then
	fail "the guard did not catch a planted git.ListWorktrees bypass"
fi

echo "check-host-helpers self-test: guard is clean, all patterns live, planted bypass caught."
