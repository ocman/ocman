#!/usr/bin/env bash
# Self-test for fuzz.sh against a fake `go` in a throwaway git repository:
# failures are recorded and the run continues, crash inputs are collected,
# and a package whose targets cannot be listed, or a run that finds no
# targets at all, fails instead of passing vacuously.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/fuzz.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The fake go answers `go test -list '^Fuzz' ./<dir>` from the dir's
# sources and `go test ... -fuzz=^<T>$ ./<dir>` from marker files in
# $FAKE: list-fail-<dir with / as -> (listing errors), list-empty-<dir>
# (lists nothing), crash-<T> (writes an input), fail-<T> (no input). Every
# invocation is appended to $FAKE/calls.
cat >"$tmp/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
pkg="${*: -1}"
dir="${pkg#./}"
echo "$*" >>"$FAKE/calls"
case "$*" in
test\ -list*)
	if [ -e "$FAKE/list-fail-$(printf '%s' "$dir" | tr / -)" ]; then
		echo "$pkg: build failed: undefined: nope" >&2
		exit 1
	fi
	[ -e "$FAKE/list-empty-$(printf '%s' "$dir" | tr / -)" ] ||
		cat "$dir"/*_test.go | sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\).*/\1/p'
	echo "ok  	example/$dir	0.01s"
	;;
test\ -run*)
	target="$(printf '%s\n' "$@" | sed -n 's/^-fuzz=^\(.*\)\$$/\1/p')"
	echo "fuzz: elapsed: 0s, running $target"
	if [ -e "$FAKE/crash-$target" ]; then
		mkdir -p "$dir/testdata/fuzz/$target"
		printf 'go test fuzz v1\nstring("boom")\n' >"$dir/testdata/fuzz/$target/0123abcd"
		echo "--- FAIL: $target (0.01s)"
		echo "    Failing input written to testdata/fuzz/$target/0123abcd"
		exit 1
	fi
	if [ -e "$FAKE/fail-$target" ]; then
		echo "panic: test timed out"
		exit 2
	fi
	echo PASS
	;;
*)
	echo "fake go: unexpected $*" >&2
	exit 3
	;;
esac
EOF
chmod +x "$tmp/go"

fail() {
	echo "fuzz.sh self-test: $*" >&2
	[ -f "$out" ] && sed 's/^/  | /' "$out" >&2
	exit 1
}

# repo NAME PKG...: a git repository with one fuzz target per PKG
# ("internal/a:FuzzA,FuzzB"), plus fresh fake state.
repo() {
	work="$tmp/$1"
	shift
	mkdir -p "$work"
	git -C "$work" init -q
	for spec in "$@"; do
		dir="${spec%%:*}"
		mkdir -p "$work/$dir"
		for t in $(printf '%s' "${spec#*:}" | tr , ' '); do
			printf 'package p\n\nfunc %s(f *testing.F) {}\n' "$t" >"$work/$dir/${t}_test.go"
		done
	done
	export FAKE="$work/.fake"
	mkdir -p "$FAKE"
	: >"$FAKE/calls"
	out="$work/.out"
}

# run: fuzz.sh in $work, with FUZZTIME=$fuzztime (default 1s); sets $status.
run() {
	status=0
	(cd "$work" && GO="$tmp/go" FUZZTIME="${fuzztime:-1s}" "$script") >"$out" 2>&1 || status=$?
}

ran() { grep -qF -- "-fuzz=^$1\$ " "$FAKE/calls"; }

# All targets pass.
repo pass internal/a:FuzzA internal/b:FuzzB
run
[ "$status" -eq 0 ] || fail "passing run exited $status"
grep -q 'all 2 targets passed' "$out" || fail "passing run did not report 2 targets"
[ ! -s "$work/fuzz-failures/index.tsv" ] || fail "passing run recorded failures"

# A crash, then a crash-free failure; later targets still run.
repo failures internal/a:FuzzA,FuzzB internal/b/c:FuzzC
touch "$FAKE/crash-FuzzA" "$FAKE/fail-FuzzC"
run
[ "$status" -ne 0 ] || fail "failing targets exited 0"
{ ran FuzzB && ran FuzzC; } || fail "a failure stopped the run"
grep -q '2 failures (3 targets run)' "$out" || fail "failure summary missing"
idx="$work/fuzz-failures/index.tsv"
printf 'internal/a\tFuzzA\tfuzz-failures/internal-a.FuzzA.log\tinternal/a/testdata/fuzz/FuzzA/0123abcd\n' |
	cmp -s - <(grep FuzzA "$idx") || fail "unexpected FuzzA record: $(grep FuzzA "$idx")"
printf 'internal/b/c\tFuzzC\tfuzz-failures/internal-b-c.FuzzC.log\t\n' |
	cmp -s - <(grep FuzzC "$idx") || fail "unexpected FuzzC record: $(grep FuzzC "$idx")"
grep -q 'string("boom")' "$work/fuzz-failures/inputs/internal/a/testdata/fuzz/FuzzA/0123abcd" ||
	fail "crash input not collected"
grep -q 'Failing input written' "$work/fuzz-failures/internal-a.FuzzA.log" || fail "FuzzA log not kept"
grep -q 'timed out' "$work/fuzz-failures/internal-b-c.FuzzC.log" || fail "FuzzC log not kept"

# A package whose targets cannot be listed is a failure, not a skip.
repo discovery internal/a:FuzzA internal/b:FuzzB
touch "$FAKE/list-fail-internal-a"
run
[ "$status" -ne 0 ] || fail "discovery failure exited 0"
ran FuzzB || fail "a discovery failure stopped the run"
printf 'internal/a\t-\tfuzz-failures/internal-a.discovery.log\t\n' |
	cmp -s - "$work/fuzz-failures/index.tsv" || fail "unexpected discovery record: $(cat "$work/fuzz-failures/index.tsv")"
grep -q 'undefined: nope' "$work/fuzz-failures/internal-a.discovery.log" || fail "discovery log not kept"

# A package that declares a fuzz function go test does not list (a build
# constraint, say) would otherwise be skipped without a word.
repo unlisted internal/a:FuzzA internal/b:FuzzB
touch "$FAKE/list-empty-internal-a"
run
[ "$status" -ne 0 ] || fail "an unlisted package exited 0"
ran FuzzB || fail "an unlisted package stopped the run"
grep -q "^internal/a	-	" "$work/fuzz-failures/index.tsv" || fail "unlisted package not recorded"

# No fuzz targets anywhere.
repo empty
run
[ "$status" -ne 0 ] || fail "a run with no targets exited 0"
grep -q 'no fuzz targets' "$out" || fail "zero-target run gave no reason"

# Not a git repository: discovery itself fails.
work="$tmp/nogit"
mkdir -p "$work"
out="$work/.out"
run
[ "$status" -ne 0 ] || fail "failed package discovery exited 0"

# FUZZTIME is operator input (the workflow's manual fuzztime): anything but
# a duration or a count is rejected before a target runs, and neither the
# script nor the Makefile ever evaluates it as shell.
repo fuzztime internal/a:FuzzA
for fuzztime in '1s; touch pwned #' '$(touch pwned)' '1s -run=x' '10'; do
	: >"$FAKE/calls"
	run
	[ "$status" -ne 0 ] || fail "FUZZTIME '$fuzztime' was accepted"
	! grep -q '^test -run' "$FAKE/calls" || fail "FUZZTIME '$fuzztime' still fuzzed"
	[ ! -e "$work/pwned" ] || fail "FUZZTIME '$fuzztime' ran as shell"
done
for fuzztime in 1m30s 1.5s 200x; do
	run
	[ "$status" -eq 0 ] || fail "valid FUZZTIME '$fuzztime' exited $status"
done
unset fuzztime
mkdir -p "$work/scripts"
cp "$script" "$work/scripts/fuzz.sh"
status=0
GO="$tmp/go" make -s -C "$work" -f "$(dirname "$script")/../Makefile" test-fuzz \
	FUZZTIME='1s; touch pwned #' >"$out" 2>&1 || status=$?
[ "$status" -ne 0 ] || fail "make test-fuzz accepted a shell FUZZTIME"
[ ! -e "$work/pwned" ] || fail "make test-fuzz ran FUZZTIME as shell"

echo "fuzz.sh self-test: ok"
