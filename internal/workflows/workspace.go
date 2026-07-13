package workflows

import (
	"fmt"
	"path"
	"strings"
)

// Workspace lease modes. Exclusive is the default: it owns a whole shard
// so no other mutator can share it. Path narrows ownership to declared
// scopes so disjoint scopes can share one shard.
const (
	LeaseExclusive = "exclusive"
	LeasePath      = "path"
)

// WorkspaceConfig declares a run-owned bounded pool of worktree shards for
// a single repository. Shards are created lazily through the host/worktree
// service as mutating nodes acquire leases. Host is an optional owning-host
// identity; the first scheduler always uses the local host.
type WorkspaceConfig struct {
	Repo   string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Shards int    `json:"shards" yaml:"shards"`
	Host   string `json:"host,omitempty" yaml:"host,omitempty"`
	// RestrictedGit overrides the default set of repository-wide git
	// mutations denied to path-leased mutators. Empty uses the defaults.
	RestrictedGit []string `json:"restrictedGit,omitempty" yaml:"restrictedGit,omitempty"`
}

// LeaseConfig declares how a node acquires workspace ownership. Mode is
// exclusive (default) or path. Path mode requires declared normalized
// scopes and may coexist with other path leases only when scopes do not
// overlap. Commit marks a coordinator node that owns repository-wide git
// mutation (staging/commit/push) via exclusive or serialized per-shard
// capacity.
type LeaseConfig struct {
	Mode   string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	Paths  []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Commit bool     `json:"commit,omitempty" yaml:"commit,omitempty"`
}

// normalizeLeasePath cleans a declared path scope to a canonical form for
// overlap comparison: forward-slash separators, no leading "./", no
// trailing slash, and rejection of absolute paths or any component that
// escapes the shard root ("..").
func normalizeLeasePath(scope string) (string, error) {
	if scope == "" {
		return "", fmt.Errorf("lease path is required")
	}
	if strings.ContainsRune(scope, 0) {
		return "", fmt.Errorf("lease path contains NUL")
	}
	unified := strings.ReplaceAll(scope, "\\", "/")
	if strings.HasPrefix(unified, "/") {
		return "", fmt.Errorf("lease path %q must be relative", scope)
	}
	clean := path.Clean(unified)
	if clean == "." {
		return "", fmt.Errorf("lease path %q must not be the shard root", scope)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("lease path %q escapes the shard", scope)
	}
	return clean, nil
}

// leasePathsOverlap reports whether two normalized path scopes conflict:
// identical, or one is an ancestor directory of the other. Disjoint
// siblings (e.g. "src/a" and "src/b") do not overlap even though they
// share a prefix string.
func leasePathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return isAncestorPath(a, b) || isAncestorPath(b, a)
}

// isAncestorPath reports whether ancestor is a proper parent directory of
// descendant. Component-aware: "src/a" is an ancestor of "src/a/b" but not
// of "src/ab".
func isAncestorPath(ancestor, descendant string) bool {
	return strings.HasPrefix(descendant, ancestor+"/")
}

// normalizedLeasePaths normalizes and de-duplicates a node's declared path
// scopes, rejecting scopes that overlap each other within the same node.
func normalizedLeasePaths(scopes []string) ([]string, error) {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		clean, err := normalizeLeasePath(scope)
		if err != nil {
			return nil, err
		}
		for _, existing := range out {
			if leasePathsOverlap(existing, clean) {
				return nil, fmt.Errorf("lease paths %q and %q overlap", existing, clean)
			}
		}
		out = append(out, clean)
	}
	return out, nil
}

// scopesConflict reports whether any scope in a overlaps any scope in b.
// Used to decide whether two path leases can share a shard.
func scopesConflict(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if leasePathsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

// defaultRestrictedGitSubcommands are the repository-wide git mutations a
// path-leased mutator may not run: a centralized coordinator owns staging,
// reset, stash, commit, and push. A workflow may extend this set via
// WorkspaceConfig.RestrictedGit.
var defaultRestrictedGitSubcommands = []string{
	"add", "checkout", "commit", "merge", "push", "rebase", "reset",
	"restore", "stash", "switch",
}

// gitMutationDenied reports whether command is a git invocation whose
// subcommand is in the restricted set. It parses past leading global git
// options (e.g. `git -C <dir> commit`) so a path-leased agent cannot slip a
// commit through by prefixing flags. Non-git commands are never denied here;
// per-command bash permission rules still apply independently.
func gitMutationDenied(command []string, restricted []string) bool {
	sub := gitSubcommand(command)
	if sub == "" {
		return false
	}
	for _, r := range restricted {
		if sub == r {
			return true
		}
	}
	return false
}

// gitSubcommand returns the git subcommand for a command invocation, or ""
// when the command does not invoke git. It skips a leading shell wrapper
// (/bin/sh -c "..."), resolves the git binary by base name, and skips
// value-taking global options.
func gitSubcommand(command []string) string {
	args := unwrapShell(command)
	if len(args) == 0 {
		return ""
	}
	if path.Base(args[0]) != "git" {
		return ""
	}
	args = args[1:]
	for len(args) > 0 {
		arg := args[0]
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
		// Global options that consume the following token.
		switch arg {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			if len(args) >= 2 {
				args = args[2:]
				continue
			}
			return ""
		}
		args = args[1:]
	}
	return ""
}

// unwrapShell flattens `sh -c "git ..."` style wrappers into their token
// stream so the git subcommand is visible to the restriction check. Only the
// simple, unambiguous single-command form is unwrapped; anything with shell
// operators is left intact (the surrounding bash permission rule governs it).
func unwrapShell(command []string) []string {
	if len(command) < 3 {
		return command
	}
	base := path.Base(command[0])
	if base != "sh" && base != "bash" {
		return command
	}
	if command[1] != "-c" {
		return command
	}
	script := command[2]
	if strings.ContainsAny(script, "|&;<>()`$") {
		return command
	}
	return strings.Fields(script)
}
