#!/usr/bin/env bash
# Coverage ratchet: compares the freshly collected coverage/*.json
# against a baseline directory and fails if any suite's total line
# coverage dropped beyond the tolerance grace.
#
# Usage:
#   scripts/coverage-ratchet.sh <BASELINE_DIR> [suite ...]
#   (no suite args == check both go and frontend)
#
# Rules (spec/ci-coverage-ratchet):
#   - Tolerance: a suite FAILS only when new < old - 0.1 (grace absorbs
#     v8 / Go atomic counting jitter).
#   - A missing/unreadable baseline file is a PASS for that suite
#     (first run, fresh branch, retention n/a here).
#
# Side effect: writes coverage/ratchet-result.json for the CI
# PR-comment step:
#   { "overall": "pass|fail",
#     "suites": [ { "name", "old", "new", "delta", "status" }, ... ] }

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BASELINE_DIR="${1:-}"
TOLERANCE="0.1"

if [ -z "$BASELINE_DIR" ]; then
	echo "usage: $0 <BASELINE_DIR> [suite ...]" >&2
	exit 2
fi
shift

read_pct() {
	# $1=json file -> prints pct or empty string
	[ -f "$1" ] || return 0
	node -e 'try{const fs=require("fs");process.stdout.write(String(JSON.parse(fs.readFileSync(process.argv[1],"utf8")).pct))}catch(e){}' "$1" 2>/dev/null || true
}

# Suites to check: explicit args, else both.
if [ "$#" -gt 0 ]; then
	SUITES=("$@")
else
	SUITES=("go" "frontend")
fi
OVERALL="pass"
RESULT_ITEMS=()

printf '%-10s %8s %8s %8s   %s\n' "SUITE" "OLD" "NEW" "DELTA" "STATUS"
printf '%s\n' "------------------------------------------------------"

for suite in "${SUITES[@]}"; do
	new_pct="$(read_pct "coverage/${suite}.json")"
	old_pct="$(read_pct "${BASELINE_DIR}/${suite}.json")"

	if [ -z "$new_pct" ]; then
		# No current coverage for this suite -> skip (treat as pass).
		printf '%-10s %8s %8s %8s   %s\n' "$suite" "${old_pct:--}" "-" "-" "SKIP (no current)"
		RESULT_ITEMS+=("{\"name\":\"$suite\",\"old\":${old_pct:-null},\"new\":null,\"delta\":null,\"status\":\"skip\"}")
		continue
	fi

	if [ -z "$old_pct" ]; then
		printf '%-10s %8s %8s %8s   %s\n' "$suite" "-" "$new_pct" "-" "PASS (no baseline)"
		RESULT_ITEMS+=("{\"name\":\"$suite\",\"old\":null,\"new\":$new_pct,\"delta\":null,\"status\":\"pass\"}")
		continue
	fi

	# delta = new - old ; fail when new < old - tolerance
	read -r delta status <<EOF
$(awk -v n="$new_pct" -v o="$old_pct" -v t="$TOLERANCE" 'BEGIN{
	d = n - o;
	if (n < o - t) print d" fail"; else print d" pass";
}')
EOF

	printf '%-10s %8s %8s %+8.2f   %s\n' "$suite" "$old_pct" "$new_pct" "$delta" "$(echo "$status" | tr '[:lower:]' '[:upper:]')"
	RESULT_ITEMS+=("{\"name\":\"$suite\",\"old\":$old_pct,\"new\":$new_pct,\"delta\":$delta,\"status\":\"$status\"}")
	if [ "$status" = "fail" ]; then
		OVERALL="fail"
	fi
done

# Emit machine-readable result for the PR-comment step.
{
	printf '{ "overall": "%s", "tolerance": %s, "suites": [' "$OVERALL" "$TOLERANCE"
	first=1
	for item in "${RESULT_ITEMS[@]}"; do
		if [ "$first" -eq 1 ]; then first=0; else printf ','; fi
		printf '%s' "$item"
	done
	printf '] }\n'
} > coverage/ratchet-result.json

printf '%s\n' "------------------------------------------------------"
echo "Overall: $(echo "$OVERALL" | tr '[:lower:]' '[:upper:]')  (tolerance ${TOLERANCE}%)"

[ "$OVERALL" = "pass" ]
