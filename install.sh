#!/usr/bin/env bash
# ocman installer + service manager.
#
#   curl -fsSL https://forgejo.nousefreak.be/dries/ocman/raw/branch/main/install.sh | bash -s -- install
#
# After install the same script lives at ~/.local/bin/ocman-ctl:
#   ocman-ctl start|stop|restart|status|logs|update|uninstall|doctor
#
# ponytail: nohup + pidfile, one code path for macOS and Linux. No launchd /
# systemd unit — add one when surviving a reboot actually matters.
set -euo pipefail

REPO="${OCMAN_REPO:-https://forgejo.nousefreak.be/dries/ocman.git}"
BRANCH="${OCMAN_BRANCH:-main}"
PREFIX="${OCMAN_PREFIX:-$HOME/.local}"
SRC="${OCMAN_SRC:-$PREFIX/share/ocman/src}"
BIN="${OCMAN_BIN:-$PREFIX/bin/ocman}"
CTL="$PREFIX/bin/ocman-ctl"
RUN="${OCMAN_RUN:-${XDG_STATE_HOME:-$HOME/.local/state}/ocman}"
PIDFILE="$RUN/ocman.pid"
LOG="$RUN/ocman.log"
ADDR="${OCMAN_ADDR:-127.0.0.1:8228}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m warn\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror\033[0m %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------- doctor -----

doctor() {
	local missing=0
	case "$(uname -s)" in
	Darwin | Linux) ;;
	*) die "unsupported OS: $(uname -s) (macOS and Linux only)" ;;
	esac

	req() { # req <cmd> <how-to-install>
		if have "$1"; then
			printf '  ok      %-8s %s\n' "$1" "$(command -v "$1")"
		else
			printf '  MISSING %-8s -> %s\n' "$1" "$2"
			missing=1
		fi
	}
	opt() {
		if have "$1"; then
			printf '  ok      %-8s %s\n' "$1" "$(command -v "$1")"
		else
			printf '  absent  %-8s (optional) %s\n' "$1" "$2"
		fi
	}

	info "checking dependencies"
	req git "https://git-scm.com/downloads"
	req go "https://go.dev/dl/ (GOTOOLCHAIN handles the exact version)"
	req node "mise install node, or https://nodejs.org"
	req pnpm "npm install -g pnpm, or corepack enable pnpm"
	opt tmux "needed to launch OpenCode sessions from ocman"
	opt opencode "the agent ocman drives: https://opencode.ai"
	opt lsof "used to discover externally started OpenCode instances"

	[ "$missing" -eq 0 ] || die "install the missing tools above, then re-run"

	case ":$PATH:" in
	*":$PREFIX/bin:"*) ;;
	*) warn "$PREFIX/bin is not on your PATH — add it to your shell profile" ;;
	esac
}

# ----------------------------------------------------------------- build -----

fetch_src() {
	mkdir -p "$(dirname "$SRC")"
	if [ -d "$SRC/.git" ]; then
		info "updating $SRC"
		git -C "$SRC" fetch --depth 1 origin "$BRANCH"
		git -C "$SRC" reset --hard "origin/$BRANCH"
	else
		info "cloning $REPO -> $SRC"
		git clone --depth 1 --branch "$BRANCH" "$REPO" "$SRC"
	fi
}

build() {
	info "building frontend"
	(cd "$SRC/frontend" && pnpm install --frozen-lockfile && pnpm build)
	info "building binary"
	mkdir -p "$(dirname "$BIN")"
	(cd "$SRC" && go build -o "$BIN" .)
	install -m 0755 "$SRC/install.sh" "$CTL"
	info "installed $BIN and $CTL"
}

# --------------------------------------------------------------- service -----

running_pid() {
	[ -f "$PIDFILE" ] || return 1
	local pid
	pid=$(cat "$PIDFILE" 2>/dev/null) || return 1
	[ -n "$pid" ] || return 1
	kill -0 "$pid" 2>/dev/null || return 1
	# Guard against pid reuse: a recycled pid must not get SIGTERM from stop().
	ps -p "$pid" -o args= 2>/dev/null | grep -qF "$BIN" || return 1
	printf '%s' "$pid"
}

start() {
	local pid
	if pid=$(running_pid); then
		info "already running (pid $pid) on http://$ADDR"
		return 0
	fi
	[ -x "$BIN" ] || die "$BIN not found — run '$0 install' first"
	mkdir -p "$RUN"
	nohup "$BIN" -addr "$ADDR" >>"$LOG" 2>&1 &
	echo $! >"$PIDFILE"
	sleep 1
	if pid=$(running_pid); then
		info "started (pid $pid) — http://$ADDR  logs: $LOG"
	else
		rm -f "$PIDFILE"
		tail -n 20 "$LOG" >&2 || true
		die "failed to start; see $LOG"
	fi
}

stop() {
	local pid
	if ! pid=$(running_pid); then
		rm -f "$PIDFILE"
		info "not running"
		return 0
	fi
	kill "$pid" 2>/dev/null || true
	for _ in $(seq 20); do
		kill -0 "$pid" 2>/dev/null || break
		sleep 0.25
	done
	kill -9 "$pid" 2>/dev/null || true
	rm -f "$PIDFILE"
	info "stopped (pid $pid)"
}

status() {
	local pid
	if pid=$(running_pid); then
		info "running (pid $pid) on http://$ADDR"
	else
		info "stopped"
		return 1
	fi
}

# --------------------------------------------------------------- actions -----

cmd_install() {
	doctor
	fetch_src
	build
	start
}

cmd_update() {
	fetch_src
	build
	stop
	start
}

cmd_uninstall() {
	stop
	rm -f "$BIN" "$LOG" "$PIDFILE"
	rm -rf "$SRC"
	info "removed binary, sources and logs"
	if [ "${1:-}" = "--purge" ]; then
		rm -rf "${XDG_DATA_HOME:-$HOME/.local/share}/ocman"
		info "purged ocman data (state.db included)"
	else
		info "kept your data in ~/.local/share/ocman — re-run with --purge to delete it"
	fi
	rm -f "$CTL"
}

usage() {
	cat <<EOF
ocman-ctl <command>

  install              check deps, build from source, start in the background
  update               pull, rebuild, restart
  start | stop | restart | status
  logs                 tail -f the service log
  uninstall [--purge]  stop and remove (--purge also deletes state.db)
  doctor               dependency check only

Environment: OCMAN_ADDR (default $ADDR), OCMAN_PREFIX, OCMAN_SRC,
OCMAN_BRANCH, OCMAN_REPO, OCMAN_RUN
EOF
}

case "${1:-help}" in
install) cmd_install ;;
update) cmd_update ;;
start) start ;;
stop) stop ;;
restart)
	stop
	start
	;;
status) status ;;
logs)
	mkdir -p "$RUN"
	touch "$LOG"
	tail -f "$LOG"
	;;
uninstall) cmd_uninstall "${2:-}" ;;
doctor) doctor ;;
help | -h | --help) usage ;;
*)
	usage >&2
	die "unknown command: $1"
	;;
esac
