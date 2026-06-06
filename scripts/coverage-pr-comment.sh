#!/usr/bin/env bash
# Posts (or updates) a PR comment with the coverage ratchet result.
# Best-effort: any failure (e.g. fork PR without a write token) is
# swallowed so it never fails CI (spec/ci-coverage-ratchet R9/R10).
#
# Reads coverage/ratchet-result.json and renders a Markdown table.
# Finds an existing comment by the hidden marker and PATCHes it, else
# POSTs a new one — so re-runs update in place instead of spamming.
#
# Required env:
#   FORGEJO_API_URL   e.g. github.api_url
#   REPOSITORY        owner/repo
#   PR_NUMBER         pull request number
#   TOKEN             API token (github.token)

set -uo pipefail

MARKER="<!-- coverage-ratchet -->"
RESULT="coverage/ratchet-result.json"

[ -f "$RESULT" ] || { echo "coverage-pr-comment: no $RESULT, skipping"; exit 0; }
[ -n "${PR_NUMBER:-}" ] || { echo "coverage-pr-comment: no PR_NUMBER, skipping"; exit 0; }
[ -n "${TOKEN:-}" ] || { echo "coverage-pr-comment: no TOKEN, skipping"; exit 0; }

owner="${REPOSITORY%/*}"
repo="${REPOSITORY#*/}"
api="${FORGEJO_API_URL}/repos/${owner}/${repo}"

# Render the Markdown body from the result JSON.
body="$(node -e '
const r = require("./coverage/ratchet-result.json");
const marker = "<!-- coverage-ratchet -->";
const icon = s => s === "pass" ? "✅" : s === "fail" ? "❌" : "➖";
const fmt = v => v === null || v === undefined ? "—" : Number(v).toFixed(2) + "%";
const dlt = v => v === null || v === undefined ? "—" : (v >= 0 ? "+" : "") + Number(v).toFixed(2);
let out = marker + "\n";
out += "### Coverage ratchet: " + (r.overall === "pass" ? "✅ pass" : "❌ fail") + "\n\n";
out += "| Suite | Baseline | This PR | Δ | |\n|---|---|---|---|---|\n";
for (const s of r.suites) {
  out += `| ${s.name} | ${fmt(s.old)} | ${fmt(s.new)} | ${dlt(s.delta)} | ${icon(s.status)} |\n`;
}
out += `\n_Tolerance: -${r.tolerance}%. Baseline stored on \`gh-pages\`._`;
process.stdout.write(out);
')"

# JSON-encode the body safely.
payload="$(node -e 'process.stdout.write(JSON.stringify({body: process.argv[1]}))' "$body")"

# Find an existing ratchet comment.
existing_id="$(curl -sS \
	--header "Authorization: token ${TOKEN}" \
	"${api}/issues/${PR_NUMBER}/comments" 2>/dev/null \
	| node -e '
let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{
  try{const a=JSON.parse(d);const m=a.find(c=>c.body&&c.body.includes("<!-- coverage-ratchet -->"));
  process.stdout.write(m?String(m.id):"");}catch(e){}
});' 2>/dev/null || true)"

if [ -n "$existing_id" ]; then
	echo "coverage-pr-comment: updating comment ${existing_id}"
	curl -sS -o /dev/null \
		--request PATCH \
		--header "Authorization: token ${TOKEN}" \
		--header "Content-Type: application/json" \
		--data "$payload" \
		"${api}/issues/comments/${existing_id}" || true
else
	echo "coverage-pr-comment: creating new comment"
	curl -sS -o /dev/null \
		--request POST \
		--header "Authorization: token ${TOKEN}" \
		--header "Content-Type: application/json" \
		--data "$payload" \
		"${api}/issues/${PR_NUMBER}/comments" || true
fi

exit 0
