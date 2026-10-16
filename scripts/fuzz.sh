#!/usr/bin/env bash
# Runs every Fuzz* target under internal/ for FUZZTIME each (default 10s).
#
# A failing target does not stop the run: each failure is recorded and the
# script exits non-zero after the last target. Records go to
# FUZZ_FAILURES_DIR (default fuzz-failures/), which is emptied first:
#
#   index.tsv            one "<package dir>\t<Target>\t<log file>\t<input>"
#                        line per failure; <input> is the crashing input
#                        Go wrote under <package dir>/testdata/fuzz/, or ""
#   <pkg>.<Target>.log   the tail of that target's output
#   inputs/<input>       a copy of each crashing input
#
# scripts/fuzz-report-issues.sh turns the index into Forgejo issues.

set -uo pipefail

FUZZTIME="${FUZZTIME:-10s}"
out="${FUZZ_FAILURES_DIR:-fuzz-failures}"
go="${GO:-go}"

rm -rf "$out"
mkdir -p "$out"
: > "$out/index.tsv"

# Only packages that declare a fuzz target; go test -list names them.
dirs="$(git grep --untracked -l '^func Fuzz' -- 'internal/*_test.go' | xargs -n1 dirname | sort -u)"
total=0
failed=0
for dir in $dirs; do
	for fn in $("$go" test -list '^Fuzz' "./$dir" | grep '^Fuzz'); do
		total=$((total + 1))
		echo "==> ./$dir $fn"
		log="$(mktemp)"
		"$go" test -run='^$' -fuzz="^${fn}\$" -fuzztime="$FUZZTIME" "./$dir" 2>&1 | tee "$log"
		status="${PIPESTATUS[0]}"
		if [ "$status" -ne 0 ]; then
			failed=$((failed + 1))
			name="$(printf '%s' "$dir" | tr '/' '-').$fn.log"
			tail -n 80 "$log" > "$out/$name"
			input="$(sed -n 's/^.*Failing input written to \(testdata\/fuzz\/[^[:space:]]*\).*$/\1/p' "$log" | tail -n 1)"
			if [ -n "$input" ]; then
				input="$dir/$input"
				# Keep a copy beside the log for artifact upload.
				mkdir -p "$out/inputs/$(dirname "$input")"
				cp "$input" "$out/inputs/$input" || true
			fi
			printf '%s\t%s\t%s\t%s\n' "$dir" "$fn" "$out/$name" "$input" >> "$out/index.tsv"
		fi
		rm -f "$log"
	done
done

echo
if [ "$failed" -ne 0 ]; then
	echo "fuzz: $failed of $total targets failed:"
	cut -f1,2,4 "$out/index.tsv" | sed 's/^/  /'
	exit 1
fi
echo "fuzz: all $total targets passed (${FUZZTIME} each)"
