#!/usr/bin/env bash
# Fails CI when the frontend or any shared code starts branching on a
# specific platform identifier — the anti-pattern that this multi-agent
# architecture is explicitly designed to prevent (architecture.md §R3).
#
# Use the capability flags from /api/capabilities (via
# useCapabilities / usePlatformCapabilities) instead. If you really
# need to special-case one platform, suppress the guard with an
# explicit `// ocman:allow-platform-branch` pragma on the same line —
# but expect to justify it in review.
#
# Exit code: 0 on clean, 1 on violations.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ripgrep is not preinstalled on every CI image. Without this check the
# `rg ... || true` calls below swallow exit 127 and the guard passes
# vacuously — the exact silent rot this guard exists to prevent.
command -v rg >/dev/null 2>&1 || {
	echo "check-platform-branching: ripgrep (rg) is required but not installed" >&2
	exit 1
}

# Look for:
#   session.platform === 'xxx'
#   session?.platform === "xxx"
#   .platform === 'xxx'    (catches whatever variable name holds a Session)
#
# We scan the frontend and shared Go code only; adapters by their nature
# DO know their platform identity, so internal/platforms/ is excluded.
PATTERNS=(
	"\\.platform\\s*===\\s*['\"]"
	"\\.platform\\s*!==\\s*['\"]"
)

FOUND=0
for pattern in "${PATTERNS[@]}"; do
	# --no-messages keeps ripgrep quiet when a scanned dir is empty;
	# --glob excludes files we explicitly allow (tests, docs, this script).
	matches=$(
		rg -n --no-messages \
			--glob 'frontend/src/**' \
			--glob '!**/*.test.ts' \
			--glob '!**/*.test.tsx' \
			"$pattern" "$ROOT" || true
	)
	if [[ -n "$matches" ]]; then
		# Honour per-line suppression pragma.
		offenders=$(echo "$matches" | grep -v 'ocman:allow-platform-branch' || true)
		if [[ -n "$offenders" ]]; then
			echo "Platform-branching anti-pattern detected:" >&2
			echo "$offenders" >&2
			FOUND=1
		fi
	fi
done

if [[ $FOUND -eq 0 ]]; then
	echo "check-platform-branching: no platform-identity comparisons found."
fi

exit $FOUND
