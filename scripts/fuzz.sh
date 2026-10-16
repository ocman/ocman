#!/usr/bin/env bash
# Runs every Fuzz* target under internal/ for FUZZTIME each (default 10s).
#
# A failing target does not stop the run: each failure is recorded and the
# script exits non-zero after the last target. A package whose targets
# cannot be listed (go test -list fails, or lists none of the Fuzz
# functions it declares) is recorded the same way, as target "-", and a run
# that finds no targets at all fails. Records go to FUZZ_FAILURES_DIR
# (default fuzz-failures/), which is emptied first:
#
#   index.tsv            one "<package dir>\t<Target>\t<log file>\t<input>"
#                        line per failure; <input> is the crashing input
#                        Go wrote under <package dir>/testdata/fuzz/, or "".
#                        <Target> is "-" when discovering the package's
#                        targets failed.
#   <pkg>.<Target>.log   the tail of that target's output
#   <pkg>.discovery.log  the tail of a failed go test -list
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

# Only packages that declare a fuzz target. git grep exits 1 for no match
# (caught below as zero targets) and above 1 when it could not search.
files="$(git grep --untracked -l '^func Fuzz' -- 'internal/*_test.go')"
status=$?
if [ "$status" -gt 1 ]; then
	echo "fuzz: searching for fuzz targets failed (git grep exit $status)" >&2
	exit 1
fi
dirs="$(printf '%s\n' "$files" | sed -n 's|/[^/]*$||p' | sort -u)"

total=0
failed=0
for dir in $dirs; do
	log="$(mktemp)"
	"$go" test -list '^Fuzz' "./$dir" > "$log" 2>&1
	status=$?
	targets="$(grep '^Fuzz' "$log")"
	if [ "$status" -ne 0 ] || [ -z "$targets" ]; then
		failed=$((failed + 1))
		echo "==> ./$dir: listing fuzz targets failed (exit $status)"
		cat "$log"
		[ "$status" -eq 0 ] && echo "go test -list found none of the Fuzz functions this package declares" >> "$log"
		name="$(printf '%s' "$dir" | tr '/' '-').discovery.log"
		tail -n 80 "$log" > "$out/$name"
		printf '%s\t-\t%s\t\n' "$dir" "$out/$name" >> "$out/index.tsv"
		rm -f "$log"
		continue
	fi
	rm -f "$log"
	for fn in $targets; do
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
	echo "fuzz: $failed failures ($total targets run):"
	cut -f1,2,4 "$out/index.tsv" | sed 's/^/  /'
	exit 1
fi
if [ "$total" -eq 0 ]; then
	echo "fuzz: no fuzz targets found under internal/" >&2
	exit 1
fi
echo "fuzz: all $total targets passed (${FUZZTIME} each)"
