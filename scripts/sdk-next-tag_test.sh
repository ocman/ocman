#!/usr/bin/env bash
# Self-test for sdk-next-tag.sh against throwaway Git repositories.
set -uo pipefail

NEXT="$(cd "$(dirname "$0")" && pwd)/sdk-next-tag.sh"
repo="$(mktemp -d)"
trap 'rm -rf "$repo"' EXIT
fail() { echo "sdk-next-tag self-test: $1" >&2; exit 1; }

unset $(git rev-parse --local-env-vars)
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git -C "$repo" init -q
git -C "$repo" config user.name test
git -C "$repo" config user.email test@example.invalid
commit() { # $1=path $2=message
	mkdir -p "$repo/$(dirname "$1")"
	echo "$RANDOM$RANDOM" >> "$repo/$1"
	git -C "$repo" add -A
	git -C "$repo" commit -q -m "$2"
}
next() { (cd "$repo" && "$NEXT"); }
expect() { # $1=want $2=case
	got="$(next)" || fail "$2: script failed"
	[ "$got" = "$1" ] || fail "$2: want '$1', got '$got'"
}

commit sdk/go.mod "feat: add sdk"
expect sdk/v0.1.0 "first tag"
git -C "$repo" tag sdk/v0.1.0

commit main.go "feat: root only"
commit sdk/plugin/README.md "docs: sdk readme"
expect "" "root and markdown changes do not release"

commit sdk/plugin/a.go "fix(sdk): patch"
expect sdk/v0.1.1 "fix bumps patch"

commit sdk/plugin/b.go "feat(sdk): add helper"
expect sdk/v0.2.0 "feat bumps minor"

git -C "$repo" tag sdk/v0.2.0
git -C "$repo" tag v0.300.0
commit sdk/plugin/c.go "refactor!: rename helper"
expect sdk/v0.3.0 "pre-1.0 breaking change bumps minor; app tags are ignored"

git -C "$repo" tag sdk/v1.0.0
commit sdk/plugin/d.go "fix: change API

BREAKING CHANGE: removes Helper"
(cd "$repo" && "$NEXT") > /dev/null 2>&1 && fail "breaking change after v1 chose a version"
echo "sdk-next-tag self-test: ok"
