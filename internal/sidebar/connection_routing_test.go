package sidebar

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestGroupedSidebarProjectsRoutingKeyWithoutChangingNativeFlags(t *testing.T) {
	r, core, history := sidebarInventory(t, 4)
	writer, err := sql.Open("sqlite", history.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "second", OwnerID: "owner", Platform: "test", SourceKey: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`UPDATE session_sidebar SET instance_id='second' WHERE directory='/one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().Exec(`UPDATE session_catalog SET source_key='other' WHERE directory='/one'`); err != nil {
		t.Fatal(err)
	}
	id := "ocm_00000001-0000-0000-0000-000000000001"
	if err := core.PinSession(t.Context(), "test", id); err != nil {
		t.Fatal(err)
	}
	if err := core.MarkSessionSeen(t.Context(), "test", id, 50); err != nil {
		t.Fatal(err)
	}
	if err := core.ArchiveSession(t.Context(), "test", id, 50); err != nil {
		t.Fatal(err)
	}
	var scopes []Scope
	if err := json.Unmarshal([]byte(`[{"Owner":"owner","Platform":"test","Source":"source","Scoped":true,"RoutingKey":"cnx_work"},{"Owner":"owner","Platform":"test","Source":"other","Scoped":true,"RoutingKey":"cnx_personal"}]`), &scopes); err != nil {
		t.Fatal(err)
	}
	rows, err := r.SessionsFor(t.Context(), scopes, platforms.SidebarQuery{Limit: 500})
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	for _, row := range rows {
		want := "cnx_work"
		if row.Directory == "/one" {
			want = "cnx_personal"
		}
		if row.Platform != want {
			t.Fatalf("physical source was not mapped in grouped SQL: %+v", row)
		}
		if row.ID == id && (!row.Pinned || !row.Seen || !row.Archived) {
			t.Fatalf("routing projection changed native state joins: %+v", row)
		}
	}
	if _, err := writer.Exec(`UPDATE session_sidebar SET is_subagent=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	instance := statev2.Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if err := history.BeginSidebarGeneration(t.Context(), instance, "g"); err != nil {
		t.Fatal(err)
	}
	if err := history.ReplaceSidebarObservations(t.Context(), instance, "g", time.Now().Add(time.Hour).UnixMilli(), map[string]statev2.SidebarObservation{"native-1": {Status: "busy"}}); err != nil {
		t.Fatal(err)
	}
	scopes[0].Generation = "g"
	if n, err := r.RunningCount(t.Context(), scopes, platforms.SidebarQuery{}); err != nil || n != 1 {
		t.Fatalf("native pin not counted under connection routing: %d %v", n, err)
	}
	if err := core.UnpinSession(t.Context(), "test", id); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RunningCount(t.Context(), scopes, platforms.SidebarQuery{}); err != nil || n != 0 {
		t.Fatalf("native unpin not reflected in count: %d %v", n, err)
	}
}

func TestGroupedSidebarRejectsAmbiguousRoutingClaims(t *testing.T) {
	r, _, _ := sidebarInventory(t, 2)
	var scopes []Scope
	if err := json.Unmarshal([]byte(`[{"Owner":"owner","Platform":"test","Source":"source","Scoped":true,"RoutingKey":"cnx_work"},{"Owner":"owner","Platform":"test","Source":"source","Scoped":true,"RoutingKey":"cnx_personal"}]`), &scopes); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SessionsFor(t.Context(), scopes, platforms.SidebarQuery{Limit: 500}); err == nil {
		t.Fatal("ambiguous source was assigned to the first connection")
	}
	if _, err := r.RunningCount(t.Context(), scopes, platforms.SidebarQuery{}); err == nil {
		t.Fatal("ambiguous routing sources accepted by running count")
	}
}
