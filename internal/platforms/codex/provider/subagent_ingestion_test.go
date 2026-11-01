package codexprovider

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sidebar"
	corestate "github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

// Only the ingestion/identity methods are supplied. Any attempt to resume a
// native thread, browse it as a fallback, or execute an agent fails the test.
type subagentHistory struct {
	platforms.Integration
	reader *HistoryReader
}

func TestCodexHistoryDistinguishesNativeChildFromFork(t *testing.T) {
	for _, tc := range []struct {
		name, parent string
		subagent     bool
	}{
		{"native child", "root", true}, {"ordinary fork", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHistory{threads: []*codexappserver.Thread{{ID: "thread", SessionID: "shared-tree", ForkedFromID: "fork-origin", ParentThreadID: tc.parent}}}
			snap, err := (&HistoryReader{Client: f}).Snapshot(t.Context(), "thread")
			if err != nil {
				t.Fatal(err)
			}
			if snap.Session.ID != "thread" || snap.Session.ParentID != tc.parent || snap.Subagent != tc.subagent {
				t.Fatalf("native identity lost: %+v", snap)
			}
		})
	}
}

func (*subagentHistory) ID() platforms.ID { return "codex" }
func (*subagentHistory) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "codex", SourceKey: "fixture-codex-home"}, nil
}
func (p *subagentHistory) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	return p.reader.Inventory(ctx)
}
func (p *subagentHistory) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	return p.reader.Snapshot(ctx, id)
}
func (p *subagentHistory) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	return p.reader.SessionIdentityMetadata(ctx, id)
}

func TestCodexSubagentCanonicalIngestionAndSidebar(t *testing.T) {
	h, _, notify := subagentHarness(t)
	notify("thr", "thr:subagent:child", "running")
	notify("thr:subagent:child", "thr:subagent:grandchild", "running")
	native, _, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	corePath := statetest.Path(t, sessioncatalog.Schema)
	core, err := corestate.Open(corePath, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	catalog, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	history := statev2test.Open(t)
	a, err := owned.New(t.Context(), &subagentHistory{reader: &HistoryReader{Client: native}}, catalog, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	instance := statev2.Instance{ID: "fixture", OwnerID: "owner", Platform: "codex", SourceKey: "fixture-codex-home"}
	if err := history.RegisterInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	entries, err := a.HistoryInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("native child inventory = %+v", entries)
	}
	ids := map[string]string{}
	for _, entry := range entries {
		snap, err := a.HistorySnapshot(t.Context(), entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		row, err := catalog.Get(t.Context(), entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids[row.NativeID] = row.ID
		batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: instance.ID, SessionID: row.ID}, snap)
		if err != nil {
			t.Fatal(err)
		}
		if row.NativeID != "thr" && (!batch.Session.Subagent || snap.SourceChild == nil || !*snap.SourceChild) {
			t.Fatalf("child flags lost: %+v", batch.Session)
		}
		if _, err := history.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	for child, parent := range map[string]string{"child": "thr", "grandchild": "child"} {
		row, err := catalog.Get(t.Context(), ids[child])
		if err != nil || row.ParentID != ids[parent] || row.NativeParentID != parent {
			t.Fatalf("catalog parent = %+v %v", row, err)
		}
		stored, err := history.ReadConversation(t.Context(), statev2.Key{InstanceID: instance.ID, SessionID: ids[child]}, 10, 0)
		if err != nil || stored.Session.ParentID != ids[parent] || !stored.Session.Subagent {
			t.Fatalf("stored child = %+v %v", stored, err)
		}
	}
	r, err := sidebar.Open(history.FilePath(), corePath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := history.BeginSidebarGeneration(t.Context(), instance, "generation"); err != nil {
		t.Fatal(err)
	}
	read := func(include bool) []db.Session {
		t.Helper()
		observations := map[string]statev2.SidebarObservation{}
		for _, row := range h.b.statuses() {
			observations[row.ID] = statev2.SidebarObservation{Status: string(row.Status)}
		}
		if err := history.ReplaceSidebarObservations(t.Context(), instance, "generation", time.Now().Add(time.Minute).UnixMilli(), observations); err != nil {
			t.Fatal(err)
		}
		q := platforms.SidebarQuery{}
		if include {
			q.IncludeIDs = []string{ids["child"], ids["grandchild"]}
		}
		rows, err := r.Sessions(t.Context(), sidebar.Scope{Owner: "owner", Platform: "codex", Source: instance.SourceKey, Generation: "generation"}, q)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	assert := func(id, parent string, want db.SessionStatus) {
		t.Helper()
		for _, row := range read(want != db.StatusBusy) {
			if row.ID == ids[id] {
				if row.ParentID != ids[parent] || row.Status != want {
					t.Fatalf("sidebar child = %+v", row)
				}
				return
			}
		}
		t.Fatalf("sidebar lost %s", id)
	}
	assert("child", "thr", db.StatusBusy)
	assert("grandchild", "child", db.StatusBusy)
	notify("thr", "thr:subagent:child", "idle")
	for _, row := range read(false) {
		if row.ID == ids["child"] {
			t.Fatal("completed native child remains visible")
		}
	}
	assert("child", "thr", db.StatusDone)
	assert("grandchild", "child", db.StatusBusy)
}
