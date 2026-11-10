package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func namedRecoveryEnv(t *testing.T) (*recoveryEnv, []platformconnections.Connection) {
	t.Helper()
	e := newRecoveryEnv(t)
	if err := e.state.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	e.state, err = state.Open(filepath.Join(e.data, "state.db"), platformconnections.Schema)
	if err != nil {
		t.Fatal(err)
	}
	s := platformconnections.New(e.state)
	if _, err := s.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	var result []platformconnections.Connection
	for _, name := range []string{"A", "B"} {
		config := filepath.Join(e.data, "config-"+name)
		if err := os.Mkdir(config, 0o700); err != nil {
			t.Fatal(err)
		}
		c, err := s.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: name, Configuration: "isolated", Directory: canonical(t, config), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, c)
	}
	return e, result
}

func TestNamedClaudeSameProjectAndRestart(t *testing.T) {
	e, connections := namedRecoveryEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	start := func(c platformconnections.Connection) *claudePlatform {
		t.Helper()
		p, err := newConnectionAt(ctx, c, e.data, e.exe, e.state, nil, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	a, b := start(connections[0]), start(connections[1])
	left, err := a.EnsureRuntime(ctx, e.repo)
	if err != nil {
		t.Fatal(err)
	}
	right, err := b.EnsureRuntime(ctx, e.repo)
	if err != nil {
		t.Fatal(err)
	}
	if left.Endpoint == right.Endpoint || left.Runtime.ID == right.Runtime.ID {
		t.Fatal("accounts shared supervisor")
	}
	if a.ID() != "claudecode" || b.ID() != "claudecode" {
		t.Fatal("native kind changed")
	}
	if a.projects.statePath == b.projects.statePath || a.launchPath == b.launchPath || a.marketplace == b.marketplace {
		t.Fatal("account writes share paths")
	}
	if a.slots != filepath.Join(e.data, "claude-slots") || a.slots != b.slots {
		t.Fatal("owner slot budget split")
	}
	if _, exists, err := e.state.GetManagedProvider(ctx, "claudecode", e.repo); err != nil || exists {
		t.Fatalf("legacy row changed: %v %v", exists, err)
	}
	created, err := a.CreateSession(ctx, platforms.CreateSessionRequest{Directory: e.repo, Title: "account A"})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(a.projects.statePath); err != nil || !strings.Contains(string(data), created.ID) {
		t.Fatalf("selected ownership: %s %v", data, err)
	}
	if data, _ := os.ReadFile(b.projects.statePath); strings.Contains(string(data), created.ID) {
		t.Fatal("ownership leaked to sibling")
	}
	a.Close()
	restarted := start(connections[0])
	reused, err := restarted.EnsureRuntime(ctx, e.repo)
	if err != nil || reused.Endpoint != left.Endpoint || reused.Launched {
		t.Fatalf("restart did not recover: %+v %v", reused, err)
	}
	reusedB, err := b.EnsureRuntime(ctx, e.repo)
	if err != nil || reusedB.Endpoint != right.Endpoint {
		t.Fatalf("restart altered sibling: %+v %v", reusedB, err)
	}
}

func TestNamedClaudeRejectsInvalidBinding(t *testing.T) {
	e, connections := namedRecoveryEnv(t)
	for _, mutate := range []func(*platformconnections.Connection){
		func(c *platformconnections.Connection) { c.ID = "../escape" },
		func(c *platformconnections.Connection) { c.Kind = "codex" },
		func(c *platformconnections.Connection) { c.Directory = "" },
		func(c *platformconnections.Connection) { c.Directory = t.TempDir() },
	} {
		c := connections[0]
		mutate(&c)
		if p, err := newConnectionAt(t.Context(), c, e.data, e.exe, e.state, nil, "", nil); err == nil {
			p.Close()
			t.Fatal("invalid binding accepted")
		}
	}
	if _, err := connectionDataDir(e.data, connections[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(e.data, "platform-connections", connections[0].ID, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := connectionDataDir(e.data, connections[0].ID); err == nil {
		t.Fatal("nonprivate state accepted")
	}
}
