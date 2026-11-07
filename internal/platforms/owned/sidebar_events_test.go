package owned

import (
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

func TestSidebarPromptEventsNotifyNativeAncestorsAfterCheckpoint(t *testing.T) {
	a, p, root := storedFixture(t)
	publish(t, a, db.Session{ID: "parent", ParentID: "native", Directory: "/stored", Title: "parent"})
	publish(t, a, db.Session{ID: "child", ParentID: "parent", Directory: "/stored", Title: "pinned child"})
	parentRow, err := a.canonical(t.Context(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	childRow, err := a.canonical(t.Context(), "child")
	if err != nil {
		t.Fatal(err)
	}
	parent, child := parentRow.ID, childRow.ID
	path, err := a.store.FilePath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	core, err := state.Open(path, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if err := core.PinSession(t.Context(), string(a.ID()), child); err != nil {
		t.Fatal(err)
	}
	if err := core.PinSession(t.Context(), string(a.ID()), parent); err != nil {
		t.Fatal(err)
	}
	native := &sidebarNative{nativePlatform: p, t: t, observations: map[string]platforms.LiveState{
		"native": {Status: "busy"}, "parent": {Status: "busy"}, "child": {Status: "busy"},
	}}
	a.native = native
	worker := &sidebarWorker{adapter: a}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer worker.Close(t.Context())
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"question", "clear", "permission", "remove", "question", "disconnect", "reconnect"} {
		t.Run(phase, func(t *testing.T) {
			switch phase {
			case "question":
				native.observations["child"] = platforms.LiveState{Status: "busy", PendingQuestion: true}
			case "clear":
				native.observations["child"] = platforms.LiveState{Status: "busy"}
			case "permission":
				native.observations["child"] = platforms.LiveState{Status: "busy", PendingPermission: true}
			case "remove":
				delete(native.observations, "child")
			case "disconnect":
				a.native = drainingSidebarNative{native}
			case "reconnect":
				a.native = native
			}
			rows := map[string]db.Session{}
			a.SetSessionEventSink(platforms.SessionEventSink{OnSidebarChanged: func(platform, id string) {
				if platform != string(a.ID()) {
					t.Errorf("wrong owner: %s", platform)
				}
				row, err := a.SidebarSession(t.Context(), id)
				if err != nil {
					t.Errorf("postcommit compact read: %v", err)
					return
				}
				rows[id] = *row
			}})
			if err := worker.snapshot(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{root, parent, child} {
				row, ok := rows[id]
				if !ok {
					t.Errorf("missing affected row %s; got %v", id, rows)
					continue
				}
				live := phase != "disconnect"
				question := live && (phase == "question" || phase == "reconnect")
				permission := phase == "permission"
				if row.PendingQuestion != question || row.PendingPermission != permission || row.LiveConnection != live {
					t.Errorf("notification preceded checkpoint or lost prompt: %+v", row)
				}
				if live && (question || permission) && row.Status != db.StatusWaiting {
					t.Errorf("prompt did not settle ancestor: %+v", row)
				}
			}
			if err := worker.snapshot(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSidebarPromptEventsDoNotFollowApplicationOrForeignSourceParents(t *testing.T) {
	a, _, root := storedFixture(t)
	linked, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: a.owner, Platform: a.source.Platform,
		Directory: "/stored", ParentID: root, Origin: sessioncatalog.User, RequestKey: "application-child", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Bind(t.Context(), linked.ID, a.binding("linked")); err != nil {
		t.Fatal(err)
	}
	publish(t, a, db.Session{ID: "linked", Directory: "/stored"})
	other, err := New(t.Context(), &nativePlatform{id: "test", source: "other-source"}, a.store, a.history, a.owner)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, other, db.Session{ID: "foreign-parent", Directory: "/stored"})
	foreign, err := other.canonical(t.Context(), "foreign-parent")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.importRows(t.Context(), []db.Session{{ID: "child", ParentID: "foreign-parent", Directory: "/stored"}}); err != nil {
		t.Fatal(err)
	}
	child, err := a.canonical(t.Context(), "child")
	if err != nil {
		t.Fatal(err)
	}
	notified := map[string]int{}
	a.SetSessionEventSink(platforms.SessionEventSink{OnSidebarChanged: func(_ string, id string) { notified[id]++ }})
	worker := &sidebarWorker{adapter: a}
	observations := map[string]statev2.SidebarObservation{"linked": {PendingQuestion: true}, "child": {PendingPermission: true}}
	worker.publishSidebarChanges(t.Context(), observations, true)
	// Reconciliation creates an owner-local placeholder for the missing native
	// parent. It may be notified, but never the other source's matching native ID.
	if len(notified) != 3 || notified[linked.ID] != 1 || notified[child.ID] != 1 || notified[child.ParentID] != 1 || notified[foreign.ID] != 0 || notified[root] != 0 {
		t.Fatalf("crossed non-native/source boundary: %v", notified)
	}
	clear(notified)
	worker.publishSidebarChanges(t.Context(), observations, true)
	if len(notified) != 0 {
		t.Fatalf("unchanged prompts notified again: %v", notified)
	}
}
