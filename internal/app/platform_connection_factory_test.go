package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type factoryRuntime struct {
	launches int
	fail     error
}

func TestBuildSavedCollectionKeepsDisabledIntentAndUnavailableEntries(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "claudecode", Name: "Machine", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}, DefaultKind: "claudecode"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Disabled", Configuration: "isolated", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	previous := knownPlatforms["claudecode"]
	t.Cleanup(func() { knownPlatforms["claudecode"] = previous })
	knownPlatforms["claudecode"] = func(context.Context, *db.DB, *state.DB, ocapi.Auth) platforms.Integration {
		return &platformtest.Fake{PlatformID: "claudecode"}
	}
	f := connectionFactory{state: core, dataDir: t.TempDir()}
	adapters, keys, err := f.buildCollection(t.Context(), store)
	if err != nil || len(adapters) != 1 || len(keys) != 1 {
		t.Fatalf("saved collection: %v %v %v", adapters, keys, err)
	}
	knownPlatforms["claudecode"] = func(context.Context, *db.DB, *state.DB, ocapi.Auth) platforms.Integration { return nil }
	adapters, keys, err = f.buildCollection(t.Context(), store)
	if err != nil || len(adapters) != 0 || len(keys) != 0 {
		t.Fatalf("unavailable entries published: %v %v %v", adapters, keys, err)
	}
	collection, err := store.Read(t.Context())
	if err != nil || len(collection.Connections) != 2 || collection.DefaultID == "" {
		t.Fatalf("startup rewrote saved intent: %+v %v", collection, err)
	}
}

func TestSavedRuntimeVersionBlockDoesNotRewriteIntent(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	previous := installedOpenCodeMajor
	t.Cleanup(func() { installedOpenCodeMajor = previous })
	installedOpenCodeMajor = func() (int, bool) { t.Fatal("fresh onboarding probed native CLI"); return 0, false }
	if blocked, issues, err := blockedConnectionKinds(t.Context(), store); err != nil || len(blocked) != 0 || len(issues) != 0 {
		t.Fatal(blocked, issues, err)
	}
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	installedOpenCodeMajor = func() (int, bool) { return 1, true }
	blocked, issues, err := blockedConnectionKinds(t.Context(), store)
	if err != nil || !blocked["opencode"] || len(issues) != 1 {
		t.Fatal(blocked, issues, err)
	}
	if current, err := store.Get(t.Context(), c.ID); err != nil || !current.Enabled {
		t.Fatal("runtime issue disabled saved intent", err)
	}
	f := connectionFactory{state: core, blockedKinds: blocked}
	if adapters, _, err := f.buildCollection(t.Context(), store); err != nil || len(adapters) != 0 {
		t.Fatal("unsupported runtime published", err)
	}
	installedOpenCodeMajor = func() (int, bool) { return 2, true }
	if blocked, issues, err := blockedConnectionKinds(t.Context(), store); err != nil || len(blocked) != 0 || len(issues) != 0 {
		t.Fatal(blocked, issues, err)
	}
	if enabled, err := selectedPlatforms(" claudecode, ,codex ", "known"); err != nil || !enabled["claudecode"] || !enabled["codex"] {
		t.Fatal(enabled, err)
	}
	if _, err := selectedPlatforms("unknown", "known"); err == nil {
		t.Fatal("unknown selection accepted")
	}
}

func (r *factoryRuntime) Launch(_ context.Context, s ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches++
	if r.fail != nil {
		return nil, r.fail
	}
	return &ocruntime.Instance{Endpoint: fmt.Sprintf("http://127.0.0.1:%d", s.Port), RepoRoot: s.RepoRoot, Kind: ocruntime.KindNativeTmux, ID: "private"}, nil
}
func (*factoryRuntime) Probe(context.Context, *ocruntime.Instance) error { return nil }
func (*factoryRuntime) Stop(context.Context, *ocruntime.Instance) error  { return nil }

func TestConnectionFactoryUsesScopedRuntimeAndExplicitPaths(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: "account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &factoryRuntime{}
	f := connectionFactory{state: core, dataDir: t.TempDir(), runtimeBuilder: func(cfg ocruntime.NativeConnectionConfig, auth ocapi.Auth) (ocruntime.Runtime, error) {
		if !auth.Enabled() || cfg.ConfigDir != c.Directory || !filepath.IsAbs(cfg.DBPath) {
			t.Fatal("implicit connection paths/auth")
		}
		return runtime, nil
	}, adapterBuilder: func(actual platformconnections.Connection, cfg opencode.ConnectionConfig) (*opencode.Adapter, error) {
		if actual.ID != c.ID || cfg.Native.ConfigDir != c.Directory {
			t.Fatal("adapter bound sibling")
		}
		return opencode.New(nil, core, nil, cfg.Auth), nil
	}}
	p, err := f.build(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer p.(interface{ Close() }).Close()
	if runtime.launches != 1 {
		t.Fatal(runtime.launches)
	}
	if p.ID() != "opencode" {
		t.Fatal("native kind rewritten")
	}
	result, err := p.(*openCodeConnection).HostServices().Runtime.EnsureRuntime(t.Context(), t.TempDir())
	if err != nil || result == nil || runtime.launches != 1 {
		t.Fatalf("runtime not shared by connection: %+v %v", result, err)
	}
	scoped, err := platformconnections.NewRuntimeStore(t.Context(), core, c)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := scoped.ListManagedProviders(t.Context(), "opencode")
	if err != nil || len(roots) != 1 {
		t.Fatalf("runtime not durable: %v %v", roots, err)
	}
	f.adapterBuilder = func(platformconnections.Connection, opencode.ConnectionConfig) (*opencode.Adapter, error) {
		return nil, errors.New("read-only adapter failed")
	}
	if _, err := f.build(t.Context(), c); err == nil {
		t.Fatal("ignored adapter construction failure")
	}
	runtime.fail = errors.New("launch failed")
	other, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: "other", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c = other
	if _, err := f.build(t.Context(), c); err == nil {
		t.Fatal("ignored launch failure")
	}
	if _, err := f.build(t.Context(), platformconnections.Connection{Kind: "unknown"}); !errors.Is(err, platformconnections.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestConnectionFactoryRejectsInvalidRuntimeAndUnboundAccount(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	f := connectionFactory{state: core, dataDir: t.TempDir(), runtimeBuilder: func(ocruntime.NativeConnectionConfig, ocapi.Auth) (ocruntime.Runtime, error) {
		return nil, errors.New("invalid runtime")
	}}
	c := platformconnections.Connection{ID: "cnx_missing", Kind: "opencode", Directory: t.TempDir()}
	if _, err := f.build(t.Context(), c); err == nil {
		t.Fatal("invalid runtime accepted")
	}
	f.runtimeBuilder = func(ocruntime.NativeConnectionConfig, ocapi.Auth) (ocruntime.Runtime, error) {
		return &factoryRuntime{}, nil
	}
	if _, err := f.build(t.Context(), c); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := f.build(t.Context(), platformconnections.Connection{Kind: "claudecode", Directory: "relative"}); err == nil {
		t.Fatal("invalid named Claude accepted")
	}
	if _, err := f.build(t.Context(), platformconnections.Connection{Kind: "codex", Directory: "relative"}); err == nil {
		t.Fatal("invalid named Codex accepted")
	}
}
