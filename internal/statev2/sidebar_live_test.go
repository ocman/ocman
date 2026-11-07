package statev2

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestSidebarLiveCommitFencesAndCopies(t *testing.T) {
	d := openTestDB(t)
	scope := Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if got := d.SidebarLiveSnapshots(); got != nil {
		t.Fatal(got)
	}
	if err := d.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
		t.Fatal(err)
	}
	rows := map[string]SidebarObservation{"native": {Status: "busy", BackgroundTasks: 1}}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "g", 123, rows); err != nil {
		t.Fatal(err)
	}
	rows["native"] = SidebarObservation{Status: "done"}
	snapshot := d.SidebarLiveSnapshots()
	if snapshot[0].ExpiresAt != 123 || snapshot[0].Observations["native"].Status != "busy" {
		t.Fatal(snapshot)
	}
	snapshot[0].Observations["native"] = SidebarObservation{}
	if d.SidebarLiveSnapshots()[0].Observations["native"].Status != "busy" {
		t.Fatal("snapshot aliases reader")
	}
	if err := d.RenewSidebarLease(t.Context(), scope, "g", 456); err != nil {
		t.Fatal(err)
	}
	before := d.SidebarLiveSnapshots()
	if before[0].ExpiresAt != 456 || before[0].Observations["native"].Status != "busy" {
		t.Fatal(before)
	}
	if err := d.RenewSidebarLease(t.Context(), scope, "old", 999); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "old", 999, rows); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, d.SidebarLiveSnapshots()) {
		t.Fatal("stale writer changed live view")
	}
	if _, err := d.db.Exec(`CREATE TRIGGER reject_observation BEFORE INSERT ON sidebar_observation BEGIN SELECT RAISE(ABORT,'rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "g", 999, rows); err == nil {
		t.Fatal("expected rollback")
	}
	if !reflect.DeepEqual(before, d.SidebarLiveSnapshots()) {
		t.Fatal("failed commit published observations")
	}
	if err := d.BeginSidebarGeneration(t.Context(), scope, "new"); err != nil {
		t.Fatal(err)
	}
	got := d.SidebarLiveSnapshots()[0]
	if got.Generation != "new" || got.ExpiresAt != 0 || len(got.Observations) != 0 {
		t.Fatal(got)
	}
	// Publication is serialized with writes, but readers never take that lock.
	d.sidebarLive.write.Lock()
	_ = d.SidebarLiveSnapshots()
	d.sidebarLive.write.Unlock()
}

func TestSidebarLiveEncodingBoundsAndSparseCounts(t *testing.T) {
	d := openTestDB(t)
	scope := Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if d.SidebarLiveJSON(false) != "[]" {
		t.Fatal("new process has observations")
	}
	if err := d.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
		t.Fatal(err)
	}
	rows := map[string]SidebarObservation{}
	for i := 0; i < 1100; i++ {
		rows[fmt.Sprint(i)] = SidebarObservation{Status: "idle"}
	}
	rows["0"] = SidebarObservation{Status: "busy"}
	rows["1"] = SidebarObservation{Status: "idle", PendingPermission: true}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "g", 123, rows); err != nil {
		t.Fatal(err)
	}
	if d.SidebarLiveJSON(false) != "" {
		t.Fatal("encoded dense display observations")
	}
	var counts [][6]json.RawMessage
	if err := json.Unmarshal([]byte(d.SidebarLiveJSON(true)), &counts); err != nil {
		t.Fatal(err)
	}
	var selected [][7]json.RawMessage
	if err := json.Unmarshal(counts[0][5], &selected); err != nil || len(selected) != 2 {
		t.Fatalf("count rows=%d err=%v", len(selected), err)
	}
	for id := range rows {
		rows[id] = SidebarObservation{Status: "busy"}
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "g", 123, rows); err != nil {
		t.Fatal(err)
	}
	if d.SidebarLiveJSON(true) != "" {
		t.Fatal("encoded dense running observations")
	}
}

func TestSidebarLiveDoesNotRestoreAuthorityAfterRestart(t *testing.T) {
	d := openTestDB(t)
	scope := Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if err := d.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "g", 9999999999999, map[string]SidebarObservation{"native": {Status: "busy"}}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(d.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.SidebarLiveSnapshots(); got != nil {
		t.Fatalf("restored stale live authority: %v", got)
	}
}
