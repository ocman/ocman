package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type maintenanceNative struct {
	*nativePlatform
	err error
}

func (p *maintenanceNative) WorktreeRuntimeSessions(context.Context) ([]db.Session, error) {
	return p.rows, p.err
}

func (p *maintenanceNative) WorktreeRuntimeLifecycle(context.Context, string) (db.SessionLifecycle, error) {
	return db.SessionLifecycle{Directory: "/repo", Status: db.StatusDone}, p.err
}

func TestMixedCanonicalRuntimeInventoryIncludesCleanupOnlyIntegrations(t *testing.T) {
	catalog, err := sessioncatalog.New(statetest.Open(t, sessioncatalog.Schema))
	if err != nil {
		t.Fatal(err)
	}
	history := statev2test.Open(t)
	var adapters []*Adapter
	for _, id := range []platforms.ID{"opencode", "claudecode", "codex"} {
		base := &nativePlatform{id: id, source: string(id), rows: []db.Session{{ID: "root", Directory: "/repo"}, {ID: "child", ParentID: "root", Directory: "/repo"}}}
		var native platforms.Integration = &maintenanceNative{nativePlatform: base}
		if id == "opencode" {
			native = &runtimeRecoveryNative{nativePlatform: base, identities: base.rows}
		}
		a, err := New(t.Context(), native, catalog, history, "owner")
		if err != nil {
			t.Fatal(err)
		}
		adapters = append(adapters, a)
	}
	for _, a := range adapters {
		rows, err := a.GetSessionIdentities(t.Context())
		if err != nil || len(rows) != 2 || rows[1].ParentID != rows[0].ID || !sessioncatalog.IsID(rows[1].ID) {
			t.Fatalf("%s runtime inventory: %+v %v", a.ID(), rows, err)
		}
		if _, err := a.GetSessionLifecycle(t.Context(), rows[1].ID); err != nil {
			t.Fatalf("%s runtime lifecycle: %v", a.ID(), err)
		}
		if native, ok := a.native.(*maintenanceNative); ok {
			native.err = errors.New("native owner unavailable")
			if _, err := a.GetSessionIdentities(t.Context()); !errors.Is(err, native.err) {
				t.Fatalf("%s accepted incomplete inventory: %v", a.ID(), err)
			}
			if _, err := a.GetSessionLifecycle(t.Context(), rows[1].ID); !errors.Is(err, native.err) {
				t.Fatalf("%s accepted unavailable lifecycle: %v", a.ID(), err)
			}
		}
	}
}
