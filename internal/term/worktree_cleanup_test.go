package term

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

func TestWorktreeTerminalPaneChecks(t *testing.T) {
	for _, tc := range []struct {
		out  string
		busy bool
	}{
		{"", false}, {"/other\t/other\n", false}, {"/repo-other\t/elsewhere\n", false},
		{"/repo\t/other\n", true}, {"/other\t/repo/subdir\n", true}, {"/repo/subdir\t/other\n", true},
	} {
		if err := checkPanePaths("/repo", tc.out); (err != nil) != tc.busy {
			t.Fatalf("pane paths %q = %v", tc.out, err)
		}
	}
}

func TestWorktreeTerminalInspectionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, tc := range []struct {
		script string
		allow  bool
	}{
		{"#!/bin/sh\necho 'no server running' >&2\nexit 1\n", true},
		{"#!/bin/sh\necho 'permission denied' >&2\nexit 1\n", false},
		{"#!/bin/sh\necho '/repo/subdir'\n", false},
		{"#!/bin/sh\necho '/elsewhere'\n", true},
	} {
		testutil.WriteScript(t, filepath.Join(dir, "tmux"), tc.script)
		if err := CheckWorktreeIdle(t.Context(), "/repo"); (err == nil) != tc.allow {
			t.Fatalf("inspection = %v, allow %v", err, tc.allow)
		}
	}
}
