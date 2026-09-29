package git

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// FactoryWorktreePath returns the checkout of a Factory shared branch.
func FactoryWorktreePath(ctx context.Context, repoRoot, branch string) (string, error) {
	worktrees, err := ListWorktrees(ctx, repoRoot)
	if err != nil {
		return "", err
	}
	for _, worktree := range worktrees {
		if worktree.Branch == branch {
			return worktree.Path, nil
		}
	}
	return "", errors.New("factory shared branch worktree was not found")
}

// ponytail: a line-based heuristic, not a parser. It points the validator at
// suspicious hunks; the validator decides whether they are legitimate.
var weakenedTestPattern = regexp.MustCompile(`\bt\.Skip(Now|f)?\(|\b(it|test|describe)\.(skip|only|todo)\(|\bx(it|describe)\(|@pytest\.mark\.(skip|xfail)|@unittest\.skip|//\s*nolint|eslint-disable|@ts-(ignore|expect-error|nocheck)|#\s*type:\s*ignore|#\s*noqa|--no-verify`)

var testPathPattern = regexp.MustCompile(`(_test\.go|\.(test|spec)\.[cm]?[jt]sx?|(^|/)test_[^/]*\.py)$|(^|/)(tests?|__tests__|e2e)/`)

// checkConfigPattern matches files that define how tests and linters run;
// editing them can make a declared command pass without the work.
var checkConfigPattern = regexp.MustCompile(`(^|/)(Makefile|GNUmakefile|[^/]*\.mk|package\.json|\.golangci\.ya?ml|(jest|vitest|playwright|vite|karma)\.config\.[cm]?[jt]s|\.eslintrc[^/]*|eslint\.config\.[cm]?[jt]s|tsconfig[^/]*\.json|pyproject\.toml|setup\.cfg|tox\.ini|pytest\.ini|\.coveragerc|codecov\.ya?ml|\.forgejo/workflows/[^/]+|\.github/workflows/[^/]+)$`)

// FactoryWorktreeState returns HEAD and whether the worktree has changes.
func FactoryWorktreeState(ctx context.Context, dir string) (string, bool, error) {
	head, err := gitexec.Output(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	status, err := gitexec.Output(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(head), strings.TrimSpace(status) != "", nil
}

// FactoryDiffRedFlags lists changes since target that commonly make checks
// pass without doing the work: deleted tests, edited test/lint
// configuration, and newly added skip or suppression markers. It compares
// against the remote-tracking target when present, like the workspace does.
func FactoryDiffRedFlags(ctx context.Context, dir, target string) ([]string, error) {
	baseRef := target
	if _, err := gitexec.Output(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+target); err == nil {
		baseRef = "refs/remotes/origin/" + target
	}
	base := baseRef + "...HEAD"
	names, err := gitexec.Output(ctx, dir, "diff", "--name-status", "--no-renames", base)
	if err != nil {
		return nil, fmt.Errorf("diff against %s: %w", target, err)
	}
	var flags []string
	for _, line := range strings.Split(strings.TrimSpace(names), "\n") {
		status, file, ok := strings.Cut(line, "\t")
		switch {
		case !ok:
		case status == "D" && testPathPattern.MatchString(file):
			flags = append(flags, "deleted test file "+file)
		case checkConfigPattern.MatchString(file):
			flags = append(flags, "changed check configuration "+file)
		}
	}
	patch, err := gitexec.Output(ctx, dir, "diff", "--unified=0", "--no-color", base)
	if err != nil {
		return nil, fmt.Errorf("diff against %s: %w", target, err)
	}
	file := ""
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
		case strings.HasPrefix(line, "+") && weakenedTestPattern.MatchString(line):
			flags = append(flags, fmt.Sprintf("%s adds %q", file, strings.TrimSpace(strings.TrimPrefix(line, "+"))))
		}
	}
	return flags, nil
}
