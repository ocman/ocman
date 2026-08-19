#!/usr/bin/env bash
# Checks install.sh's service lifecycle (pidfile handling) against a fake
# binary. No network, no build. Run: ./scripts/install_test.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

cat >"$TMP/fake-ocman" <<'EOF'
#!/bin/sh
while :; do sleep 1; done
EOF
chmod +x "$TMP/fake-ocman"

export OCMAN_BIN="$TMP/fake-ocman" OCMAN_RUN="$TMP/run" OCMAN_PREFIX="$TMP/prefix"
ctl() { "$ROOT/install.sh" "$@"; }

fail() { echo "FAIL: $*" >&2; exit 1; }

ctl status && fail "status should exit non-zero when stopped"
ctl start >/dev/null || fail "start"
pid=$(cat "$TMP/run/ocman.pid")
kill -0 "$pid" || fail "process not alive after start"
ctl start | grep -q 'already running' || fail "second start should be a no-op"
ctl status >/dev/null || fail "status should exit 0 while running"

ctl stop >/dev/null || fail "stop"
[ ! -f "$TMP/run/ocman.pid" ] || fail "pidfile left behind"
kill -0 "$pid" 2>/dev/null && fail "process still alive after stop"
ctl stop | grep -q 'not running' || fail "second stop should be a no-op"

# stale pidfile (process gone) must not be treated as running
mkdir -p "$TMP/run" && echo 999999 >"$TMP/run/ocman.pid"
ctl status && fail "stale pidfile reported as running"

# recycled pid: alive, but not our binary -> must read as stopped, not killed
sleep 30 &
other=$!
echo "$other" >"$TMP/run/ocman.pid"
ctl status && fail "foreign pid reported as running"
ctl stop >/dev/null
kill -0 "$other" 2>/dev/null || fail "stop killed an unrelated process"
kill "$other"

ctl help >/dev/null || fail "help"
ctl bogus >/dev/null 2>&1 && fail "unknown command should exit non-zero"

echo "ok"
