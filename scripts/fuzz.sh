#!/usr/bin/env bash
# Runs every Fuzz* target under internal/ and sdk/ (or in the package dirs
# given as arguments) for FUZZTIME each (default 10s). Go writes crashing
# inputs to <package>/testdata/fuzz/. Stops at the first failure; also fails
# when a package lists no targets or no target runs at all.
set -euo pipefail

FUZZTIME="${FUZZTIME:-10s}"
dirs="${*:-$(git grep --untracked -l '^func Fuzz' -- 'internal/*_test.go' 'sdk/*_test.go' | xargs -n1 dirname | sort -u)}"
total=0
for dir in $dirs; do
	# sdk/ is a nested module, and Go only fuzzes packages in the main module.
	module=.
	pkg="./$dir"
	case "$dir" in
	sdk/*)
		module=sdk
		pkg="./${dir#sdk/}"
		;;
	esac
	targets="$(go -C "$module" test -list '^Fuzz' "$pkg" | grep '^Fuzz')" || {
		echo "fuzz: listing fuzz targets in ./$dir failed" >&2
		exit 1
	}
	for name in $targets; do
		echo "==> ./$dir $name"
		go -C "$module" test -run '^$' -fuzz "^${name}\$" -fuzztime "$FUZZTIME" "$pkg"
		total=$((total + 1))
	done
done
if [ "$total" -eq 0 ]; then
	echo "fuzz: no fuzz targets found" >&2
	exit 1
fi
echo "fuzz: all $total targets passed (${FUZZTIME} each)"
