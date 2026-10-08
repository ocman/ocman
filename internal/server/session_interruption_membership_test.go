package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestRestartSkipsDeletedHistoricalCheckout(t *testing.T) {
	main := initWorktreeTestRepo(t)
	deleted := initWorktreeTestRepo(t)
	if err := os.RemoveAll(deleted); err != nil {
		t.Fatal(err)
	}
	srv, reg := newSessionsTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{
		{ID: "current", Directory: main}, {ID: "historical", Directory: deleted},
	}}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "turn"}, nil
	}})
	rt := &recoveryWiringRuntime{stop: func() error { return nil }}
	srv.runtime = rt
	host := srv.newLocalHost()
	srv.hostRouter = hostsvc.NewRouter(host)
	req := hostsvc.EnsureProjectOpencodeRequest{ProjectDir: main}
	if _, err := host.EnsureProjectOpencode(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := host.RestartProjectOpencode(t.Context(), req); err != nil {
		t.Fatalf("deleted unrelated checkout blocked restart: %v", err)
	}
	if rt.launches != 2 || rt.stops != 1 {
		t.Fatalf("restart did not complete: launches=%d stops=%d", rt.launches, rt.stops)
	}
	for _, id := range []string{"current", "historical"} {
		notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id)
		want := 0
		if id == "current" {
			want = 1
		}
		if err != nil || len(notices) != want {
			t.Fatalf("%s history: %v %v", id, notices, err)
		}
	}
}

func TestReplacementCanonicalMembership(t *testing.T) {
	for _, kind := range []string{"external-linked-worktree", "independent-nested-repo"} {
		for _, createdDuringPreparation := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/baseline", true: "/new-session"}[createdDuringPreparation], func(t *testing.T) {
				main := initWorktreeTestRepo(t)
				var dir string
				if kind == "external-linked-worktree" {
					dir = filepath.Join(t.TempDir(), "external")
					if err := gitexec.Command(t.Context(), "-C", main, "worktree", "add", "-b", "external", dir).Run(); err != nil {
						t.Fatal(err)
					}
				} else {
					dir = filepath.Join(main, "nested")
					if err := gitexec.Command(t.Context(), "init", dir).Run(); err != nil {
						t.Fatal(err)
					}
				}
				dir, err := filepath.EvalSymlinks(dir)
				if err != nil {
					t.Fatal(err)
				}
				srv, reg := newSessionsTestServer(t)
				fp := &interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
					return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "turn"}, nil
				}}
				reg.Register(fp)
				if !createdDuringPreparation {
					fp.sessions = []db.Session{{ID: "s", Directory: dir}}
				}
				if err := srv.recordOpencodeReplacement(t.Context(), main, "requested restart"); err != nil {
					t.Fatal(err)
				}
				baseline, err := srv.stateDB.PreparedSessionInterruptions(t.Context(), "opencode", main)
				if err != nil {
					t.Fatal(err)
				}
				_, sampled := baseline["s"]
				want := kind == "external-linked-worktree"
				if sampled != (want && !createdDuringPreparation) {
					t.Errorf("canonical baseline membership=%t want=%t", sampled, want && !createdDuringPreparation)
				}
				fp.sessions = []db.Session{{ID: "s", Directory: dir}}
				if err := srv.confirmOpencodeReplacement(t.Context(), main); err != nil {
					t.Fatal(err)
				}
				got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
				if err != nil || (len(got) == 1) != want {
					t.Fatalf("canonical history membership: notices=%v err=%v want=%t", got, err, want)
				}
			})
		}
	}
}

func TestReplacementMembershipResolvesDistinctDirectoriesOnce(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	calls := make(map[string]int)
	srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(_ context.Context, dir string) (string, error) {
		calls[dir]++
		if dir == "/non-repo" {
			return "", git.ErrNotARepo
		}
		return dir, nil
	}})
	sessions := []db.Session{{Directory: "/repo"}, {Directory: "/repo"}, {Directory: "/non-repo"}, {Directory: "/non-repo"}}
	members, err := srv.replacementMembership(t.Context(), "/repo", sessions)
	if err != nil || !members["/repo"] || members["/non-repo"] || calls["/repo"] != 1 || calls["/non-repo"] != 1 {
		t.Fatalf("membership=%v calls=%v err=%v", members, calls, err)
	}
	defer ocv2.SetInstalledV2(true)()
	members, err = srv.replacementMembership(t.Context(), "machine", sessions)
	if err != nil || !members["/non-repo"] || calls["/repo"] != 1 {
		t.Fatalf("v2 used per-project membership: %v %v %v", members, calls, err)
	}
}

func TestReplacementMembershipFailsClosedOnResolverFailure(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	resolverErr := errors.New("root lookup failed")
	srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(context.Context, string) (string, error) { return "", resolverErr }})
	if _, err := srv.replacementMembership(t.Context(), "/repo", []db.Session{{Directory: "/repo"}}); !errors.Is(err, resolverErr) {
		t.Fatalf("lost resolution failure: %v", err)
	}
	srv.hostRouter = hostsvc.NewRouter(&interruptionMembershipHost{resolve: func(context.Context, string) (string, error) { return "", fs.ErrPermission }})
	if _, err := srv.replacementMembership(t.Context(), "/repo", []db.Session{{Directory: "/repo"}}); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("permission failure was ignored: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := srv.replacementMembership(ctx, "/repo", []db.Session{{Directory: "/repo"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	srv.hostRouter = hostsvc.NewRouter(&struct{ hostsvc.Host }{})
	if _, err := srv.replacementMembership(t.Context(), "/repo", nil); err == nil {
		t.Fatal("unsupported owner silently inferred membership")
	}
}
