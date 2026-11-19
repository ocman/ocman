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
#   - bare `"${NEW_SESSION[@]}" cmd &` (no function, pipe or subshell wrapper)
#     so `$!` is the REAL pid, which is also the job's process group id. A
#     shell function here would fork a dev.sh subshell in OUR group: a terminal
#     hangup kills it, and its job is left in its own session, out of reach.
#   - a `sleep 1` poll loop as the foreground blocker: it notices any job
#     exiting (macOS bash 3.2 has no `wait -n`). Bash runs a trap only after
#     the current foreground command returns, so keep that command short.
#   - the trap ignores further signals (no re-entry, no half-done cleanup on a
#     second Ctrl+C), then TERMs each job's process group + descendant tree
#     (air -> compiled binary; pnpm -> node/vite; watcher -> tsc/vite), waits >
#     air's 2s kill_delay, KILLs stragglers, and reclaims the ports.
#   - each group id is recorded in tmp/dev.pgids. A SIGKILLed dev.sh runs no
#     trap, so scripts/dev-reap.sh (run at start and by `make kill-dev`) stops
#     what an earlier run left behind.

set -u

MODE="${1:-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p tmp

# Prefix for running a command in a new session so it can't steal the
# controlling terminal. Prefer the setsid binary (Linux); fall back to python3
# (macOS has no setsid). Both exec the command in place, keeping its pid.
if command -v setsid >/dev/null 2>&1; then
	NEW_SESSION=(setsid)
else
	NEW_SESSION=(python3 -c 'import os,sys; os.setsid(); os.execvp(sys.argv[1], sys.argv[1:])')
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
groups=""

# Track the job just started with `"${NEW_SESSION[@]}" cmd &`: its pid is also
# its process group id.
track_job() {
	pids="$pids $1"
	groups="$groups $1"
	echo "$1" >> tmp/dev.pgids
}

# Signal every tracked process group, pid and descendant. The group reaches
# children whose parent already exited; descendants catch any that left it.
signal_all() {
	local all="" p
	for p in $pids; do all="$all $p $(descendants "$p")"; done
	for p in $groups; do all="$all -$p"; done
	# shellcheck disable=SC2086
	kill -"$1" -- $all 2>/dev/null
}

stop() {
	trap '' INT TERM HUP
	trap - EXIT
	signal_all TERM
	sleep 3
	signal_all KILL
	rm -f tmp/dev.pgids
	lsof -tiTCP:8228 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null
	lsof -tiTCP:8229 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null
	lsof -tiTCP:8230 -sTCP:LISTEN 2>/dev/null | xargs kill -9 2>/dev/null
	exit 0
}

case "$MODE" in
	dev)             fe="pnpm dev";     felog="tmp/vite-dev.log" ;;
	prod|prod-watch) fe="pnpm preview"; felog="tmp/vite-preview.log" ;;
	remote)          fe="";             felog="" ;;
	*) echo "usage: dev.sh [dev|prod|prod-watch|remote]" >&2; exit 2 ;;
esac

# Stop whatever an earlier run of this checkout left behind before reusing
# its ports and log files.
./scripts/dev-reap.sh
: > tmp/dev.pgids
trap stop INT TERM HUP EXIT

: > tmp/air.log

if [ "$MODE" = "remote" ]; then
	"${NEW_SESSION[@]}" env OTEL_EXPORTER_OTLP_ENDPOINT= air --build.full_bin "./tmp/ocman -addr 127.0.0.1:8229 -remote-listen 0.0.0.0:8230" < /dev/null >> tmp/air.log 2>&1 &
else
	"${NEW_SESSION[@]}" air < /dev/null >> tmp/air.log 2>&1 &
fi
track_job $!

logs="tmp/air.log"

if [ -n "$fe" ]; then
	: > "$felog"
	"${NEW_SESSION[@]}" sh -c "cd frontend && exec $fe" < /dev/null >> "$felog" 2>&1 &
	track_job $!
	logs="$logs $felog"
fi

if [ "$MODE" = "prod-watch" ]; then
	: > tmp/frontend-watch.log
	"${NEW_SESSION[@]}" ./scripts/watch-frontend-prod.sh < /dev/null >> tmp/frontend-watch.log 2>&1 &
	track_job $!
	logs="$logs tmp/frontend-watch.log"
fi

# Watch the owner as well as tail. A forced stop cannot run stop(), and an
# orphan tail would otherwise keep printing the next run's shared log files.
stream_logs() {
	local owner=$1 reader
	shift
	tail -n +1 -F "$@" &
	reader=$!
	trap 'kill "$reader" 2>/dev/null; wait "$reader" 2>/dev/null' EXIT
	trap 'exit 0' INT TERM
	while kill -0 "$owner" 2>/dev/null && kill -0 "$reader" 2>/dev/null; do
		sleep 1
	done
	exit 0
}
# shellcheck disable=SC2086
stream_logs "$$" $logs &
pids="$pids $!"

# Foreground blocker: poll until a job dies. A signal's trap runs once the
# current `sleep 1` returns.
while :; do
	for p in $pids; do
		kill -0 "$p" 2>/dev/null || exit 0
	done
	sleep 1
done
