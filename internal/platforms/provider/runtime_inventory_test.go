package provider

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type inventoryLiveness struct {
	alive bool
	err   error
	seen  *ocruntime.Instance
}

func (r *inventoryLiveness) Alive(_ context.Context, inst *ocruntime.Instance) (bool, error) {
	r.seen = inst
	return r.alive, r.err
}

func TestPersistedRuntimeInventoryRequiresStoppedProof(t *testing.T) {
	for _, tc := range []struct {
		name      string
		alive     bool
		aliveErr  error
		connected bool
		readError bool
		wantErr   bool
	}{
		{"readable", true, nil, true, false, false},
		{"unreadable live", true, nil, false, false, true},
		{"unreadable unknown", false, errors.New("tmux unavailable"), false, false, true},
		{"confirmed stopped", false, nil, false, false, false},
		{"failed inventory live", true, nil, true, true, true},
		{"failed inventory stopped", false, nil, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := statetest.Open(t)
			endpoint := "unix://" + filepath.Join(t.TempDir(), "missing")
			if tc.connected {
				f := newFake(t, allCapabilities())
				endpoint = "unix://" + f.socket
				f.reply("GET", "/sessions", []db.Session{{ID: "hidden", Directory: "/repo", Status: db.StatusBusy}})
				if tc.readError {
					f.mu.Lock()
					f.replies["GET /sessions"] = fakeReply{status: http.StatusServiceUnavailable}
					f.mu.Unlock()
				}
			}
			if err := store.UpsertManagedProvider(t.Context(), "fake", "/repo", state.ManagedInstance{Endpoint: endpoint, Kind: ocruntime.KindProviderTmux, RuntimeID: "tracked", PID: 42}); err != nil {
				t.Fatal(err)
			}
			runtime := &inventoryLiveness{alive: tc.alive, err: tc.aliveErr}
			rows, err := PersistedRuntimeSessions(t.Context(), "fake", store, runtime)
			if (err != nil) != tc.wantErr {
				t.Fatalf("inventory: %+v %v", rows, err)
			}
			if tc.connected && !tc.readError {
				if len(rows) != 1 || rows[0].ID != "hidden" || runtime.seen != nil {
					t.Fatalf("readable supervisor not used: %+v %+v", rows, runtime.seen)
				}
			} else if runtime.seen == nil || runtime.seen.ID != "tracked" || runtime.seen.PID != 42 || runtime.seen.RepoRoot != "/repo" {
				t.Fatalf("missing owner liveness proof: %+v", runtime.seen)
			}
		})
	}
}

func TestPersistedRuntimeInventoryRejectsInvalidDependencies(t *testing.T) {
	if rows, err := PersistedRuntimeSessions(t.Context(), "fake", nil, nil); err != nil || len(rows) != 0 {
		t.Fatalf("absent registry: %+v %v", rows, err)
	}
	store := statetest.Open(t)
	if _, err := PersistedRuntimeSessions(t.Context(), "fake", store, nil); err == nil {
		t.Fatal("missing liveness probe accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := PersistedRuntimeSessions(t.Context(), "fake", store, &inventoryLiveness{}); err == nil {
		t.Fatal("unreadable registry accepted")
	}
}
