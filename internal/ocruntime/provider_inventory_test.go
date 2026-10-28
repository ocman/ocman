package ocruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

func TestProviderInventoryIsRootAndPlatformScoped(t *testing.T) {
	r := NewBackendRuntime("codex", "/binary", filepath.Join(testutil.ShortTempDir(t, "inv-"), "runtimes"))
	r.launch = func(context.Context, string, []string) error { return nil }
	if got, err := r.Instances("/root"); err != nil || len(got) != 0 {
		t.Fatalf("missing inventory = %v, %v", got, err)
	}
	first, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/other"}); err != nil {
		t.Fatal(err)
	}
	r.PlatformID = "claudecode"
	if _, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/root"}); err != nil {
		t.Fatal(err)
	}
	r.PlatformID = "codex"
	if err := os.Symlink(filepath.Dir(first.Endpoint[len("unix://"):]), filepath.Join(r.Directory, first.ID+"-symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Directory, first.ID+"-file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := r.Instances("/root")
	if err != nil || len(got) != 1 || got[0].Endpoint != first.Endpoint || got[0].ID != first.ID || got[0].Kind != KindProviderTmux {
		t.Fatalf("inventory = %+v, %v", got, err)
	}
	r.Directory = filepath.Join(r.Directory, first.ID+"-file")
	if _, err := r.Instances("/root"); err == nil {
		t.Fatal("accepted unreadable inventory")
	}
}
