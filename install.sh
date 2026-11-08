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
SELECTION="${OCMAN_VERSION:-}"
PREFIX="${OCMAN_PREFIX:-$HOME/.local}"
SRC="${OCMAN_SRC:-$PREFIX/share/ocman/src}"
BIN="${OCMAN_BIN:-$PREFIX/bin/ocman}"
CTL="$PREFIX/bin/ocman-ctl"
RUN="${OCMAN_RUN:-${XDG_STATE_HOME:-$HOME/.local/state}/ocman}"
PIDFILE="$RUN/ocman.pid"
LOG="$RUN/ocman.log"
ADDR="${OCMAN_ADDR:-127.0.0.1:8228}"
API="https://api.github.com/repos/ocman/ocman/releases"
DOWNLOAD="https://github.com/ocman/ocman/releases/download"
VERSION_FILE="$PREFIX/share/ocman/install-version"
SCRIPT_URL="https://forgejo.nousefreak.be/dries/ocman/raw/branch/main/install.sh"
NO_START=0
SOURCE_ONLY=0

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
	req curl "https://curl.se/download.html"
	req tar "install tar with your package manager"
	opt tmux "needed to launch OpenCode sessions from ocman"
	opt opencode "the agent ocman drives: https://opencode.ai"

	[ "$missing" -eq 0 ] || die "install the missing tools above, then re-run"

	case ":$PATH:" in
	*":$PREFIX/bin:"*) ;;
	*) warn "$PREFIX/bin is not on your PATH — add it to your shell profile" ;;
	esac
}

# ----------------------------------------------------------------- build -----

valid_version() {
	[[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-(nightly\.[0-9]+|dev\.[0-9]+))?$ ]]
}

resolve_version() {
	local json tags page=1 endpoint="$API/latest"
	case "$SELECTION" in
	stable | nightly) ;;
	*)
		TAG="v${SELECTION#v}"
		valid_version "$TAG" || die "expected stable, nightly or a version such as v1.2.3"
		return ;;
	esac
	while :; do
		[ "$SELECTION" != nightly ] || endpoint="$API?per_page=100&page=$page"
		if ! json=$(curl -fsSL --connect-timeout 10 --max-time 60 "$endpoint"); then
			warn "GitHub lookup failed; resolving $SELECTION from source tags"
			break
		fi
		tags=$(printf '%s\n' "$json" | sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p')
		if [ "$SELECTION" = stable ]; then
			TAG="$tags"
			[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] && return
			break
		fi
		TAG=$(printf '%s\n' "$tags" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+-nightly\.[0-9]+$' | head -n 1) || true
		[ -z "$TAG" ] || return 0
		[ -n "$tags" ] || break
		page=$((page + 1))
	done
	have git || die "git is required to resolve $SELECTION when GitHub is unavailable"
	tags=$(git ls-remote --tags --refs "$REPO") || die "cannot list source tags"
	local pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
	[ "$SELECTION" != nightly ] || pattern='^v[0-9]+\.[0-9]+\.[0-9]+-nightly\.[0-9]+$'
	TAG=$(printf '%s\n' "$tags" | sed 's|.*refs/tags/||' | grep -E "$pattern" |
		sort -t . -k1.2,1n -k2,2n -k3,3n -k4,4n | tail -n 1) || true
	[ -n "$TAG" ] || die "no $SELECTION release found; refusing to install main"
}

download_binary() {
	local os arch asset
	case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) return 1 ;; esac
	case "$(uname -m)" in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) return 1 ;; esac
	asset="ocman-$os-$arch"
	info "downloading $TAG ($os/$arch) from GitHub"
	curl -fsSL --connect-timeout 10 --max-time 300 "$DOWNLOAD/$TAG/$asset.tar.gz" -o "$WORK/binary.tar.gz" || return 1
	# Extract only the expected file, never arbitrary paths from the archive.
	tar -xOzf "$WORK/binary.tar.gz" "$asset" >"$WORK/ocman" || return 1
	[ -s "$WORK/ocman" ]
}

fetch_src() {
	mkdir -p "$(dirname "$SRC")"
	if [ -d "$SRC/.git" ]; then
		info "updating $SRC"
		[ -z "$(git -C "$SRC" status --porcelain)" ] || die "$SRC has local changes; refusing to overwrite them"
		git -C "$SRC" fetch --depth 1 origin "refs/tags/$TAG"
		git -C "$SRC" checkout --detach FETCH_HEAD
	else
		info "cloning $REPO -> $SRC"
		git clone --depth 1 --branch "$TAG" "$REPO" "$SRC"
	fi
}

build() {
	local tool
	for tool in git go node pnpm; do
		have "$tool" || die "source build needs $tool; install the versions in go.mod and mise.toml"
	done
	fetch_src
	info "building frontend"
	(cd "$SRC/frontend" && pnpm install --frozen-lockfile && pnpm build)
	info "building binary"
	# Build aside and rename: writing over a running binary fails on Linux
	# (ETXTBSY) and can SIGKILL the running process on macOS (codesign);
	# same for $CTL, which is the very script executing "ocman-ctl update".
	(cd "$SRC" && go build -ldflags "-X main.version=$TAG" -o "$WORK/ocman" .)
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
	if healthy; then
		info "started (pid $(cat "$PIDFILE")) — http://$ADDR  logs: $LOG"
	else
		stop >/dev/null
		tail -n 20 "$LOG" >&2 || true
		die "failed to start; see $LOG"
	fi
}

# healthy waits up to ~10s for the process to answer HTTP on $ADDR (any
# status counts — an auth redirect still proves the listener is up). Fails
# early if the process dies; falls back to the pid check without curl.
healthy() {
	local i
	for i in $(seq 40); do
		running_pid >/dev/null || return 1
		if command -v curl >/dev/null; then
			curl -s -o /dev/null --max-time 1 "http://$ADDR/" && return 0
		elif [ "$i" -ge 4 ]; then
			return 0
		fi
		sleep 0.25
	done
	return 1
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
	if [ -z "$SELECTION" ] && [ -f "$VERSION_FILE" ]; then
		SELECTION=$(cat "$VERSION_FILE")
	fi
	SELECTION="${SELECTION:-stable}"
	resolve_version
	WORK=$(mktemp -d)
	trap 'rm -rf "$WORK"' EXIT
	if [ "$SOURCE_ONLY" = 1 ] || ! download_binary; then
		warn "building $TAG locally"
		build
	fi
	# Fetch the controller before replacing the existing installation.
	if [ -f "${BASH_SOURCE[0]:-}" ]; then
		cp "${BASH_SOURCE[0]}" "$WORK/ocman-ctl"
	else
		curl -fsSL "$SCRIPT_URL" -o "$WORK/ocman-ctl"
	fi
	mkdir -p "$(dirname "$BIN")" "$PREFIX/bin" "$(dirname "$VERSION_FILE")"
	install -m 0755 "$WORK/ocman" "$BIN.new"
	install -m 0755 "$WORK/ocman-ctl" "$CTL.new"
	mv -f "$BIN.new" "$BIN"
	mv -f "$CTL.new" "$CTL"
	printf '%s\n' "$SELECTION" >"$VERSION_FILE"
	info "installed $TAG to $BIN"
	[ "$NO_START" = 1 ] || start
}

cmd_update() {
	local requested_no_start="$NO_START"
	NO_START=1
	cmd_install
	NO_START="$requested_no_start"
	if [ "$NO_START" != 1 ]; then
		stop
		start
	fi
}

cmd_uninstall() {
	stop
	rm -f "$BIN" "$BIN.new" "$LOG" "$PIDFILE" "$VERSION_FILE"
	rm -rf "$SRC"
	info "removed binary, sources and logs"
	local data="${XDG_DATA_HOME:-$HOME/.local/share}/ocman"
	if [ "${1:-}" = "--purge" ]; then
		rm -rf "$data"
		info "purged ocman data (state.db included)"
	else
		info "kept your data in $data — re-run with --purge to delete it"
	fi
	rm -f "$CTL" "$CTL.new"
}

usage() {
	cat <<EOF
ocman-ctl <command>

  install [version]    install stable (default), nightly or an exact version
  update [version]     reinstall saved channel/version, then restart
  --no-start           install/update without starting or stopping ocman
  --source             skip binary download and build the selected tag
  start | stop | restart | status
  logs                 tail -f the service log
  uninstall [--purge]  stop and remove (--purge also deletes state.db)
  doctor               dependency check only

Environment: OCMAN_ADDR (default $ADDR), OCMAN_PREFIX, OCMAN_SRC,
OCMAN_VERSION, OCMAN_REPO (source URL), OCMAN_RUN, OCMAN_BIN
EOF
}

COMMAND="${1:-install}"
[ "$#" -eq 0 ] || shift
if [ "$COMMAND" = install ] || [ "$COMMAND" = update ]; then
	for arg in "$@"; do
		case "$arg" in
		--no-start) NO_START=1 ;;
		--source) SOURCE_ONLY=1 ;;
		-*) die "unknown option: $arg" ;;
		*) [ -z "$SELECTION" ] || die "specify only one version"; SELECTION="$arg" ;;
		esac
	done
fi
case "$COMMAND" in
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
uninstall) cmd_uninstall "${1:-}" ;;
doctor) doctor ;;
help | -h | --help) usage ;;
*)
	usage >&2
	die "unknown command: $COMMAND"
	;;
esac
