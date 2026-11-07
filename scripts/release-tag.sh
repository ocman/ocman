#!/usr/bin/env bash
# Chooses the next app release tag for one of three channels. All tags are
# created on Forgejo; see docs/other/releases.md for the full flow.
#
#   release-tag.sh latest-stable [REV]
#       The newest vX.Y.Z tag reachable from REV (default HEAD), or nothing.
#
#   release-tag.sh dev NEXT [REV]
#       The next vX.Y.Z-dev.N tag for REV, where NEXT is the stable version
#       git-cliff would bump to (`git-cliff --bumped-version`). Prints nothing
#       when NEXT is not a new version, REV already has a dev tag, or no
#       releasable commit landed since the previous app tag.
#
#   release-tag.sh nightly DATE [REV]
#       The next vX.Y.Z-nightly.DATE tag, placed on the newest dev tag
#       reachable from REV. Prints "TAG SHA", or nothing when that commit
#       already shipped as a nightly or stable release.
#
#   release-tag.sh stable [VERSION]
#       Promotes the newest nightly to vX.Y.Z (or vVERSION). Prints "TAG SHA"
#       and fails when the nightly already shipped or the version is taken.
#
# Releasable commits use the visible Conventional Commit types in cliff.toml.
set -euo pipefail

stable_re='^v[0-9]+\.[0-9]+\.[0-9]+$'
releasable_re='^(feat|fix|perf|refactor)(\([^)]*\))?!?:|^BREAKING[ -]CHANGE:'

die() { echo "release-tag: $*" >&2; exit 1; }

# Reads every line before choosing, so git cannot fail with SIGPIPE under
# pipefail, and still reports git's own failures.
first_matching() { # $1=regex; stdin=tags
	# ENVIRON, not -v: -v would interpret the regex's backslash escapes.
	RE="$1" awk '$0 ~ ENVIRON["RE"] && !found { print; found = 1 }'
}

latest_stable() { # $1=rev
	git tag -l 'v*' --merged "$1" --sort=-version:refname | first_matching "$stable_re"
}

# Newest tag of a channel by tagger date: version sort cannot order prerelease
# suffixes against their stable versions without extra git configuration.
latest_channel() { # $1=glob $2=rev (optional)
	if [ -n "${2:-}" ]; then
		git tag -l "$1" --merged "$2" --sort=-creatordate | first_matching .
	else
		git tag -l "$1" --sort=-creatordate | first_matching .
	fi
}

commit_of() { git rev-parse "$1^{commit}"; }

tags_at() { # $1=rev $2=glob
	git tag -l "$2" --points-at "$1"
}

cmd_dev() {
	local next="${1:?usage: release-tag.sh dev NEXT [REV]}" rev="${2:-HEAD}"
	[[ "$next" =~ $stable_re ]] || die "NEXT must look like vX.Y.Z, got '$next'"
	[ "$next" != "$(latest_stable "$rev")" ] || return 0
	[ -z "$(tags_at "$rev" 'v*-dev.*')" ] || return 0

	local previous range log
	previous="$(git tag -l 'v*' --merged "$rev" --sort=-creatordate | first_matching '^v[0-9]')"
	range="$rev"
	[ -z "$previous" ] || range="${previous}..${rev}"
	# Captured first: `grep -q` exits early and would SIGPIPE git.
	log="$(git log --format='%s%n%b' "$range")"
	grep -qE "$releasable_re" <<< "$log" || return 0

	local n=0 tag
	while IFS= read -r tag; do
		tag="${tag##*-dev.}"
		[[ "$tag" =~ ^[0-9]+$ ]] && [ "$tag" -gt "$n" ] && n="$tag"
	done < <(git tag -l "${next}-dev.*")
	echo "${next}-dev.$((n + 1))"
}

cmd_nightly() {
	local date="${1:?usage: release-tag.sh nightly DATE [REV]}" rev="${2:-HEAD}"
	[[ "$date" =~ ^[0-9]{8}$ ]] || die "DATE must be YYYYMMDD, got '$date'"

	local dev sha
	dev="$(latest_channel 'v*-dev.*' "$rev")"
	[ -n "$dev" ] || return 0
	sha="$(commit_of "$dev")"
	[ -z "$(tags_at "$sha" 'v*-nightly.*')" ] || return 0
	[ -z "$(tags_at "$sha" 'v*' | first_matching "$stable_re")" ] || return 0

	local tag="${dev%-dev.*}-nightly.${date}"
	if git rev-parse -q --verify "refs/tags/${tag}" > /dev/null; then
		die "${tag} already exists for another commit; try again tomorrow"
	fi
	echo "${tag} ${sha}"
}

cmd_stable() {
	local override="${1:-}" nightly sha latest tag
	nightly="$(latest_channel 'v*-nightly.*')"
	[ -n "$nightly" ] || die "no nightly to promote; stable releases ship a published nightly"
	sha="$(commit_of "$nightly")"

	if [ -n "$override" ]; then
		tag="v${override#v}"
		[[ "$tag" =~ $stable_re ]] || die "version must look like X.Y.Z, got '$override'"
	else
		tag="${nightly%-nightly.*}"
	fi

	latest="$(git tag -l 'v*' --sort=-version:refname | first_matching "$stable_re")"
	if [ -n "$latest" ]; then
		if git merge-base --is-ancestor "$sha" "$(commit_of "$latest")"; then
			die "${nightly} is already part of ${latest}; wait for a newer nightly"
		fi
		if [ "$tag" = "$latest" ] ||
			[ "$(printf '%s\n%s\n' "${latest#v}" "${tag#v}" | sort -V | sed -n '2p')" != "${tag#v}" ]; then
			die "${tag} is not newer than ${latest}"
		fi
	fi
	if git rev-parse -q --verify "refs/tags/${tag}" > /dev/null; then
		die "${tag} already exists"
	fi
	echo "${tag} ${sha}"
}

case "${1:-}" in
latest-stable) latest_stable "${2:-HEAD}" ;;
dev) shift; cmd_dev "$@" ;;
nightly) shift; cmd_nightly "$@" ;;
stable) shift; cmd_stable "$@" ;;
*) die "usage: release-tag.sh latest-stable|dev|nightly|stable ..." ;;
esac
