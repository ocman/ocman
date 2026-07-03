#!/bin/bash
# Dev loop for ocman: run air (backend) plus optional frontend/watchers with a
# clean Ctrl+C. Invoked via `make dev` / `dev-prod` / `dev-prod-watch` /
# `dev-remote`.
#
# Mode (arg 1): dev | prod | prod-watch | remote
#
# The non-obvious bit that makes Ctrl+C work: each job runs in its OWN session
# (`new_session`, i.e. setsid). Without it, vite/node call tcsetpgrp() to grab
# the controlling terminal's foreground process group, which STEALS Ctrl+C —
# the tty then delivers SIGINT to vite's group instead of ours, this script's
# trap never fires, and nothing gets reaped (the "Ctrl+C does nothing in tmux"
# bug). Detaching each job from the tty keeps US as the terminal's foreground
# group, so Ctrl+C reaches the trap.
#
# The rest:
#   - `< /dev/null` per job so a job that reads the tty doesn't SIGTTIN-suspend
#     the run on a keypress.
#   - bare `cmd & pid=$!` (no pipe/subshell wrapper) so `$!` is the REAL pid.
#   - a `sleep` poll loop (not `wait`) as the foreground blocker: bash defers
#     traps while blocked in `wait`, but a trapped signal interrupts `sleep`.
#   - the trap clears itself (no re-entry), then TERMs each pid + its full
#     descendant tree (air -> compiled binary; pnpm -> node/vite; watcher ->
#     fswatch -> subshell), waits > air's 2s kill_delay, KILLs stragglers, and
#     reclaims the ports.

set -u

MODE="${1:-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p tmp

# Run "$@" in a new session so it can't steal the controlling terminal.
# Prefer the setsid binary (Linux); fall back to python3 (macOS has no setsid).
if command -v setsid >/dev/null 2>&1; then
	new_session() { setsid "$@"; }
else
	new_session() { python3 -c 'import os,sys; os.setsid(); os.execvp(sys.argv[1], sys.argv[1:])' "$@"; }
fi

# Echo a pid and all its descendants, recursively.
descendants() {
	local p=$1 c
	for c in $(pgrep -P "$p" 2>/dev/null); do
		echo "$c"
		descendants "$c"
	done
}

pids=""

stop() {
	trap - INT TERM EXIT
	local all="" p
	for p in $pids; do all="$all $p $(descendants "$p")"; done
	# shellcheck disable=SC2086
	kill -TERM $all 2>/dev/null
	sleep 3
	all=""
	for p in $pids; do all="$all $p $(descendants "$p")"; done
	# shellcheck disable=SC2086
	kill -KILL $all 2>/dev/null
	lsof -tiTCP:8228 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null
	lsof -tiTCP:8229 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null
	lsof -tiTCP:8230 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null
	exit 0
}
trap stop INT TERM EXIT

case "$MODE" in
	dev)             fe="pnpm dev";     felog="tmp/vite-dev.log" ;;
	prod|prod-watch) fe="pnpm preview"; felog="tmp/vite-preview.log" ;;
	remote)          fe="";             felog="" ;;
	*) echo "usage: dev.sh [dev|prod|prod-watch|remote]" >&2; exit 2 ;;
esac

: > tmp/air.log

if [ "$MODE" = "remote" ]; then
	new_session env OTEL_EXPORTER_OTLP_ENDPOINT= air -c .air.remote.toml < /dev/null >> tmp/air.log 2>&1 &
else
	new_session air < /dev/null >> tmp/air.log 2>&1 &
fi
pids="$pids $!"

logs="tmp/air.log"

if [ -n "$fe" ]; then
	: > "$felog"
	new_session sh -c "cd frontend && exec $fe" < /dev/null >> "$felog" 2>&1 &
	pids="$pids $!"
	logs="$logs $felog"
fi

if [ "$MODE" = "prod-watch" ]; then
	: > tmp/frontend-watch.log
	new_session ./scripts/watch-frontend-prod.sh < /dev/null >> tmp/frontend-watch.log 2>&1 &
	pids="$pids $!"
	logs="$logs tmp/frontend-watch.log"
fi

# Background tail streams the logs to the terminal.
# shellcheck disable=SC2086
tail -n +1 -F $logs &
pids="$pids $!"

# Foreground blocker: poll until a job dies (or Ctrl+C interrupts the sleep and
# fires the trap). `wait` can't be used — bash defers traps until it returns,
# which never happens while the jobs are alive.
while :; do
	for p in $pids; do
		kill -0 "$p" 2>/dev/null || exit 0
	done
	sleep 1
done
