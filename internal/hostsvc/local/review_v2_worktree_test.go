package local

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestV2WorktreeSessionUsesMachineRuntime(t *testing.T) {
	h, rt, store, rec, root := v2Host(t)
	repo := initRepo(t)
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy := ManagedInstance{Endpoint: "http://127.0.0.1:9999", RuntimeID: "legacy", Kind: ocruntime.KindNativeTmux}
	if err := store.Upsert(t.Context(), repo, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatal(err)
	}
	h.deps.BeforeReplace = func(context.Context, string, string) error {
		t.Error("worktree replaced unrelated legacy instance")
		return errors.New("unexpected replacement")
	}
	rt.probe = func(inst *ocruntime.Instance) bool { return inst.Endpoint != legacy.Endpoint }
	h.deps.CreateSession = func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		if req.Port != "7777" {
			t.Errorf("session port = %q, want machine port", req.Port)
		}
		return &platforms.CreateSessionResponse{ID: "worktree"}, nil
	}
	if _, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{ProjectDir: repo, Branch: "feature", NewBranch: true, BaseRef: "main"}); err != nil {
		t.Fatal(err)
	}
	if rt.launchCount() != 1 || rt.stopCount() != 0 || rec.last() != "7777" || h.currentInstance(root) == nil || !store.has(repo) {
		t.Fatal("worktree bypassed machine runtime")
	}
}

func TestV2RejectsProjectReplacementBeforeCallbacks(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			h, rt, _, _, _ := v2Host(t)
			repo := initRepo(t)
			h.setInstance(repo, &ocruntime.Instance{Endpoint: "old", ID: "legacy", RepoRoot: repo})
			rt.probe = func(*ocruntime.Instance) bool { t.Error("probed wrong v2 root"); return false }
			h.deps.BeforeReplace = func(context.Context, string, string) error { t.Error("prepared wrong v2 root"); return nil }
			h.deps.ReplacementStopped = func(context.Context, string) (bool, error) {
				t.Error("consulted replacement for wrong v2 root")
				return false, nil
			}
			fn := h.ensureLocked
			if restart {
				fn = h.restartLocked
			}
			if _, err := fn(t.Context(), repo); err == nil || rt.stopCount() != 0 || rt.launchCount() != 0 {
				t.Fatalf("wrong v2 root replaced: %v", err)
			}
		})
	}
}
