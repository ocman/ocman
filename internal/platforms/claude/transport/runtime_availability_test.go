package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeNativeRuntimeAvailability(t *testing.T) {
	for _, bins := range [][]string{nil, {"claude"}, {"tmux"}, {"claude", "tmux"}} {
		t.Run(filepath.Join(bins...), func(t *testing.T) {
			dir := t.TempDir()
			for _, bin := range bins {
				if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			p := &claudePlatform{cli: filepath.Join(dir, "claude")}
			availability, ok := any(p).(interface{ RuntimeAvailable() bool })
			if !ok {
				t.Fatal("Claude browsing adapter does not report native runtime prerequisites")
			}
			if got, want := availability.RuntimeAvailable(), len(bins) == 2; got != want {
				t.Fatalf("runtime available = %v, want %v", got, want)
			}
		})
	}
}
