package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

type namedWiringRuntime struct {
	endpoint string
	launches int
}

func TestNamedWorktreeNamingDoesNotFallBackAfterOwnRuntimeFailure(t *testing.T) {
	want := errors.New("selected account runtime unavailable")
	host := local.New(local.Deps{OpenCodeRoot: t.TempDir(), Runtime: &factoryRuntime{fail: want}})
	defer host.Finalize()
	// A nil adapter would panic if naming fell through to any HTTP/model path.
	connection := &openCodeConnection{host: host}
	if _, err := connection.WorktreeName(t.Context(), t.TempDir(), "task"); !errors.Is(err, want) {
		t.Fatal("naming ignored selected account launch failure", err)
	}
}

func (r *namedWiringRuntime) Launch(_ context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches++
	return &ocruntime.Instance{Endpoint: r.endpoint, RepoRoot: spec.RepoRoot, Kind: ocruntime.KindNativeTmux, ID: "owned"}, nil
}
func (*namedWiringRuntime) Probe(context.Context, *ocruntime.Instance) error { return nil }
func (*namedWiringRuntime) Stop(context.Context, *ocruntime.Instance) error  { return nil }

func TestNamedOpenCodeRuntimeAndNamerSurviveCanonicalServerWiring(t *testing.T) {
	core := statetest.Open(t, server.ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	var native []platforms.Integration
	var keys []platforms.ID
	var runtimes []*namedWiringRuntime
	sources := map[platforms.ID]platforms.HistoryIdentity{}
	for _, name := range []string{"work", "personal"} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: name, Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		var expectedAuth ocapi.Auth
		// This is a fake native API. Helper turns never reach a real model.
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			values := map[string]string{}
			expectedAuth.AddServerEnv(values)
			user, password, ok := r.BasicAuth()
			if !ok || user != ocapi.DefaultUsername || password != values["OPENCODE_PASSWORD"] {
				t.Error("naming crossed account credential boundary")
				http.Error(w, "wrong account", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.URL.Path == "/api/info":
				http.NotFound(w, r)
			case r.Method == http.MethodGet && r.URL.Path == "/config":
				_, _ = w.Write([]byte(`{"small_model":"fake/model"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/session":
				_, _ = w.Write([]byte(`{"id":"helper"}`))
			case r.Method == http.MethodPost && r.URL.Path == "/session/helper/message":
				_ = json.NewEncoder(w).Encode(map[string]any{"parts": []map[string]string{{"type": "text", "text": name + "-scoped-branch"}}})
			case r.URL.Path == "/session/helper/abort" || r.Method == http.MethodDelete:
				_, _ = w.Write([]byte(`{}`))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(api.Close)
		runtime := &namedWiringRuntime{endpoint: api.URL}
		runtimes = append(runtimes, runtime)
		factory := connectionFactory{state: core, dataDir: t.TempDir(), runtimeBuilder: func(ocruntime.NativeConnectionConfig, ocapi.Auth) (ocruntime.Runtime, error) { return runtime, nil }, adapterBuilder: func(actual platformconnections.Connection, cfg opencode.ConnectionConfig) (*opencode.Adapter, error) {
			expectedAuth = cfg.Auth
			if err := os.MkdirAll(filepath.Dir(cfg.Native.DBPath), 0700); err != nil {
				return nil, err
			}
			writable, err := db.OpenReadWrite(cfg.Native.DBPath)
			if err != nil {
				return nil, err
			}
			if err := writable.Close(); err != nil {
				return nil, err
			}
			return opencode.NewConnection(actual, cfg)
		}}
		p, err := factory.build(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.(interface{ Close() }).Close)
		native = append(native, p)
		keys = append(keys, platforms.ID(c.ID))
		source, err := p.(platforms.HistoryReader).HistoryIdentity(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		sources[platforms.ID(c.ID)] = source
	}
	registry := platforms.NewRegistry()
	if _, err := composeConnectionSessionCore(t.Context(), core, history, registry, native, "owner", keys, store, platformconnections.NewRouter(store)); err != nil {
		t.Fatal(err)
	}
	srv := server.New(nil, core, "127.0.0.1:0", registry, nil)
	owner := srv.HostRouter().Local()
	for i, key := range keys {
		result, err := owner.EnsureRuntime(t.Context(), string(key), t.TempDir())
		if err != nil || result == nil || result.Endpoint != runtimes[i].endpoint || runtimes[i].launches != 1 {
			t.Fatalf("canonical server lost named runtime %s: %+v %v", key, result, err)
		}
		canonical, _ := registry.Get(key)
		source, err := canonical.(platforms.HistoryReader).HistoryIdentity(t.Context())
		if err != nil || source != sources[key] {
			t.Fatal("canonical routing rewrote physical source", err)
		}
		services := canonical.(hostsvc.PlatformServiceProvider).HostServices()
		if services.Namer == nil {
			t.Fatal("canonical server lost account-scoped worktree naming")
		}
		name, err := services.Namer.WorktreeName(t.Context(), t.TempDir(), "fake task")
		if err != nil || name != []string{"work-scoped-branch", "personal-scoped-branch"}[i] {
			t.Fatalf("wrong account naming helper: %q %v", name, err)
		}
	}
}
