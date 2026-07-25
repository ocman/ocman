// Package testutil holds the tiny cross-package test helpers that would
// otherwise be copy-pasted into every package's _test.go file.
//
// It is imported only from tests; nothing in the shipped binary depends
// on it.
package testutil

import (
	"os/exec"
	"testing"
)

// RequireGit skips the test when the `git` binary isn't on PATH. Tests
// that shell out to real git must call it first, otherwise they hard-fail
// on a machine (or minimal CI image) without git installed.
func RequireGit(t *testing.T) {
	t.Helper()
	requireBinary(t, "git")
}

// RequireTmux skips the test when the `tmux` binary isn't on PATH.
func RequireTmux(t *testing.T) {
	t.Helper()
	requireBinary(t, "tmux")
}

func requireBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed", name)
	}
}
