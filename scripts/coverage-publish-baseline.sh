#!/usr/bin/env bash
# Publishes the freshly collected coverage/*.json to the orphan
# gh-pages branch as the new ratchet baseline. Main-push only.
# Creates gh-pages on first run (spec/ci-coverage-ratchet R6).
#
# Required env:
#   SERVER_URL    git server base (github.server_url)
#   REPOSITORY    owner/repo
#   GITHUB_SHA    commit being published
#   TOKEN         push token (github.token)

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

[ -f coverage/go.json ] || { echo "publish-baseline: missing coverage/go.json"; exit 1; }
[ -f coverage/frontend.json ] || { echo "publish-baseline: missing coverage/frontend.json"; exit 1; }

git config user.name 'forgejo-actions[bot]'
git config user.email 'forgejo-actions[bot]@noreply.local'

remote="${SERVER_URL#https://}"
push_url="https://x-access-token:${TOKEN}@${remote}/${REPOSITORY}.git"

tmp="$(mktemp -d)"
cleanup() { git worktree remove "$tmp" --force >/dev/null 2>&1 || true; rm -rf "$tmp"; }
trap cleanup EXIT

if git fetch --depth=1 origin gh-pages >/dev/null 2>&1; then
	git worktree add "$tmp" origin/gh-pages >/dev/null 2>&1
	( cd "$tmp" && git checkout -B gh-pages >/dev/null 2>&1 )
else
	echo "publish-baseline: gh-pages not found, creating orphan branch"
	git worktree add --detach "$tmp" >/dev/null 2>&1
	( cd "$tmp" && git checkout --orphan gh-pages >/dev/null 2>&1 && git rm -rf . >/dev/null 2>&1 || true )
fi

mkdir -p "$tmp/coverage"
cp coverage/go.json coverage/frontend.json "$tmp/coverage/"

(
	cd "$tmp"
	git add coverage/go.json coverage/frontend.json
	if git diff --cached --quiet; then
		echo "publish-baseline: baseline unchanged, nothing to commit"
		exit 0
	fi
	git commit -m "chore(coverage): update baseline for ${GITHUB_SHA:0:8}" >/dev/null
	git push "$push_url" gh-pages
	echo "publish-baseline: pushed baseline to gh-pages"
)
