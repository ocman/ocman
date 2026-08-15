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
# matches `make test-coverage`) with cross-package instrumentation so
# integration tests credit the internal packages they exercise, and
# -covermode=atomic so it stays consistent with `make test-race`.
# Generated files (protobuf/gRPC
# *.pb.go) are excluded from the profile before computing the total —
# they are thousands of untested auto-generated statements that would
# otherwise distort the ratchet without measuring anything meaningful.
#
# Frontend coverage uses the @vitest/coverage-v8 provider configured in
# frontend/vitest.config.ts (json-summary reporter -> total.lines.pct).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

SUITE="${1:-all}"

# Floor on the number of testable ./internal/... packages. A silently
# shorter list under-reports the total and fails the ratchet for the
# wrong reason; bump this when packages are added, never lower it to
# make a run pass.
MIN_GO_TEST_PACKAGES="${MIN_GO_TEST_PACKAGES:-27}"

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
	# Go 1.26 invokes an unavailable covdata tool for packages without test files.
	#
	# The list is assigned before use on purpose: `set -e` does not fire on
	# a command substitution used as an argument, so a partially failing
	# `go list` silently dropped packages, `go test` still exited 0, and the
	# total came out points lower than reality. A separate `local` keeps the
	# assignment's own exit status, which `local pkgs="$(...)"` would mask.
	local pkgs
	pkgs="$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./internal/...)"
	local count
	count="$(printf '%s\n' "$pkgs" | grep -c .)"
	echo "    packages under test: ${count}"
	if [ "$count" -lt "$MIN_GO_TEST_PACKAGES" ]; then
		echo "coverage-collect: only ${count} testable Go packages found, expected at least ${MIN_GO_TEST_PACKAGES}; refusing to report an under-measured total" >&2
		exit 1
	fi
	# shellcheck disable=SC2086 # word splitting is the point
	go test $pkgs -count=1 -coverpkg=./internal/... -coverprofile=coverage/go.raw.out -covermode=atomic
	# Drop generated files (e.g. *.pb.go) from the profile; the mode
	# header line (first line) is preserved.
	grep -vE '\.pb\.go:' coverage/go.raw.out > coverage/go.out
	rm -f coverage/go.raw.out
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
