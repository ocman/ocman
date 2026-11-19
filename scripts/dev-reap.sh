#!/bin/bash
# Stop dev processes of THIS checkout that outlived the dev.sh that started
# them. Run by `make kill-dev` and at every dev.sh start.
#
# A dev.sh killed with SIGKILL (or one from before process-group shutdown)
# cannot clean up, and its jobs run in their own sessions, so nothing else ever
# signals them. They are found two ways:
#   - the process groups dev.sh recorded in tmp/dev.pgids, which also catch
#     children whose parent already exited (pnpm exec -> node);
#   - command patterns for the dev scripts and frontend watchers.
# Only processes whose working directory is inside this checkout are touched,
# so other worktrees keep running, and our own ancestors (make, the calling
# dev.sh) are spared.

set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
PGIDS="$ROOT/tmp/dev.pgids"
PATTERN='scripts/dev\.sh( |$)|watch-frontend-prod\.sh|(tsrs|tsc) -b --watch|vite(\.js)? build --watch|tail -n \+1 -F tmp/'

ancestors() {
	local p=$$
	while [ -n "$p" ] && [ "$p" -gt 1 ]; do
		echo "$p"
		p=$(ps -o ppid= -p "$p" 2>/dev/null | tr -d ' ')
	done
}

cwd_of() {
	if [ -e "/proc/$1/cwd" ]; then
		readlink "/proc/$1/cwd"
	else
		lsof -a -d cwd -Fn -p "$1" 2>/dev/null | sed -n 's/^n//p'
	fi
}

targets() {
	local skip p g cwd
	skip=" $(ancestors | tr '\n' ' ') "
	{
		pgrep -f "$PATTERN"
		if [ -f "$PGIDS" ]; then
			while read -r g; do
				case "$g" in '' | *[!0-9]*) continue ;; esac
				[ "$g" -gt 1 ] && pgrep -g "$g"
			done < "$PGIDS"
		fi
	} 2>/dev/null | sort -un | while read -r p; do
		case "$skip" in *" $p "*) continue ;; esac
		cwd=$(cwd_of "$p")
		case "$cwd" in "$ROOT" | "$ROOT"/*) echo "$p" ;; esac
	done
}

victims=$(targets)
if [ -z "$victims" ]; then
	rm -f "$PGIDS"
	exit 0
fi

echo "Stopping stale dev processes in $ROOT: $(echo "$victims" | tr '\n' ' ')"
# shellcheck disable=SC2086
kill -TERM $victims 2>/dev/null
# A stale dev.sh's own shutdown takes about 3s; let it finish before it can
# reclaim ports that the next run is about to bind.
for _ in $(seq 20); do
	alive=""
	for p in $victims; do kill -0 "$p" 2>/dev/null && alive="$alive $p"; done
	[ -n "$alive" ] || break
	sleep 0.25
done
# shellcheck disable=SC2046,SC2086
kill -KILL $alive $(targets) 2>/dev/null
rm -f "$PGIDS"
exit 0
