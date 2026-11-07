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
#       when NEXT is not a new version, REV or a later commit already has an
#       app tag, or no releasable commit landed since the previous app tag.
#
#   release-tag.sh nightly DATE [REV]
#       The next vX.Y.Z-nightly.DATE tag, placed on the nearest dev-tagged
#       commit in REV's history. Prints "TAG SHA", or nothing when that commit
#       already shipped as a nightly or stable release.
#
#   release-tag.sh stable [VERSION]
#       Promotes the nearest nightly in HEAD's history to vX.Y.Z (or
#       vVERSION). Prints "TAG SHA NIGHTLY" and fails when the nightly
#       already shipped or the version is taken. The caller must still check
#       that NIGHTLY was published.
#
# "Nearest" follows main's first-parent history, not tag creation time: CI
# runs can finish out of order, so an older commit may be tagged later.
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

# The tag matching $1 on the nearest commit in REV's first-parent history,
# REV included. Lightweight tags have no peeled object name.
nearest_tag() { # $1=regex $2=rev
	local refs commits
	refs="$(git for-each-ref --format='%(objectname) %(*objectname) %(refname:short)' 'refs/tags/v*')"
	commits="$(git rev-list --first-parent "$2")"
	RE="$1" awk '
		FNR == NR {
			sha = NF == 3 ? $2 : $1
			if ($NF ~ ENVIRON["RE"] && !(sha in tag)) tag[sha] = $NF
			next
		}
		($1 in tag) && !found { print tag[$1]; found = 1 }
	' <(printf '%s\n' "$refs") <(printf '%s\n' "$commits")
}

# App tags on REV or any commit after it.
tags_containing() { # $1=rev
	git tag -l 'v*' --contains "$1"
}

commit_of() { git rev-parse "$1^{commit}"; }

cmd_dev() {
	local next="${1:?usage: release-tag.sh dev NEXT [REV]}" rev="${2:-HEAD}"
	[[ "$next" =~ $stable_re ]] || die "NEXT must look like vX.Y.Z, got '$next'"
	[ "$next" != "$(latest_stable "$rev")" ] || return 0
	# A later commit that finished CI first already covers REV.
	[ -z "$(tags_containing "$rev")" ] || return 0

	local previous range log
	previous="$(nearest_tag '^v[0-9]' "$rev")"
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
	dev="$(nearest_tag '-dev\.[0-9]+$' "$rev")"
	[ -n "$dev" ] || return 0
	sha="$(commit_of "$dev")"
	[ -z "$(tags_containing "$sha" | first_matching "-nightly\\.|${stable_re}")" ] || return 0

	local tag="${dev%-dev.*}-nightly.${date}"
	if git rev-parse -q --verify "refs/tags/${tag}" > /dev/null; then
		die "${tag} already exists for another commit; try again tomorrow"
	fi
	echo "${tag} ${sha}"
}

cmd_stable() {
	local override="${1:-}" nightly sha latest tag
	nightly="$(nearest_tag '-nightly\.[0-9]+$' HEAD)"
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
	echo "${tag} ${sha} ${nightly}"
}

case "${1:-}" in
latest-stable) latest_stable "${2:-HEAD}" ;;
dev) shift; cmd_dev "$@" ;;
nightly) shift; cmd_nightly "$@" ;;
stable) shift; cmd_stable "$@" ;;
*) die "usage: release-tag.sh latest-stable|dev|nightly|stable ..." ;;
esac
