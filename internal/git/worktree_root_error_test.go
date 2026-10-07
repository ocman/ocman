package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRepoRootPreservesProbeErrors(t *testing.T) {
	for _, test := range []struct {
		name, diagnostic string
		notRepo          bool
	}{
		{name: "not a repo", diagnostic: "fatal: not a git repository", notRepo: true},
		{name: "corrupt repo", diagnostic: "fatal: object read failed"},
		{name: "cancelled", diagnostic: "fatal: probe cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bin := t.TempDir()
			script := "#!/bin/sh\nprintf '" + test.diagnostic + "\\n' >&2\nexit 128\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx := t.Context()
			if test.name == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := ResolveRepoRoot(ctx, t.TempDir())
			if err == nil || errors.Is(err, ErrNotARepo) != test.notRepo {
				t.Fatalf("error = %v, want notRepo=%v", err, test.notRepo)
			}
			if test.name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}
