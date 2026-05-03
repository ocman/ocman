// Package worktree wraps the bits of `git worktree` that ocman needs
// to launch isolated agent sessions. It computes deterministic on-disk
// paths for new worktrees, runs `git worktree add/list`, and surfaces
// typed errors for the situations the UI needs to react to (branch
// already checked out elsewhere, path conflict, etc.).
//
// The package shells out to git — there is no go-git dependency. This
// matches the existing internal/gitinfo package, keeps the binary
// small, and ensures we behave identically to the user's own git.
package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// maxSlugLength caps the on-disk slug so paths stay manageable on
// every filesystem we care about. Branches longer than this get a
// hash suffix appended for collision resistance.
const maxSlugLength = 96

// hashSuffixLen is how many hex chars of sha256 we append when
// disambiguating long or otherwise-collapsing slugs.
const hashSuffixLen = 8

// SlugForBranch turns a git branch name into a filesystem-safe path
// segment. The slug is one-way: callers must keep the original branch
// name around for git invocations.
//
// Rules (AD-9):
//   - Lowercase the whole string.
//   - Replace `/` with `-`.
//   - Drop any character not in [a-z0-9._-].
//   - Collapse runs of `-` to a single `-`.
//   - Trim leading/trailing `-` and `.`.
//   - If the result is empty, return "wt-<hash>".
//   - If the result is longer than maxSlugLength, truncate and append
//     "-<hash>" to keep collisions rare.
func SlugForBranch(branch string) string {
	lower := strings.ToLower(branch)

	// Replace `/` with `-` and strip unsafe chars in one pass.
	var b strings.Builder
	b.Grow(len(lower))
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == '-':
			b.WriteRune(r)
		case r == '/':
			b.WriteRune('-')
		default:
			// Drop the character; emit a `-` so adjacent allowed
			// chars don't accidentally fuse (e.g. "féa" must
			// become "f-a", not "fa"). Runs collapse below.
			b.WriteRune('-')
		}
	}
	slug := b.String()

	// Collapse runs of `-` to a single `-`.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}

	// Trim leading/trailing `-` and `.`.
	slug = strings.Trim(slug, "-.")

	if slug == "" {
		return "wt-" + branchHash(branch, hashSuffixLen)
	}

	if len(slug) > maxSlugLength {
		// Reserve room for "-<hash>".
		head := slug[:maxSlugLength-hashSuffixLen-1]
		head = strings.TrimRight(head, "-.")
		return head + "-" + branchHash(branch, hashSuffixLen)
	}

	return slug
}

// branchHash returns the first n hex chars of sha256(branch). Used to
// disambiguate slugs that would otherwise collapse to the empty string
// or get truncated.
func branchHash(branch string, n int) string {
	sum := sha256.Sum256([]byte(branch))
	return hex.EncodeToString(sum[:])[:n]
}
