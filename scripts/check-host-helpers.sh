#!/usr/bin/env bash
# Fails CI when an HTTP handler in internal/server, or a tool in
# internal/mcp, calls a host-local helper directly instead of going
# through the hostsvc.Host seam (architecture.md AD-16, rule R-A).
#
# Directory-scoped host operations (git, worktree, tmux, terminals) must
# be resolved via s.resolveOwner(w, dir, remoteId) / s.router().ForDir(dir)
# and delegated to a hostsvc.Host. The local Host
# (internal/hostsvc/local) is the only place that may call
# git.*/gitexec.*/term.* directly; the server-package tmux launchers live
# in tmux.go and are wired into the local Host from host.go.
#
# internal/mcp is covered too: MCP tools split work into worktrees and
# cancel sessions, and they get owner-routed adapters injected by the
# server package (mcp.WorktreeSessionCreator, mcp.GitContextReader,
# mcp.TmuxTargetKiller) instead of importing git/tmux themselves.
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

# ripgrep is not preinstalled on every CI image. Without this check the
# `rg ... || true` calls below swallow exit 127 and the guard passes
# vacuously — the exact silent rot this guard exists to prevent.
command -v rg >/dev/null 2>&1 || {
	echo "check-host-helpers: ripgrep (rg) is required but not installed" >&2
	exit 1
}

# Naked host-helper call patterns. These are function *calls* (note the
# trailing "("), so sentinel-error and type references like git.ErrNotRepo
# or hostsvc.WorktreeSessionResult are not flagged.
#
# Keep this list in sync with the exported host-local helpers: `rg
# '^func [A-Z]' internal/git internal/gitexec internal/tmux
# internal/term`. Pure
# predicates and name derivations (git.SlugForBranch, tmux.IsAvailable,
# tmux.SessionNameForPath, term.IsWindowForDir, term.WindowPrefix) are
# deliberately absent — they touch no host state, so a handler calling
# them still works for a remote project.
PATTERNS=(
	# internal/git — repository reads and mutations
	"git\\.GetDiff\\("
	"git\\.Lookup\\("
	"git\\.LookupMany\\("
	"git\\.ListBranches\\("
	"git\\.Checkout\\("
	"git\\.ListWorktrees\\("
	"git\\.CreateWorktree\\("
	"git\\.RemoveWorktree\\("
	"git\\.ResolveRepoRoot\\("
	"git\\.ResolveBaseRef\\("
	# internal/tmux — process control on this machine
	"tmux\\.ListClients\\("
	"tmux\\.ListSessions\\("
	"tmux\\.ListWindows\\("
	"tmux\\.SwitchClient\\("
	"tmux\\.KillTarget\\("
	"tmux\\.LaunchOpencode[A-Za-z]*\\("
	"tmux\\.RestartOpencode[A-Za-z]*\\("
	# internal/term — in-app terminal windows and PTYs
	"term\\.AttachLocalPTY\\("
	"term\\.CreateWindow\\("
	"term\\.Windows\\("
	"term\\.KillWindow\\("
	# internal/gitexec — raw git subprocesses on *this* machine. Without
	# these, wrapping the bypass one layer lower (gitexec.Output(ctx, dir,
	# "diff", "--stat")) slipped past the git.*/tmux.* patterns entirely.
	# gitexec.CleanEnv is deliberately absent: it only builds an env slice.
	"gitexec\\.Output\\("
	"gitexec\\.Command\\("
)

# Files allowed to reference these directly:
#   tmux.go        — defines the tmux launchers themselves
#   host.go        — wires server helpers into the local Host, and holds
#                    the deliberate fail-closed tmux kill for legacy MCP
#                    child sessions (marked ocman:allow-host-helper)
EXCLUDES=(
	"--glob" "!internal/server/*_test.go"
	"--glob" "!internal/mcp/*_test.go"
	"--glob" "!internal/server/tmux.go"
	"--glob" "!internal/server/term.go"
	"--glob" "!internal/server/host.go"
)

FOUND=0
for pattern in "${PATTERNS[@]}"; do
	matches=$(
		rg -n --no-messages \
			--glob 'internal/server/*.go' \
			--glob 'internal/mcp/*.go' \
			"${EXCLUDES[@]}" \
			"$pattern" "$ROOT" || true
	)
	if [[ -n "$matches" ]]; then
		offenders=$(echo "$matches" | grep -v 'ocman:allow-host-helper' || true)
		if [[ -n "$offenders" ]]; then
			echo "Host-helper bypass detected (route through hostsvc.Host: s.router().ForDir/ForRemote, or an injected owner-routed adapter):" >&2
			echo "$offenders" >&2
			FOUND=1
		fi
	fi
done

if [[ $FOUND -eq 0 ]]; then
	echo "check-host-helpers: no naked host-helper calls in server handlers or MCP tools."
fi

exit $FOUND
