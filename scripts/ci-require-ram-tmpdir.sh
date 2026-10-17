#!/usr/bin/env bash
# Fails unless DIR is a writable, executable tmpfs, so Go tests never
# silently fall back to the runner's disk. On the shared amd64 runners every
# fsync on the overlay disk took ~23 ms; tests that create SQLite databases
# ran 7-20x slower there and internal/server hit go test's 10-minute timeout.
#
# Go 1.26's t.TempDir and the go command's work directory both prefer
# GOTMPDIR over TMPDIR, so GOTMPDIR must be unset or point inside DIR.
#
# Usage: scripts/ci-require-ram-tmpdir.sh DIR

set -euo pipefail

DIR="${1:?usage: $0 DIR}"

fail() {
	echo "::error::RAM-backed test TMPDIR unavailable: $*" >&2
	exit 1
}

[ -d "$DIR" ] || fail "$DIR does not exist; the job container needs --tmpfs $DIR"
fstype="$(stat -f -c %T "$DIR")"
[ "$fstype" = tmpfs ] || fail "$DIR is $fstype, not tmpfs"
[ -w "$DIR" ] || fail "$DIR is not writable"
if [ -n "${GOTMPDIR:-}" ] && [[ "$GOTMPDIR" != "$DIR" && "$GOTMPDIR" != "$DIR"/* ]]; then
	fail "GOTMPDIR=$GOTMPDIR would place t.TempDir and go build work outside $DIR"
fi
probe="$(mktemp "$DIR/exec-probe.XXXXXX")"
trap 'rm -f "$probe"' EXIT
printf '#!/bin/sh\nexit 0\n' > "$probe"
chmod 0700 "$probe"
"$probe" || fail "$DIR is mounted noexec; tests run executables from TMPDIR"
df -h "$DIR"
