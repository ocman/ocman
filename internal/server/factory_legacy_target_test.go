package server

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/hostsvc"
	hostlocal "github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platforms"
)

func TestFactoryLegacyDetachedTarget(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	ctx := t.Context()
	if out, err := gitexec.Command(ctx, "-C", repo, "checkout", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("detach: %v: %s", err, out)
	}
	created, err := git.CreateWorktree(ctx, git.CreateWorktreeRequest{RepoRoot: repo, Branch: "factory/epic", NewBranch: true, BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{id: "test", sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{Directory: filepath.Clean(created.Path)}}, nil
	}})
	srv := New(nil, nil, "", registry, nil)
	srv.hostRouter = hostsvc.NewRouter(hostlocal.New(hostlocal.Deps{}))
	branch, target, err := (srv.factoryModule().ImplementationLauncher()).ResolveImplementationWorkspace(ctx, repo, "factory/epic", factory.PlanningSession{Platform: "test", ID: "old"})
	if err != nil || branch != "factory/epic" || target != "main" {
		t.Fatalf("legacy detached workspace = %q/%q, %v", branch, target, err)
	}
}
