#!/usr/bin/env bash
# Self-test for release-tag.sh against throwaway Git repositories.
set -uo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/release-tag.sh"
repo="$(mktemp -d)"
trap 'rm -rf "$repo"' EXIT
fail() { echo "release-tag self-test: $1" >&2; exit 1; }

unset $(git rev-parse --local-env-vars)
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
git -C "$repo" init -q
git -C "$repo" config user.name test
git -C "$repo" config user.email test@example.invalid

# Tags are ordered by tagger date, so every object gets a later timestamp.
clock=1700000000
tick() { clock=$((clock + 60)); export GIT_COMMITTER_DATE="@${clock} +0000" GIT_AUTHOR_DATE="@${clock} +0000"; }
commit() { # $1=message
	tick
	echo "$RANDOM$RANDOM" >> "$repo/file"
	git -C "$repo" add -A
	git -C "$repo" commit -q -m "$1"
}
tag() { # $1=name [$2=rev]
	tick
	git -C "$repo" tag -a -m "$1" "$1" "${2:-HEAD}"
}
run() { (cd "$repo" && "$SCRIPT" "$@"); }
expect() { # $1=want $2=case $3...=args
	local want="$1" name="$2" got
	shift 2
	got="$(run "$@")" || fail "${name}: script failed"
	[ "$got" = "$want" ] || fail "${name}: want '${want}', got '${got}'"
}
expect_fail() { # $1=case $2...=args
	local name="$1"
	shift
	run "$@" > /dev/null 2>&1 && fail "${name}: want failure"
	return 0
}
head_sha() { git -C "$repo" rev-parse HEAD; }

commit "feat: first"
expect "" "no stable tag yet" latest-stable
expect "v0.1.0-dev.1" "first dev tag" dev v0.1.0
expect_fail "malformed next version" dev 0.1.0
tag v0.1.0
expect "v0.1.0" "latest stable" latest-stable
expect "" "next equal to latest stable" dev v0.1.0

commit "docs: readme"
expect "" "docs-only change is not releasable" dev v0.1.1

commit "fix(sessions): patch"
expect "v0.1.1-dev.1" "fix after stable" dev v0.1.1
tag v0.1.1-dev.1
expect "" "commit already has a dev tag" dev v0.1.1

commit "chore: tidy"
expect "" "chore since previous dev tag" dev v0.1.1

commit "feat: add page"
expect "v0.2.0-dev.1" "bump resets dev counter" dev v0.2.0
tag v0.2.0-dev.1
dev1="$(head_sha)"

commit "refactor!: rename API"
expect "v0.2.0-dev.2" "counter increments" dev v0.2.0
tag v0.2.0-dev.2
dev2="$(head_sha)"

tag sdk/v0.1.0
tag v0.2.0-dev.10 "$dev1"
commit "perf: faster"
expect "v0.2.0-dev.11" "numeric counter, sdk tags ignored" dev v0.2.0
git -C "$repo" tag -d v0.2.0-dev.10 > /dev/null

# Nightly: the newest dev tag, not HEAD.
commit "fix: untested tip"
expect "v0.2.0-nightly.20261107 ${dev2}" "nightly picks newest dev tag" nightly 20261107
expect_fail "malformed date" nightly 2026-11-07
tag v0.2.0-nightly.20261107 "$dev2"
expect "" "dev commit already shipped as nightly" nightly 20261108
expect_fail "override equal to latest stable" stable 0.1.0

# Stable promotes the newest nightly.
git -C "$repo" checkout -q --detach "$dev1"
expect "v0.1.0" "latest stable from an older commit" latest-stable
git -C "$repo" checkout -q -
expect "v0.2.0 ${dev2} v0.2.0-nightly.20261107" "promote newest nightly" stable
expect "v0.3.0 ${dev2} v0.2.0-nightly.20261107" "version override" stable 0.3.0
expect_fail "invalid override" stable 0.3
tag v0.2.0 "$dev2"
expect_fail "nightly already part of stable" stable
expect_fail "existing stable tag" stable 0.2.0

commit "fix: after release"
expect "v0.2.1-dev.1" "dev after promotion" dev v0.2.1
tag v0.2.1-dev.1
expect "v0.2.1-nightly.20261108 $(head_sha)" "nightly after promotion" nightly 20261108
tag v0.2.1-nightly.20261108
expect_fail "older override" stable 0.1.9
expect "v0.2.1 $(head_sha) v0.2.1-nightly.20261108" "promote next nightly" stable
tag v0.2.1

# CI can finish out of order: B (a child of A) is tagged before A.
commit "fix: A"
a_sha="$(head_sha)"
commit "fix: B"
b_sha="$(head_sha)"
expect "v0.2.2-dev.1" "B finishes CI first" dev v0.2.2 "$b_sha"
tag v0.2.2-dev.1 "$b_sha"
expect "" "A finishes later and is superseded by B" dev v0.2.2 "$a_sha"
# Even if the older commit gets a newer tag, ancestry decides.
tag v0.2.2-dev.2 "$a_sha"
expect "v0.2.2-nightly.20261109 ${b_sha}" "nightly follows main ancestry" nightly 20261109
tag v0.2.2-nightly.20261109 "$b_sha"
expect "" "B shipped as nightly" nightly 20261110
tag v0.2.2-nightly.20261110 "$a_sha"
expect "v0.2.2 ${b_sha} v0.2.2-nightly.20261109" "stable promotes the nearest nightly on main" stable

empty="$(mktemp -d)"
git -C "$empty" init -q
(cd "$empty" && "$SCRIPT" stable) > /dev/null 2>&1 && fail "stable without a nightly succeeded"
(cd "$empty" && "$SCRIPT" bogus) > /dev/null 2>&1 && fail "unknown command succeeded"
rm -rf "$empty"

echo "release-tag self-test: ok"
