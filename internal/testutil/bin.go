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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}
