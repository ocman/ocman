#!/usr/bin/env bash
# Decides which coverage suites the ratchet job should run, based on the
# files changed in a PR. Mirrors the pre-commit hook's per-language
# `files:` split (\.go$ -> go, ^frontend/ -> frontend) so a docs-only PR
# doesn't run — and can't spuriously fail — the Go/frontend ratchet.
#
# On a push to main we always run BOTH suites: publish-baseline needs
# coverage/go.json AND coverage/frontend.json to exist, so scoping there
# would break the baseline.
#
# Emits GitHub-Actions step outputs:
#   suites=<go|frontend|all>   arg for `make coverage`   ('' means skip)
#   check=<go|frontend|'' >    args for `make coverage-check`
#     (check='' checks both — only reachable via suites=all)

set -euo pipefail

emit() {
	# $1=suites-arg $2=check-arg
	if [ -n "${GITHUB_OUTPUT:-}" ]; then
		{ echo "suites=$1"; echo "check=$2"; } >> "$GITHUB_OUTPUT"
	fi
	echo "coverage-changed-suites: suites='$1' check='$2'"
}

# Non-PR (push to main etc.): always both.
if [ "${GITHUB_EVENT_NAME:-}" != "pull_request" ]; then
	emit "all" ""
	exit 0
fi

base="origin/${GITHUB_BASE_REF:-main}"
git fetch --depth=1 origin "${GITHUB_BASE_REF:-main}" >/dev/null 2>&1 || true

changed="$(git diff --name-only "${base}...HEAD" 2>/dev/null || git diff --name-only "$base" 2>/dev/null || true)"

go=0
fe=0
while IFS= read -r f; do
	[ -z "$f" ] && continue
	case "$f" in
		*.go) go=1 ;;
	esac
	case "$f" in
		frontend/*) fe=1 ;;
	esac
done <<EOF
$changed
EOF

if [ "$go" -eq 1 ] && [ "$fe" -eq 1 ]; then
	emit "all" ""
elif [ "$go" -eq 1 ]; then
	emit "go" "go"
elif [ "$fe" -eq 1 ]; then
	emit "frontend" "frontend"
else
	emit "" ""
fi
