package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMergePath(t *testing.T) {
	sep := string(os.PathListSeparator)
	j := func(parts ...string) string { return strings.Join(parts, sep) }

	tests := []struct {
		name  string
		base  string
		extra string
		want  string
	}{
		{"adds missing", j("/usr/bin"), j("/opt/homebrew/bin"), j("/usr/bin", "/opt/homebrew/bin")},
		{"dedups", j("/usr/bin"), j("/usr/bin", "/opt/homebrew/bin"), j("/usr/bin", "/opt/homebrew/bin")},
		{"base wins order", j("/a", "/b"), j("/b", "/a", "/c"), j("/a", "/b", "/c")},
		{"empty base", "", j("/opt/homebrew/bin"), j("/opt/homebrew/bin")},
		{"empty extra", j("/usr/bin"), "", j("/usr/bin")},
		{"drops empty entries", j("/usr/bin", "", "/bin"), j("", "/x"), j("/usr/bin", "/bin", "/x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergePath(tt.base, tt.extra); got != tt.want {
				t.Errorf("mergePath(%q, %q) = %q; want %q", tt.base, tt.extra, got, tt.want)
			}
		})
	}
}

// TestEnsureToolPathRecoversBinary simulates the reboot scenario: a
// stripped PATH that hides a binary, then asserts ensureToolPath
// restores the login shell's PATH so the binary is found again.
func TestEnsureToolPathRecoversBinary(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// Pick a dir on the current (full) PATH that holds a real binary.
	full := os.Getenv("PATH")
	if full == "" {
		t.Skip("no PATH")
	}
	t.Setenv("PATH", "/nonexistent-ocman-test")
	if _, err := exec.LookPath("sh"); err == nil {
		t.Skip("sh still found under stripped PATH; cannot simulate")
	}
	ensureToolPath()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Errorf("ensureToolPath did not restore sh on PATH: %v (PATH=%q)", err, os.Getenv("PATH"))
	}
}
