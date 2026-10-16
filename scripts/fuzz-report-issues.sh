#!/usr/bin/env bash
# Opens one Forgejo issue per failure recorded by scripts/fuzz.sh: a failed
# fuzz target, or a package whose targets could not be listed.
# An open issue with the same title gets a comment for the new run instead
# of a duplicate. The "fuzz" label is applied when the repository has one.
#
# Required env:
#   FORGEJO_API_URL   e.g. github.api_url
#   REPOSITORY        owner/repo
#   FORGEJO_TOKEN     API token
#   GITHUB_SERVER_URL, GITHUB_RUN_NUMBER, GITHUB_SHA   for the run link
# Optional env:
#   FUZZ_FAILURES_DIR  failure records (default fuzz-failures)
#   DRY_RUN=1          print POST payloads instead of sending them
#   CURL               curl binary (tests substitute a fake)

# The issue body's backticks are Markdown, not command substitutions.
# shellcheck disable=SC2016

set -euo pipefail

out="${FUZZ_FAILURES_DIR:-fuzz-failures}"
index="$out/index.tsv"
[ -s "$index" ] || { echo "fuzz-report-issues: no failures recorded in $index"; exit 0; }

owner="${REPOSITORY%/*}"
repo="${REPOSITORY#*/}"
api="${FORGEJO_API_URL}/repos/${owner}/${repo}"
run_url="${GITHUB_SERVER_URL}/${REPOSITORY}/actions/runs/${GITHUB_RUN_NUMBER}"
commit="${GITHUB_SHA:-unknown}"

# request METHOD URL [JSON]: prints the response body, fails on HTTP errors.
request() {
	local method="$1" url="$2" data="${3:-}"
	if [ "${DRY_RUN:-}" = 1 ] && [ "$method" != GET ]; then
		printf 'DRY-RUN %s %s\n%s\n' "$method" "$url" "$data" >&2
		echo '{}'
		return
	fi
	local args=(--silent --show-error --fail-with-body --header "Authorization: token ${FORGEJO_TOKEN}" --request "$method")
	[ -n "$data" ] && args+=(--header "Content-Type: application/json" --data "$data")
	# An error body goes to stderr with the request, not on to jq.
	local body
	body="$("${CURL:-curl}" "${args[@]}" "$url")" || {
		printf 'fuzz-report-issues: %s %s failed: %s\n' "$method" "$url" "$body" >&2
		return 1
	}
	printf '%s\n' "$body"
}

# fenced FILE MAX: FILE's last MAX bytes in a code fence longer than any
# backtick run inside, so the content cannot close it.
fenced() {
	local file="$1" max="$2" content longest
	content="$(tail -c "$max" "$file")"
	longest="$(printf '%s' "$content" | { grep -o '`*' || true; } | awk '{ if (length > n) n = length } END { print n + 0 }')"
	local fence='```'
	while [ "${#fence}" -le "$longest" ]; do fence="${fence}\`"; done
	[ "$(wc -c < "$file")" -gt "$max" ] && printf '(truncated to the last %d bytes)\n' "$max"
	printf '%stext\n%s\n%s\n' "$fence" "$content" "$fence"
}

# The label is optional: a failed lookup files the issues unlabelled.
label_id="$(request GET "${api}/labels?limit=100" | jq -r '[.[] | select(.name == "fuzz") | .id][0] // empty')" || {
	echo "fuzz-report-issues: label lookup failed; opening issues without the fuzz label" >&2
	label_id=""
}

# discovery_body DIR LOG: fuzz.sh could not list DIR's targets, so none ran.
discovery_body() {
	local dir="$1" log="$2"
	printf 'Listing the fuzz targets in `./%s` failed, so none of them ran.\n\n' "$dir"
	printf -- '- Run: %s\n- Commit: `%s`\n\n' "$run_url" "$commit"
	printf '### Failure\n\n'
	fenced "$log" 6000
	printf '\n### Reproduce\n\n```sh\ngo test -list '\''^Fuzz'\'' ./%s\n```\n' "$dir"
}

# target_body DIR TARGET LOG INPUT: TARGET failed, INPUT is its crasher or "".
target_body() {
	local dir="$1" target="$2" log="$3" input="$4" name
	printf 'Fuzz target `%s` in `./%s` failed.\n\n' "$target" "$dir"
	printf -- '- Run: %s\n- Commit: `%s`\n\n' "$run_url" "$commit"
	printf '### Failure\n\n'
	fenced "$log" 6000
	if [ -n "$input" ] && [ -f "$input" ]; then
		name="$(basename "$input")"
		printf '\n### Crashing input\n\n`%s`:\n\n' "$input"
		fenced "$input" 4000
		printf '\n### Reproduce\n\nSave the input above to `%s/testdata/fuzz/%s/%s` and run:\n\n' "$dir" "$target" "$name"
		printf '```sh\ngo test -run '\''%s/%s'\'' ./%s\n```\n\n' "$target" "$name" "$dir"
		printf 'Commit the input with the fix so it stays a regression seed.\n'
	else
		printf '\n### Reproduce\n\nNo crashing input was written (a hang or a crash outside the fuzz function). Run:\n\n'
		printf '```sh\ngo test -run='\''^$'\'' -fuzz='\''^%s$'\'' ./%s\n```\n' "$target" "$dir"
	fi
}

while IFS=$'\t' read -r dir target log input; do
	body_file="$(mktemp)"
	# fuzz.sh records a package whose targets could not be listed as "-".
	if [ "$target" = - ]; then
		title="fuzz: ${dir} discovery failed"
		discovery_body "$dir" "$log" > "$body_file"
	else
		title="fuzz: ${dir} ${target} failed"
		target_body "$dir" "$target" "$log" "$input" > "$body_file"
	fi

	query="$(jq -rn --arg q "$title" '$q | @uri')"
	existing="$(request GET "${api}/issues?state=open&type=issues&limit=50&q=${query}" |
		jq -r --arg t "$title" '[.[] | select(.title == $t) | .number][0] // empty')"
	if [ -n "$existing" ]; then
		payload="$(jq -n --rawfile body "$body_file" '{body: ("Failed again.\n\n" + $body)}')"
		request POST "${api}/issues/${existing}/comments" "$payload" > /dev/null
		echo "fuzz-report-issues: commented on #${existing} (${title})"
	else
		payload="$(jq -n --arg title "$title" --rawfile body "$body_file" --arg label "$label_id" \
			'{title: $title, body: $body} + (if $label == "" then {} else {labels: [($label | tonumber)]} end)')"
		number="$(request POST "${api}/issues" "$payload" | jq -r '.number // empty')"
		echo "fuzz-report-issues: opened #${number:-?} (${title})"
	fi
	rm -f "$body_file"
done < "$index"
