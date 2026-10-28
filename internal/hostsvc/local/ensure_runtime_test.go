package local

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
)

func TestEnsureRuntimeDispatch(t *testing.T) {
	h := New(Deps{PlatformEnabled: func(id string) bool { return id == "fake" || id == "unknown" }, Runtimes: map[string]func(context.Context, string) (*hostsvc.EnsureRuntimeResult, error){
		"fake": func(_ context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
			return &hostsvc.EnsureRuntimeResult{Endpoint: "unix:///tmp/provider.sock", RepoRoot: dir}, nil
		},
	}})
	got, err := h.EnsureRuntime(t.Context(), "fake", "/repo")
	if err != nil || got.Endpoint != "unix:///tmp/provider.sock" || got.Port() != "" || got.RepoRoot != "/repo" {
		t.Fatalf("runtime = %+v, %v", got, err)
	}
	for _, id := range []string{"unknown", "opencode", ""} {
		if _, err := h.EnsureRuntime(t.Context(), id, "/repo"); err == nil {
			t.Fatalf("%q runtime should reject before launching", id)
		}
	}
}

func TestDoctorSkipsDisabledOpenCode(t *testing.T) {
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(bin string) (string, error) {
		if bin == "opencode" {
			t.Fatal("looked up disabled OpenCode")
		}
		return "", errors.New("not installed")
	}
	h := New(Deps{PlatformEnabled: func(string) bool { return false }})
	for _, check := range h.Doctor(t.Context()) {
		if check.ID == "opencode" {
			t.Fatal("disabled OpenCode advertised")
		}
	}
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err == nil {
		t.Fatal("disabled legacy ensure accepted")
	}
}

func TestUnknownWorktreePlatformCreatesNothing(t *testing.T) {
	repo := initRepo(t)
	h := New(Deps{PlatformEnabled: func(string) bool { return false }})
	if _, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{Platform: "unknown", ProjectDir: repo, Branch: "feature", NewBranch: true}); err == nil {
		t.Fatal("unknown platform accepted")
	}
	worktrees, err := git.ListWorktrees(t.Context(), repo)
	if err != nil || len(worktrees) != 1 {
		t.Fatalf("worktrees = %+v, %v", worktrees, err)
	}
}
