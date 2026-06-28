package hostsvc

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitinfo"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// stubHost is a minimal Host that records its identity for routing tests.
type stubHost struct{ id string }

func (h stubHost) RemoteID() string          { return h.id }
func (h stubHost) Capabilities() HostCaps     { return HostCaps{} }
func (h stubHost) GitInfo(context.Context, []string) (map[string]gitinfo.Info, error) {
	return nil, nil
}
func (h stubHost) GitDiff(context.Context, string, GitDiffOptions) (*gitinfo.Diff, error) {
	return nil, nil
}
func (h stubHost) ListWorktrees(context.Context, string) ([]worktree.Entry, error) { return nil, nil }
func (h stubHost) WorktreeDefaultBaseRef(context.Context, string) (string, error)  { return "", nil }
func (h stubHost) CreateWorktreeSession(context.Context, WorktreeSessionRequest) (*WorktreeSessionResult, error) {
	return nil, nil
}
func (h stubHost) RemoveWorktree(context.Context, RemoveWorktreeRequest) error { return nil }
func (h stubHost) LaunchTmux(context.Context, LaunchTmuxRequest) (*LaunchTmuxResult, error) {
	return nil, nil
}
func (h stubHost) TmuxSessions(context.Context) ([]TmuxSession, error)  { return nil, nil }
func (h stubHost) Projects(context.Context) ([]db.ProjectStats, error)  { return nil, nil }

func TestRouter_ForRemote(t *testing.T) {
	local := stubHost{id: "local"}
	r := NewRouter(local)
	remote := stubHost{id: "abc"}
	r.RegisterRemote("abc", remote)

	if r.ForRemote("").RemoteID() != "local" {
		t.Error("empty remoteID should resolve to local")
	}
	if r.ForRemote("local").RemoteID() != "local" {
		t.Error("'local' should resolve to local")
	}
	if r.ForRemote("abc").RemoteID() != "abc" {
		t.Error("known remote should resolve to itself")
	}
	if r.ForRemote("unknown").RemoteID() != "local" {
		t.Error("unknown remote should degrade to local")
	}

	r.UnregisterRemote("abc")
	if r.ForRemote("abc").RemoteID() != "local" {
		t.Error("unregistered remote should degrade to local")
	}
}

func TestRouter_ForDir(t *testing.T) {
	local := stubHost{id: "local"}
	r := NewRouter(local)
	r.RegisterRemote("abc", stubHost{id: "abc"})

	// No resolver installed: everything is local.
	if r.ForDir("/anything").RemoteID() != "local" {
		t.Error("ForDir without resolver should be local")
	}

	// Resolver maps a specific dir to the remote.
	r.SetDirResolver(func(dir string) string {
		if dir == "/remote/proj" {
			return "abc"
		}
		return ""
	})
	if r.ForDir("/remote/proj").RemoteID() != "abc" {
		t.Error("ForDir should resolve to remote via resolver")
	}
	if r.ForDir("/local/proj").RemoteID() != "local" {
		t.Error("unmatched dir should be local")
	}
}
