package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDisabledMigratedOpenCodeInspectionNeverBootstrapsWriter(t *testing.T) {
	for _, mode := range []string{"healthy", "missing-service", "rejected", "busy", "custom-db"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			core := statetest.Open(t, platformconnections.Schema)
			store := platformconnections.New(core)
			if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "opencode", Name: "Machine", Configuration: "existing", Directory: t.TempDir()}}}); err != nil {
				t.Fatal(err)
			}
			c, err := store.Legacy(t.Context(), "opencode")
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("inspection mutated service: %s", r.Method)
				}
				u, p, ok := r.BasicAuth()
				if !ok || u != "opencode" || p != "inspection-only-password" {
					t.Error("inspection did not authenticate selected service")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if mode == "rejected" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if mode == "busy" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if r.URL.Path == "/api/info" {
					_, _ = w.Write([]byte(`{"version":"2.1.0"}`))
					return
				}
				if r.URL.Path == "/api/config" || r.URL.Path == "/config" {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				t.Errorf("unexpected native service read %s", r.URL)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			write := func(path string, value any) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(value)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(ocv2.ServiceConfigPath(), map[string]any{"password": "inspection-only-password"})
			if mode != "missing-service" {
				write(ocv2.ServiceStatePath(), map[string]any{"url": srv.URL, "pid": os.Getpid(), "version": "2.1.0"})
			}
			dbPath := db.DefaultDBPath()
			if mode == "custom-db" {
				dbPath = filepath.Join(t.TempDir(), "custom.db")
			}
			factory := connectionFactory{state: core, dataDir: t.TempDir(), openCodeDB: dbPath}
			check, err := connectionLifecycle(factory, platformconnections.NewRouter(store), nil, nil).Inspect(t.Context(), c)
			if mode == "healthy" {
				if err != nil || check.Status != "ready" || calls.Load() == 0 {
					t.Fatalf("healthy registered service rejected: %+v %v calls=%d", check, err, calls.Load())
				}
			} else if check.Status == "ready" {
				t.Fatalf("unverified service ready: %+v %v", check, err)
			}
			if (mode == "missing-service" || mode == "custom-db") && calls.Load() != 0 {
				t.Fatal("selected nonmatching service")
			}
			cfg := factory.openCodeConfig(c)
			if _, err := os.Stat(cfg.RuntimeDir); !os.IsNotExist(err) {
				t.Fatalf("read-only legacy inspection created private writer root: %v", err)
			}
		})
	}
}
