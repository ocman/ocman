#!/usr/bin/env bash
# Self-test for fuzz.sh against a fake go: a package that lists no targets
# or a failing target must fail the run, and each listed target is fuzzed.
set -uo pipefail

FUZZ="$(cd "$(dirname "$0")" && pwd)/fuzz.sh"
bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT
fail() { echo "fuzz self-test: $1" >&2; exit 1; }

# FAKE_LIST is what `go test -list` prints; FAKE_FAIL makes -fuzz exit 1.
cat > "$bin/go" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$FAKE_LOG"
if [ "$2" = -list ]; then printf '%b' "$FAKE_LIST"; echo ok; exit 0; fi
exit "${FAKE_FAIL:-0}"
EOF
chmod +x "$bin/go"
export PATH="$bin:$PATH" FAKE_LOG="$bin/log"

FAKE_LIST='' "$FUZZ" pkg > /dev/null 2>&1 && fail "a package without targets passed"
FAKE_LIST='FuzzA\n' FAKE_FAIL=1 "$FUZZ" pkg > /dev/null 2>&1 && fail "a failing target passed"
: > "$FAKE_LOG"
FAKE_LIST='FuzzA\nFuzzB\n' "$FUZZ" pkg > /dev/null 2>&1 || fail "passing targets failed"
grep -q -- '-fuzz ^FuzzA\$ -fuzztime 10s ./pkg' "$FAKE_LOG" || fail "FuzzA was not fuzzed"
grep -q -- '-fuzz ^FuzzB\$ ' "$FAKE_LOG" || fail "FuzzB was not fuzzed"
echo "fuzz self-test: ok"
