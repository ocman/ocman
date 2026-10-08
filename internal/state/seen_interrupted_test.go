package state

import "testing"

func TestMigrateSeenInterruption(t *testing.T) {
	d := openTestStateDB(t)
	defer d.Close()
	if err := d.MarkSessionSeen(t.Context(), "local", "s", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`ALTER TABLE seen_session DROP COLUMN interrupted; DELETE FROM schema_version WHERE version > 111`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrate(d.db); err != nil {
			t.Fatal(err)
		}
	}
	records, err := d.SeenSessionStates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := records[Key{"local", "s"}]; got != (SeenSessionState{100, false}) {
		t.Fatalf("existing read changed during upgrade: %+v", got)
	}
}

func TestSeenInterruptionWatermark(t *testing.T) {
	d := openTestStateDB(t)
	defer d.Close()
	for _, step := range []struct {
		updated     int64
		interrupted bool
		want        SeenSessionState
	}{
		{100, false, SeenSessionState{100, false}},
		{100, true, SeenSessionState{100, true}},
		{99, false, SeenSessionState{100, true}},
		{100, false, SeenSessionState{100, true}},
		{101, false, SeenSessionState{101, false}},
		{101, true, SeenSessionState{101, true}},
		{101, false, SeenSessionState{101, true}},
	} {
		if err := d.MarkSessionSeen(t.Context(), "local", "s", step.updated, step.interrupted); err != nil {
			t.Fatal(err)
		}
		records, err := d.SeenSessionStates(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got := records[Key{"local", "s"}]; got != step.want {
			t.Fatalf("got %+v, want %+v", got, step.want)
		}
	}
	if err := d.MarkSessionSeen(t.Context(), "remote", "s", 100); err != nil {
		t.Fatal(err)
	}
	records, err := d.SeenSessionStates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if records[Key{"remote", "s"}].Interrupted {
		t.Fatal("interruption acknowledgment leaked to another owner")
	}
}
