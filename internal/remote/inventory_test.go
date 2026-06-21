package remote

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func newInvManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	return m
}

func TestManager_ResolveTargets(t *testing.T) {
	m := newInvManager(t)
	// Seed remote inventory: remote "abc" has the github.com/org/repo project.
	m.invMu.Lock()
	m.inventory["abc"] = []ProjectIdentity{
		{Key: "github.com/org/repo", Origin: "https://github.com/org/repo", Dir: "/remote/repo"},
	}
	m.invMu.Unlock()

	local := []ProjectIdentity{
		{Key: "github.com/org/repo", Origin: "git@github.com:org/repo.git", Dir: "/local/repo"},
	}

	// Same origin on both -> two candidates (local + remote).
	cands := m.ResolveTargets("/local/repo", "git@github.com:org/repo.git", local)
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(cands), cands)
	}
	var haveLocal, haveRemote bool
	for _, c := range cands {
		if c.RemoteID == "local" {
			haveLocal = true
		}
		if c.RemoteID == "abc" {
			haveRemote = true
			if c.Platform != "r-abc:opencode" {
				t.Errorf("remote candidate platform = %q", c.Platform)
			}
		}
	}
	if !haveLocal || !haveRemote {
		t.Fatalf("missing candidate: local=%v remote=%v", haveLocal, haveRemote)
	}

	// A project only the local has -> one candidate.
	one := m.ResolveTargets("/local/other", "git@github.com:org/other.git",
		[]ProjectIdentity{{Key: "github.com/org/other", Dir: "/local/other"}})
	if len(one) != 1 || one[0].RemoteID != "local" {
		t.Fatalf("expected 1 local candidate, got %+v", one)
	}

	// Unknown project -> zero candidates.
	none := m.ResolveTargets("/nowhere", "", nil)
	if len(none) != 0 {
		t.Fatalf("expected 0 candidates, got %+v", none)
	}
}

func TestManager_ResolveDir(t *testing.T) {
	m := newInvManager(t)
	m.invMu.Lock()
	m.inventory["abc"] = []ProjectIdentity{{Key: "k", Dir: "/remote/repo"}}
	m.invMu.Unlock()

	if got := m.resolveDir("/remote/repo"); got != "abc" {
		t.Errorf("resolveDir(/remote/repo) = %q, want abc", got)
	}
	if got := m.resolveDir("/local/repo"); got != "" {
		t.Errorf("resolveDir(/local/repo) = %q, want local", got)
	}
}
