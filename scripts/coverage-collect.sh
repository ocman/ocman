#!/usr/bin/env bash
# Collects total line coverage for one or both test suites and writes
# machine-readable summaries used by the coverage ratchet
# (spec/ci-coverage-ratchet).
#
# Usage:
#   scripts/coverage-collect.sh [go|frontend|all]
#   (no arg == all)
#
# Outputs (relative to repo root):
#   coverage/go.json        { "pct": <n>, "sha": "<commit>", "ts": "<iso8601>" }
#   coverage/frontend.json  { "pct": <n>, "sha": "<commit>", "ts": "<iso8601>" }
#
# Go coverage is measured across ./internal/... (the meaningful code;
# matches `make test-coverage`) with -covermode=atomic so it stays
# consistent with `make test-race`.
#
# Frontend coverage uses the @vitest/coverage-v8 provider configured in
# frontend/vitest.config.ts (json-summary reporter -> total.lines.pct).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

SUITE="${1:-all}"

mkdir -p coverage

SHA="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

write_json() {
	# $1=file $2=pct
	cat > "$1" <<EOF
{ "pct": $2, "sha": "$SHA", "ts": "$TS" }
EOF
}

collect_go() {
	echo "==> Go coverage (./internal/...)"
	go test ./internal/... -coverprofile=coverage/go.out -covermode=atomic
	local pct
	pct="$(go tool cover -func=coverage/go.out | awk '/^total:/ {gsub("%","",$3); print $3}')"
	if [ -z "${pct:-}" ]; then
		echo "coverage-collect: failed to parse Go total coverage" >&2
		exit 1
	fi
	write_json coverage/go.json "$pct"
	echo "    Go total: ${pct}%"
}

collect_frontend() {
	echo "==> Frontend coverage (vitest v8)"
	( cd frontend && pnpm test:coverage )
	local summary="frontend/coverage/coverage-summary.json"
	if [ ! -f "$summary" ]; then
		echo "coverage-collect: $summary not found (is @vitest/coverage-v8 configured?)" >&2
		exit 1
	fi
	local pct
	pct="$(node -e "process.stdout.write(String(require('./$summary').total.lines.pct))")"
	if [ -z "${pct:-}" ]; then
		echo "coverage-collect: failed to parse frontend total coverage" >&2
		exit 1
	fi
	write_json coverage/frontend.json "$pct"
	echo "    Frontend total: ${pct}%"
}

case "$SUITE" in
	go)       collect_go ;;
	frontend) collect_frontend ;;
	all)      collect_go; collect_frontend ;;
	*)
		echo "usage: $0 [go|frontend|all]" >&2
		exit 2
		;;
esac
