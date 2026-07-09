package local

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

// TestEnsureProjectOpencode_ReturnsRunningInstance: an already-discoverable
// instance returns its port and launches nothing.
func TestEnsureProjectOpencode_ReturnsRunningInstance(t *testing.T) {
	repo := initRepo(t)
	var launches int
	h := New(Deps{
		DiscoverPort: func(string) string { return "5555" },
		LaunchProjectOpencode: func(string, string) (string, error) {
			launches++
			return "", nil
		},
	})
	res, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("EnsureProjectOpencode: %v", err)
	}
	if res.Port != "5555" {
		t.Errorf("Port = %q; want 5555", res.Port)
	}
	if res.RepoRoot == "" {
		t.Error("RepoRoot should be resolved for observability")
	}
	if launches != 0 {
		t.Errorf("launched %d times; want 0 (instance already running)", launches)
	}
}

// TestEnsureProjectOpencode_LaunchesAndSeeds: no instance -> launches once,
// seeds OPENCODE_PERMISSION with a scoped external_directory rule, waits
// for the port, returns it.
func TestEnsureProjectOpencode_LaunchesAndSeeds(t *testing.T) {
	repo := initRepo(t)
	var launches int
	var gotPerm string
	var gotDir string
	calls := 0
	h := New(Deps{
		// Miss first, then hit after launch (simulates the process binding
		// its port).
		DiscoverPort: func(string) string {
			calls++
			if calls == 1 {
				return ""
			}
			return "6666"
		},
		LaunchProjectOpencode: func(dir, perm string) (string, error) {
			launches++
			gotDir = dir
			gotPerm = perm
			return "sess-name", nil
		},
	})
	res, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("EnsureProjectOpencode: %v", err)
	}
	if launches != 1 {
		t.Errorf("launched %d times; want exactly 1", launches)
	}
	if res.Port != "6666" {
		t.Errorf("Port = %q; want 6666", res.Port)
	}
	if res.TmuxSession != "sess-name" {
		t.Errorf("TmuxSession = %q; want sess-name", res.TmuxSession)
	}
	if gotDir != res.RepoRoot {
		t.Errorf("launched dir %q != resolved repo root %q", gotDir, res.RepoRoot)
	}
	// The permission JSON must carry a scoped external_directory rule for
	// this project's .worktrees/<repo> root, not a blanket allow.
	if !strings.Contains(gotPerm, "external_directory") {
		t.Errorf("permission JSON missing external_directory: %q", gotPerm)
	}
	if !strings.Contains(gotPerm, ".worktrees") {
		t.Errorf("permission JSON not scoped to .worktrees: %q", gotPerm)
	}
	if strings.Contains(gotPerm, `"*":"allow"`) {
		t.Errorf("permission JSON must not blanket-allow: %q", gotPerm)
	}
}

// TestEnsureProjectOpencode_Idempotent: two sequential calls launch at most
// once (the second call discovers the instance the first launched).
func TestEnsureProjectOpencode_Idempotent(t *testing.T) {
	repo := initRepo(t)
	launches := 0
	discovered := false
	h := New(Deps{
		DiscoverPort: func(string) string {
			if discovered {
				return "7777"
			}
			return ""
		},
		LaunchProjectOpencode: func(string, string) (string, error) {
			launches++
			discovered = true // launch makes the instance discoverable
			return "s", nil
		},
	})
	ctx := context.Background()
	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if launches != 1 {
		t.Errorf("launched %d times; want exactly 1 across two calls", launches)
	}
}

// TestEnsureProjectOpencode_NonRepo: a directory that is not a git repo
// returns git.ErrNotARepo.
func TestEnsureProjectOpencode_NonRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	h := New(Deps{
		DiscoverPort:          func(string) string { return "" },
		LaunchProjectOpencode: func(string, string) (string, error) { return "", nil },
	})
	_, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	if !errors.Is(err, git.ErrNotARepo) {
		t.Fatalf("err = %v; want git.ErrNotARepo", err)
	}
}

// TestEnsureProjectOpencode_WaitTimeout: launch succeeds but the port never
// becomes discoverable -> a timeout error, launched exactly once.
func TestEnsureProjectOpencode_WaitTimeout(t *testing.T) {
	repo := initRepo(t)
	launches := 0
	h := New(Deps{
		DiscoverPort: func(string) string { return "" }, // never discoverable
		LaunchProjectOpencode: func(string, string) (string, error) {
			launches++
			return "s", nil
		},
	})
	h.portWaitTimeout = 30 * time.Millisecond
	h.portWaitInterval = 5 * time.Millisecond
	_, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err == nil {
		t.Fatal("expected a timeout error when the port never binds")
	}
	if launches != 1 {
		t.Errorf("launched %d times; want exactly 1", launches)
	}
}
