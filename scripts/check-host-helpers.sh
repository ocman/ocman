#!/usr/bin/env bash
# Fails CI when an HTTP handler in internal/server calls a host-local
# helper directly instead of going through the hostsvc.Host seam
# (architecture.md AD-16, rule R-A).
#
# Directory-scoped host operations (git, worktree, tmux launch) must be
# resolved via s.router().ForDir(dir) / ForRemote(remoteId) and delegated
# to a hostsvc.Host. The local Host (internal/hostsvc/local) is the only
# place that may call gitinfo.*/worktree.* directly; the server-package
# tmux launchers live in tmux.go and are wired into the local Host from
# host.go.
#
# This keeps remote support automatic: a new host feature added on the
# seam works for remotes without the handler knowing. A handler that
# shells out directly would silently never work for a remote project.
#
# Suppress a justified exception with `// ocman:allow-host-helper` on the
# same line.
#
# Exit code: 0 on clean, 1 on violations.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Naked host-helper call patterns. These are function *calls* (note the
# trailing "("), so sentinel-error and type references like
# gitinfo.ErrNotRepo or hostsvc.WorktreeSessionResult are not flagged.
PATTERNS=(
	"gitinfo\\.GetDiff\\("
	"gitinfo\\.Lookup\\("
	"gitinfo\\.LookupMany\\("
	"worktree\\.Create\\("
	"worktree\\.List\\("
	"worktree\\.ResolveRepoRoot\\("
	"worktree\\.ResolveBaseRef\\("
	"launchOpencodeInTmux\\("
	"launchOpencodeInProjectTmuxWindow\\("
)

# Files allowed to reference these directly:
#   tmux.go        — defines the tmux launchers themselves
#   host.go        — wires server helpers into the local Host
#   handlers_mcp.go, handlers_project_handle.go — MCP launch path
#     (per-host MCP, out of v1 remote scope); passes the launcher by value
EXCLUDES=(
	"--glob" "!internal/server/*_test.go"
	"--glob" "!internal/server/tmux.go"
	"--glob" "!internal/server/host.go"
	"--glob" "!internal/server/handlers_mcp.go"
	"--glob" "!internal/server/handlers_project_handle.go"
)

FOUND=0
for pattern in "${PATTERNS[@]}"; do
	matches=$(
		rg -n --no-messages \
			--glob 'internal/server/*.go' \
			"${EXCLUDES[@]}" \
			"$pattern" "$ROOT" || true
	)
	if [[ -n "$matches" ]]; then
		offenders=$(echo "$matches" | grep -v 'ocman:allow-host-helper' || true)
		if [[ -n "$offenders" ]]; then
			echo "Host-helper bypass detected (use s.router().ForDir/ForRemote -> hostsvc.Host):" >&2
			echo "$offenders" >&2
			FOUND=1
		fi
	fi
done

if [[ $FOUND -eq 0 ]]; then
	echo "check-host-helpers: no naked host-helper calls in server handlers."
fi

exit $FOUND
