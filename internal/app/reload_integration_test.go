package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platformsettings"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type reloadPlatform struct {
	startupPlatform
	id   platforms.ID
	busy *atomic.Bool
}

func (p reloadPlatform) ID() platforms.ID { return p.id }
func (p reloadPlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	status := db.StatusDone
	if p.busy.Load() {
		status = db.StatusBusy
	}
	return []db.Session{{ID: "active", Status: status}}, nil
}

func TestPlatformReloadInProcess(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	path := statetest.Path(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + listener.Addr().String()
	ctx, cancel := context.WithCancel(t.Context())
	var busy atomic.Bool
	busy.Store(true)
	var starts atomic.Int32
	done := make(chan error, 1)
	pid := os.Getpid()
	go func() {
		done <- reloadBackend(ctx, listener, func(ctx context.Context, ln net.Listener, reload func()) error {
			store, err := state.Open(path, server.ModuleSchemas()...)
			if err != nil {
				return err
			}
			defer store.Close()
			enabled, err := platformsettings.Load(ctx, store)
			if err != nil {
				return err
			}
			registry := platforms.NewRegistry()
			for _, id := range enabled {
				registry.Register(reloadPlatform{id: platforms.ID(id), busy: &busy})
			}
			starts.Add(1)
			return server.New(nil, store, ln.Addr().String(), registry, nil).
				WithMCPAddr("").WithConfigReload(reload, strings.Join(enabled, ","), false).StartOnListener(ctx, ln)
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("reload backend did not stop")
		}
	})
	client := &http.Client{Timeout: time.Second}
	waitCatalog := func(count int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(base + "/api/capabilities")
			if err == nil {
				var catalog struct {
					Platforms []json.RawMessage `json:"platforms"`
				}
				err = json.NewDecoder(resp.Body).Decode(&catalog)
				_ = resp.Body.Close()
				if err == nil && len(catalog.Platforms) == count {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("catalog did not reach %d platforms", count)
	}
	save := func(body string) {
		t.Helper()
		resp, err := client.Post(base+"/api/settings/platforms", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("save: %d", resp.StatusCode)
		}
	}
	waitCatalog(2)
	save(`{"enabled":["claudecode"]}`)
	time.Sleep(1200 * time.Millisecond)
	if starts.Load() != 1 {
		t.Fatal("active turn was detached")
	}
	busy.Store(false)
	waitCatalog(1)
	save(`{"enabled":["opencode","claudecode"]}`)
	waitCatalog(2)
	if starts.Load() != 3 || os.Getpid() != pid {
		t.Fatalf("reload restarted process or missed composition: %d", starts.Load())
	}
}
