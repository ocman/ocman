package opencode

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func accountConfig(t *testing.T, id string) (platformconnections.Connection, ConnectionConfig) {
	t.Helper()
	root := t.TempDir()
	native := ocruntime.NativeConnectionConfig{
		DBPath: filepath.Join(root, "native.db"), ConfigDir: filepath.Join(root, "config"),
		DataDir: filepath.Join(root, "data"), StateDir: filepath.Join(root, "state"),
		CacheDir: filepath.Join(root, "cache"), RuntimeDir: filepath.Join(root, "runtime"), Executable: "fake-opencode",
	}
	// A disposable native fixture, never an installed agent or user database.
	db, err := sql.Open("sqlite", native.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE fixture(id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return platformconnections.Connection{ID: id, Kind: "opencode", Name: id, Directory: native.ConfigDir, Configuration: "isolated", Enabled: true}, ConnectionConfig{Native: native, Auth: ocapi.New(id + "-password")}
}

func TestNamedConnectionsOwnAuthClientsAndCaches(t *testing.T) {
	legacyTransport := openCodeClient.Transport
	legacyLLMTransport := openCodeLLMClient.Transport
	legacyCache := catalogCache
	ca, cfgA := accountConfig(t, "alpha")
	cb, cfgB := accountConfig(t, "beta")
	a, err := NewConnection(ca, cfgA)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseConnection() })
	b, err := NewConnection(cb, cfgB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.CloseConnection() })
	aIdentity, err := a.HistoryIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bIdentity, err := b.HistoryIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sameFile := func(a, b string) bool {
		af, err := os.Stat(a)
		if err != nil {
			t.Fatal(err)
		}
		bf, err := os.Stat(b)
		if err != nil {
			t.Fatal(err)
		}
		return os.SameFile(af, bf)
	}
	if sameFile(aIdentity.SourceKey, bIdentity.SourceKey) || !sameFile(aIdentity.SourceKey, cfgA.Native.DBPath) || !sameFile(bIdentity.SourceKey, cfgB.Native.DBPath) || aIdentity.Platform != "opencode" || bIdentity.Platform != "opencode" {
		t.Fatal("named account rewrote native kind or shared physical database")
	}
	if openCodeClient.Transport != legacyTransport || openCodeLLMClient.Transport != legacyLLMTransport || catalogCache != legacyCache {
		t.Fatal("named constructor mutated process defaults")
	}
	if a.client() == b.client() || a.llmClient() == b.llmClient() || a.cache() == b.cache() || a.Discovery() == b.Discovery() {
		t.Fatal("shared account transport/cache/discovery")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, _ := r.BasicAuth()
		if r.URL.Path == "/api/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agent":
			fmt.Fprintf(w, `[{"name":%q,"mode":"primary"}]`, password)
		case "/provider":
			fmt.Fprintf(w, `{"all":[{"id":"test","models":{%q:{"id":%q}}}],"connected":["test"]}`, password, password)
		case "/session":
			fmt.Fprintf(w, `{"id":%q}`, password)
		default:
			fmt.Fprintf(w, `{"password":%q}`, password)
		}
	}))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	port := u.Port()
	var wg sync.WaitGroup
	for _, adapter := range []*Adapter{a, b} {
		wg.Go(func() {
			want := adapter.connection.metadata.ID + "-password"
			for range 10 {
				agents, models, err := adapter.ProjectCatalog(t.Context(), server.URL, "/same/project")
				if err != nil || len(agents) != 1 || agents[0] != want || len(models) != 1 || models[0] != "test/"+want {
					t.Errorf("cross-account catalog: %v %v %v", agents, models, err)
				}
				body, err := adapter.getJSON(t.Context(), port, "/check")
				if err != nil || !strings.Contains(string(body), want) {
					t.Errorf("wrong account auth: %s %v", body, err)
				}
			}
		})
	}
	wg.Wait()
	a.InvalidateCatalogsForPort(port)
	if _, found := b.cache().get(port, "/agent?directory=%2Fsame%2Fproject"); !found {
		t.Fatal("sibling cache invalidated")
	}
	// Resetting/reconfiguring the legacy path must never retarget account A.
	New(nil, nil, nil, ocapi.New("legacy-password"))
	t.Cleanup(func() { configureHTTPAuth(ocapi.New("")) })
	response, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Port: port, Directory: "/same/project"})
	if err != nil || response.ID != "alpha-password" {
		t.Fatalf("create account changed: %#v %v", response, err)
	}
	if err := a.CloseConnection(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.HistoryIdentity(t.Context()); err != nil {
		t.Fatal("closing sibling account closed this database")
	}
}

func TestNamedConnectionFailsClosedBeforeBootstrap(t *testing.T) {
	c, cfg := accountConfig(t, "alpha")
	cfg.Native.DBPath = filepath.Join(t.TempDir(), "not-yet-created.db")
	if a, err := NewConnection(c, cfg); err == nil {
		_ = a.CloseConnection()
		t.Fatal("missing database used fallback")
	}
	for _, mutate := range []func(*platformconnections.Connection, *ConnectionConfig){
		func(c *platformconnections.Connection, _ *ConnectionConfig) { c.Kind = "claudecode" },
		func(c *platformconnections.Connection, _ *ConnectionConfig) { c.Directory = "/another/account" },
		func(c *platformconnections.Connection, _ *ConnectionConfig) { c.Removed = true },
		func(_ *platformconnections.Connection, cfg *ConnectionConfig) { cfg.Auth = ocapi.New("") },
	} {
		c, cfg := accountConfig(t, "alpha")
		mutate(&c, &cfg)
		if a, err := NewConnection(c, cfg); err == nil {
			_ = a.CloseConnection()
			t.Fatal("invalid account accepted")
		}
	}
	if err := New(nil, nil, nil, ocapi.New("")).CloseConnection(); err != nil {
		t.Fatal(err)
	}
}
