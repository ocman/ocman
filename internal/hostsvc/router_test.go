package hostsvc

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

// stubHost is a minimal Host that records its identity for routing tests.
type stubHost struct{ id string }

func (h stubHost) RemoteID() string       { return h.id }
func (h stubHost) Capabilities() HostCaps { return HostCaps{} }
func (h stubHost) BeadsStatus(context.Context, string) (BeadsStatus, error) {
	return BeadsStatus{}, nil
}
func (h stubHost) DaguStatus(context.Context) dagu.Result {
	return dagu.Result{Status: dagu.Unavailable}
}
func (h stubHost) StartDaguWorkflow(context.Context, workflows.Definition) (dagu.Run, error) {
	return dagu.Run{}, nil
}
func (h stubHost) GetDaguRun(context.Context, string, string) (dagu.Run, error) {
	return dagu.Run{}, nil
}
func (h stubHost) CancelDaguRun(context.Context, string, string) error { return nil }
func (h stubHost) GitInfo(context.Context, []string) (map[string]git.Info, error) {
	return nil, nil
}
func (h stubHost) GitDiff(context.Context, string, GitDiffOptions) (*git.Diff, error) {
	return nil, nil
}
func (h stubHost) GitBranches(context.Context, string) ([]string, error)          { return nil, nil }
func (h stubHost) GitCheckout(context.Context, string, string) error              { return nil }
func (h stubHost) ListWorktrees(context.Context, string) ([]git.Worktree, error)  { return nil, nil }
func (h stubHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) { return "", nil }
func (h stubHost) CreateWorktreeSession(context.Context, WorktreeSessionRequest) (*WorktreeSessionResult, error) {
	return nil, nil
}
func (h stubHost) RemoveWorktree(context.Context, RemoveWorktreeRequest) error { return nil }
func (h stubHost) LaunchTmux(context.Context, LaunchTmuxRequest) (*LaunchTmuxResult, error) {
	return nil, nil
}
func (h stubHost) EnsureProjectOpencode(context.Context, EnsureProjectOpencodeRequest) (*EnsureProjectOpencodeResult, error) {
	return nil, nil
}
func (h stubHost) RestartProjectOpencode(context.Context, EnsureProjectOpencodeRequest) (*EnsureProjectOpencodeResult, error) {
	return nil, nil
}
func (h stubHost) TmuxSessions(context.Context) ([]TmuxSession, error)           { return nil, nil }
func (h stubHost) Projects(context.Context) ([]db.ProjectStats, error)           { return nil, nil }
func (h stubHost) TermWindows(context.Context, string) ([]TermWindow, error)     { return nil, nil }
func (h stubHost) TermCreateWindow(context.Context, string) (string, error)      { return "", nil }
func (h stubHost) TermKillWindow(context.Context, string, string) error          { return nil }
func (h stubHost) TermAttach(context.Context, TermAttachRequest, TermConn) error { return nil }

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
