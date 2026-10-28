#!/usr/bin/env bash
# Self-test for fuzz.sh against a fake go: a package that lists no targets
# or a failing target must fail the run, and each listed target is fuzzed.
set -uo pipefail

FUZZ="$(cd "$(dirname "$0")" && pwd)/fuzz.sh"
bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT
fail() { echo "fuzz self-test: $1" >&2; exit 1; }

# FAKE_LIST is what `go test -list` prints; FAKE_FAIL makes -fuzz exit 1.
cat > "$bin/go" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >> "$FAKE_LOG"
case " $* " in *" -list "*) printf '%b' "$FAKE_LIST"; echo ok; exit 0 ;; esac
exit "${FAKE_FAIL:-0}"
FAKE
chmod +x "$bin/go"
export PATH="$bin:$PATH" FAKE_LOG="$bin/log"

FAKE_LIST='' "$FUZZ" pkg > /dev/null 2>&1 && fail "a package without targets passed"
FAKE_LIST='FuzzA\n' FAKE_FAIL=1 "$FUZZ" pkg > /dev/null 2>&1 && fail "a failing target passed"
: > "$FAKE_LOG"
FAKE_LIST='FuzzA\nFuzzB\n' "$FUZZ" pkg > /dev/null 2>&1 || fail "passing targets failed"
grep -q -- '-C \. test -run ^\$ -fuzz ^FuzzA\$ -fuzztime 10s ./pkg' "$FAKE_LOG" || fail "FuzzA was not fuzzed"
grep -q -- '-fuzz ^FuzzB\$ ' "$FAKE_LOG" || fail "FuzzB was not fuzzed"
: > "$FAKE_LOG"
FAKE_LIST='FuzzC\n' "$FUZZ" sdk/protocol > /dev/null 2>&1 || fail "sdk target failed"
grep -q -- '-C sdk test -run ^\$ -fuzz ^FuzzC\$ -fuzztime 10s ./protocol' "$FAKE_LOG" || fail "sdk target did not run in the sdk module"
echo "fuzz self-test: ok"
