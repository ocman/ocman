#!/usr/bin/env bash
# Self-test for fuzz-report-issues.sh against a fake curl: new issues (with
# and without the "fuzz" label), a comment instead of a duplicate, the
# discovery-failure title, Markdown fences that survive backticks in the
# content, and an HTTP error that fails the run. Needs jq.
# Backticks in the expectations are Markdown, not command substitutions.
# shellcheck disable=SC2016

set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/fuzz-report-issues.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# The fake curl answers from $FAKE: labels.json and issues.json for the
# GETs (default []), fail-<label|issues|post|comment> to return HTTP 500
# the way curl --fail-with-body does. Each request is logged to
# $FAKE/requests as "METHOD URL", and each POST body to $FAKE/post-<n>.json.
cat >"$tmp/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
method=GET data=
while [ $# -gt 1 ]; do
	case "$1" in
	--request) method="$2"; shift ;;
	--data) data="$2"; shift ;;
	--header) shift ;;
	esac
	shift
done
url="$1"
echo "$method $url" >>"$FAKE/requests"
case "$method $url" in
"GET "*/labels*) kind=label ;;
"GET "*/issues\?*) kind=issues ;;
"POST "*/comments) kind=comment ;;
"POST "*/issues) kind=post ;;
*) echo "fake curl: unexpected $method $url" >&2; exit 3 ;;
esac
if [ -e "$FAKE/fail-$kind" ]; then
	echo '{"message":"internal error"}'
	echo "curl: (22) The requested URL returned error: 500" >&2
	exit 22
fi
if [ "$method" = POST ]; then
	n=$(($(ls "$FAKE" | grep -c '^post-' || true) + 1))
	printf '%s' "$data" >"$FAKE/post-$n.json"
fi
case "$kind" in
label) cat "$FAKE/labels.json" 2>/dev/null || echo '[]' ;;
issues) cat "$FAKE/issues.json" 2>/dev/null || echo '[]' ;;
post) echo '{"number":42}' ;;
comment) echo '{"id":1}' ;;
esac
EOF
chmod +x "$tmp/curl"

fail() {
	echo "fuzz-report-issues self-test: $*" >&2
	[ -f "$work/.out" ] && sed 's/^/  | /' "$work/.out" >&2
	exit 1
}

# scenario NAME: an empty failure directory and fresh fake state.
scenario() {
	work="$tmp/$1"
	mkdir -p "$work/fuzz-failures"
	: >"$work/fuzz-failures/index.tsv"
	export FAKE="$work/.fake"
	mkdir -p "$FAKE"
	: >"$FAKE/requests"
}

# record DIR TARGET LOGTEXT [INPUTTEXT]: one index line like fuzz.sh writes.
record() {
	local log input=""
	log="fuzz-failures/$(printf "%s" "$1" | tr / -).$2.log"
	printf '%s\n' "$3" >"$work/$log"
	if [ $# -gt 3 ]; then
		input="$1/testdata/fuzz/$2/0123abcd"
		mkdir -p "$work/$(dirname "$input")"
		printf '%s\n' "$4" >"$work/$input"
	fi
	printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$log" "$input" >>"$work/fuzz-failures/index.tsv"
}

# run: the reporter in $work; sets $status.
run() {
	status=0
	(cd "$work" && CURL="$tmp/curl" FORGEJO_API_URL=https://forge.test/api/v1 REPOSITORY=o/r \
		FORGEJO_TOKEN=tok GITHUB_SERVER_URL=https://forge.test GITHUB_RUN_NUMBER=7 GITHUB_SHA=abc123 \
		"$script") >"$work/.out" 2>&1 || status=$?
}

posts() { grep -c '^POST ' "$FAKE/requests" || true; }

# Nothing recorded: nothing to do.
scenario empty
run
[ "$status" -eq 0 ] || fail "empty index exited $status"
[ ! -s "$FAKE/requests" ] || fail "empty index made requests"

# A new issue, labelled, with the crashing input and a reproduction.
scenario labelled
echo '[{"id":3,"name":"bug"},{"id":7,"name":"fuzz"}]' >"$FAKE/labels.json"
record internal/a FuzzA '--- FAIL: FuzzA' 'go test fuzz v1'
run
[ "$status" -eq 0 ] || fail "new issue exited $status"
grep -q '^POST https://forge.test/api/v1/repos/o/r/issues$' "$FAKE/requests" || fail "no issue created"
p="$FAKE/post-1.json"
[ "$(jq -r .title "$p")" = "fuzz: internal/a FuzzA failed" ] || fail "title $(jq .title "$p")"
[ "$(jq -c .labels "$p")" = "[7]" ] || fail "labels $(jq -c .labels "$p")"
body="$(jq -r .body "$p")"
for want in 'go test fuzz v1' "go test -run 'FuzzA/0123abcd' ./internal/a" \
	'internal/a/testdata/fuzz/FuzzA/0123abcd' 'https://forge.test/o/r/actions/runs/7' '`abc123`'; do
	grep -qF -- "$want" <<<"$body" || fail "body lacks $want"
done
grep -q 'opened #42' "$work/.out" || fail "issue number not reported"

# No "fuzz" label in the repository: no labels key.
scenario unlabelled
echo '[{"id":3,"name":"bug"}]' >"$FAKE/labels.json"
record internal/a FuzzA 'panic: test timed out'
run
[ "$status" -eq 0 ] || fail "unlabelled issue exited $status"
[ "$(jq -c 'has("labels")' "$FAKE/post-1.json")" = false ] || fail "labels sent without a fuzz label"
grep -qF -- "-fuzz='^FuzzA\$' ./internal/a" <<<"$(jq -r .body "$FAKE/post-1.json")" ||
	fail "crash-free failure lacks the fuzz reproduction"

# The label lookup failing does not stop the report; the issue is unlabelled.
scenario labelerror
touch "$FAKE/fail-label"
record internal/a FuzzA 'boom'
run
[ "$status" -eq 0 ] || fail "label lookup failure exited $status"
[ "$(jq -c 'has("labels")' "$FAKE/post-1.json")" = false ] || fail "labels sent after a failed lookup"
grep -q "label lookup failed" "$work/.out" || fail "failed label lookup not reported"

# An open issue with the same title gets a comment, not a duplicate; a
# title that merely contains it does not count.
scenario dedup
cat >"$FAKE/issues.json" <<'EOF'
[{"number":4,"title":"fuzz: internal/a FuzzA failed again"},{"number":5,"title":"fuzz: internal/a FuzzA failed"}]
EOF
record internal/a FuzzA 'boom'
run
[ "$status" -eq 0 ] || fail "dedup exited $status"
grep -q '^POST https://forge.test/api/v1/repos/o/r/issues/5/comments$' "$FAKE/requests" || fail "no comment on #5"
[ "$(posts)" -eq 1 ] || fail "dedup made $(posts) POSTs"
grep -q '^Failed again\.' <<<"$(jq -r .body "$FAKE/post-1.json")" || fail "comment body"
grep -qF 'q=fuzz%3A%20internal%2Fa%20FuzzA%20failed' "$FAKE/requests" || fail "search query not encoded"

# A discovery failure is filed under its own title.
scenario discovery
record internal/b - 'internal/b/x_test.go:3: undefined: nope'
run
[ "$status" -eq 0 ] || fail "discovery report exited $status"
[ "$(jq -r .title "$FAKE/post-1.json")" = "fuzz: internal/b discovery failed" ] ||
	fail "discovery title $(jq .title "$FAKE/post-1.json")"
body="$(jq -r .body "$FAKE/post-1.json")"
grep -qF 'undefined: nope' <<<"$body" || fail "discovery body lacks the log"
grep -qF "go test -list '^Fuzz' ./internal/b" <<<"$body" || fail "discovery body lacks the reproduction"

# Backticks in the log or input cannot close the fence around them.
scenario fences
record internal/a FuzzA 'got ``` and ````` in the output' 'string("````")'
run
[ "$status" -eq 0 ] || fail "fence scenario exited $status"
body="$(jq -r .body "$FAKE/post-1.json")"
grep -qxF '``````text' <<<"$body" || fail "log fence not longer than its 5-backtick run"
grep -qxF '`````text' <<<"$body" || fail "input fence not longer than its 4-backtick run"
[ "$(grep -cxF '``````' <<<"$body")" -eq 1 ] || fail "log fence not closed once"
[ "$(grep -cxF '`````' <<<"$body")" -eq 1 ] || fail "input fence not closed once"

# HTTP errors fail the run visibly: creating, commenting and searching.
for kind in post comment issues; do
	scenario "http-$kind"
	[ "$kind" = comment ] && echo '[{"number":5,"title":"fuzz: internal/a FuzzA failed"}]' >"$FAKE/issues.json"
	touch "$FAKE/fail-$kind"
	record internal/a FuzzA 'boom'
	run
	[ "$status" -ne 0 ] || fail "HTTP 500 on $kind exited 0"
	grep -q 'error: 500' "$work/.out" || fail "HTTP 500 on $kind not reported"
	grep -q 'fuzz-report-issues: .* failed' "$work/.out" || fail "HTTP 500 on $kind not attributed"
done

echo "fuzz-report-issues self-test: ok"
