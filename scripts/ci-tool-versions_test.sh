#!/usr/bin/env bash
# Self-test for ci-tool-versions.sh against throwaway repository roots.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/ci-tool-versions.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "ci-tool-versions self-test: $*" >&2
	exit 1
}

mkdir -p "$tmp/repo/frontend"
cat >"$tmp/repo/mise.toml" <<'EOF'
[tools]
node = "25"
nodemon = "3"
hugo = "0.164.0"
goreleaser = "2.20.1"
EOF
cat >"$tmp/repo/frontend/package.json" <<'EOF'
{
  "name": "fixture",
  "packageManager": "pnpm@10.11.1+sha512.abc"
}
EOF

want="$(printf 'node=25\nhugo=0.164.0\npnpm=10.11.1\ngoreleaser=2.20.1')"
got="$(GITHUB_OUTPUT="$tmp/output" "$script" "$tmp/repo")"
[ "$got" = "$want" ] || fail "stdout = $got"
[ "$(cat "$tmp/output")" = "$want" ] || fail "GITHUB_OUTPUT = $(cat "$tmp/output")"

sed -i.bak '/^hugo/d' "$tmp/repo/mise.toml"
if "$script" "$tmp/repo" >/dev/null 2>&1; then
	fail "a missing version must fail"
fi

"$script" >/dev/null || fail "the repository's own pins must parse"

echo "ci-tool-versions self-test: ok"
