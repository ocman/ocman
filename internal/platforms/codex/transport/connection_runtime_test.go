package codextransport

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/mcpauth"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/codex/acptest"
	"github.com/ocman/ocman/internal/testutil"
)

func TestNamedCodexAccountsExecuteAndRecoverSeparately(t *testing.T) {
	r := newRig(t)
	// The state root deliberately lives in a repository. Account runtime keys
	// must not collapse to the checkout as ordinary project supervisors do.
	if out, err := gitexec.Command(t.Context(), "init", "--quiet", r.data).CombinedOutput(); err != nil {
		t.Fatalf("temporary repository: %s %v", out, err)
	}
	t.Setenv("FAKE_CODEX_CONNECTION_ACCOUNT", "1")
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_FEDERATION_RULE_ID", "OPENAI_BASE_URL"} {
		t.Setenv(key, "test-owner-override")
	}
	before := os.Environ()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var accounts []*Platform
	var configs []Config
	var endpoints []string
	for range 2 {
		home := filepath.Join(t.TempDir(), "named account")
		if err := os.Mkdir(home, 0o700); err != nil {
			t.Fatal(err)
		}
		cfg := Config{DataDir: r.data, State: r.store, Version: "test", Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: home}}
		p, err := newPlatform(ctx, cfg, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		configs = append(configs, cfg)
		accounts = append(accounts, p)
		if p.root == r.data || !p.mgr.CanonicalRoots || !strings.Contains(p.root, cfg.Connection.ID) {
			t.Fatalf("account root collapsed into the project: %q", p.root)
		}
		if _, err := p.HistoryInventory(ctx); err != nil {
			t.Fatalf("named history reader failed account isolation: %v", err)
		}
		res, err := p.EnsureRuntime(ctx, "/repo")
		if err != nil {
			t.Fatal(err)
		}
		endpoints = append(endpoints, res.Endpoint)
		created, err := p.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/repo"})
		if err != nil || created.ID == "" {
			t.Fatalf("named ACP execution failed account isolation: %+v %v", created, err)
		}
		row, exists, err := r.store.GetManagedProvider(ctx, "codex", p.root)
		if err != nil || !exists || row.Endpoint != res.Endpoint {
			t.Fatalf("account-scoped runtime not persisted: %+v %v %v", row, exists, err)
		}
	}
	if endpoints[0] == endpoints[1] || accounts[0].root == accounts[1].root {
		t.Fatal("same-kind connections shared execution")
	}
	if _, exists, err := r.store.GetManagedProvider(ctx, "codex", r.data); err != nil || exists {
		t.Fatalf("named runtime polluted the surrounding project key: exists=%v %v", exists, err)
	}
	for _, p := range accounts {
		state := acptest.ReadState(filepath.Join(p.home, "fixture-account-state.json"))
		if state.Home != p.home || state.CLI != r.exe || state.Directory != "/repo" {
			t.Fatalf("ACP did not use the pinned account configuration: %+v", state)
		}
	}
	idle := &idles{ch: make(chan string, 4)}
	accounts[0].SetSessionEventSink(idle.sink())
	go func() { _ = accounts[0].WatchEvents(ctx) }()
	if err := accounts[0].SendMessage(ctx, platforms.SendMessageRequest{SessionID: "thr", Message: "only the first account"}); err != nil {
		t.Fatal(err)
	}
	idle.wait(t)
	firstState := acptest.ReadState(filepath.Join(accounts[0].home, "fixture-account-state.json"))
	otherState := acptest.ReadState(filepath.Join(accounts[1].home, "fixture-account-state.json"))
	if len(firstState.Turns) != 1 || len(otherState.Turns) != 0 {
		t.Fatalf("a turn crossed account stores: first=%d other=%d", len(firstState.Turns), len(otherState.Turns))
	}
	accounts[0].Close()
	restarted, err := newPlatform(ctx, configs[0], launcher{exe: r.exe, cli: r.exe, acp: r.exe})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	testutil.WaitFor(t, 10*time.Second, "named account reattached after restart", func() bool { return restarted.current() != nil })
	res, err := restarted.EnsureRuntime(ctx, "/repo")
	if err != nil || res.Endpoint != endpoints[0] {
		t.Fatalf("restart attached another account: %+v %v", res, err)
	}
	other, err := accounts[1].EnsureRuntime(ctx, "/repo")
	if err != nil || other.Endpoint != endpoints[1] {
		t.Fatalf("restart replaced the sibling account: %+v %v", other, err)
	}
	if got := acptest.ReadState(filepath.Join(accounts[1].home, "fixture-account-state.json")); got.Revision != otherState.Revision {
		t.Fatal("recovering the first account mutated the sibling account")
	}
	launches, err := os.ReadFile(os.Getenv("FAKE_CODEX_LAUNCHES"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range accounts {
		if !strings.Contains(string(launches), p.home+" [") {
			t.Fatalf("native readers did not receive the pinned account home: %s", launches)
		}
	}
	if !reflect.DeepEqual(os.Environ(), before) {
		t.Fatal("constructing or executing connections changed the process-global environment")
	}
}

func TestNamedCodexPreservesOwnerMCPAndShortSockets(t *testing.T) {
	r := newRig(t)
	cfg := Config{DataDir: r.data, Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: t.TempDir()}}
	p, err := newPlatform(t.Context(), cfg, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.SetMCPAddr("127.0.0.1:9")
	a, err := parseSupervisorArgs(append([]string{"--socket", "/s", "--spool", "/sp", "--repo", p.root}, p.mgr.Runtime.Args()...))
	if err != nil || !a.ConnectionAccount || a.Home != p.home || a.MCPTokenFile != mcpauth.TokenPath(r.data) {
		t.Fatalf("named launch lost owner MCP or account identity: %+v %v", a, err)
	}
	if p.mgr.Runtime.Directory != filepath.Join(r.data, "p") {
		t.Fatal("opaque connection IDs were expanded into the short socket parent")
	}
	if !strings.HasSuffix(p.mgr.Runtime.Identity(), "\x00connection-account/v1") {
		t.Fatal("unfiltered supervisors could be recovered as a named account")
	}
	originalHome := p.home
	cfg.Connection.Home = t.TempDir()
	cfg.Connection.ID = "changed"
	if p.home != originalHome || !strings.Contains(p.root, "cnx_") || a.Home != originalHome {
		t.Fatal("caller mutation retargeted the active account")
	}
}

func TestNamedCodexRefusesRuntimeRootRetargeting(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var accounts []*Platform
	var endpoints []string
	for range 2 {
		cfg := Config{DataDir: r.data, State: r.store, Connection: &ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: t.TempDir()}}
		p, err := newPlatform(ctx, cfg, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		res, err := p.EnsureRuntime(ctx, "/repo")
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, p)
		endpoints = append(endpoints, res.Endpoint)
	}
	first, sibling := accounts[0], accounts[1]
	backup := first.root + "-moved"
	if err := os.Rename(first.root, backup); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(first.root); _ = os.Rename(backup, first.root) }()
	if err := os.Symlink(sibling.root, first.root); err != nil {
		t.Fatal(err)
	}
	if _, err := first.EnsureRuntime(ctx, "/repo"); err == nil {
		t.Fatal("a changed account-root alias could drain and replace the sibling supervisor")
	}
	if _, _, err := first.mgr.Recover(ctx, first.root); err == nil {
		t.Fatal("recovery accepted a retargeted account runtime root")
	}
	for i, p := range accounts {
		row, exists, err := r.store.GetManagedProvider(ctx, "codex", p.root)
		if err != nil || !exists || row.Endpoint != endpoints[i] {
			t.Fatalf("root refusal changed a runtime record: %+v %v %v", row, exists, err)
		}
	}
}
