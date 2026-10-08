package state

import (
	"path/filepath"
	"testing"
)

func TestStopEvidenceSurvivesRecoveryAndFailedStopRefreshes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	baseline := map[string]SessionInterruption{"s": {MessageID: "user", BaselineStatus: "busy", BaselineMessageCreated: 10}}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", baseline); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", baseline, 100); err != nil {
		t.Fatal(err)
	}
	// A positively validated original instance releases a failed Stop attempt;
	// only then may fresh preparation replace its boundary.
	if err := database.CancelSessionReplacementStop(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", baseline); err != nil {
		t.Fatal(err)
	}
	at, got, err := database.SessionReplacementStopEvidence(t.Context(), "opencode", "/root")
	if err != nil || at != 0 || len(got) != 0 {
		t.Fatalf("failed attempt retained boundary: %d %v %v", at, got, err)
	}
	baseline["s"] = SessionInterruption{MessageID: "assistant", BaselineStatus: "busy", BaselineMessageCreated: 110}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", baseline, 200); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	at, got, err = database.SessionReplacementStopEvidence(t.Context(), "opencode", "/root")
	if err != nil || at != 200 || got["s"] != baseline["s"] {
		t.Fatalf("recovery changed stop evidence: %d %v %v", at, got, err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 300); err == nil {
		t.Fatal("stopped evidence was resampled")
	}
	if _, err := database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	at, got, err = database.SessionReplacementStopEvidence(t.Context(), "opencode", "/root")
	if err != nil || at != 0 || len(got) != 0 {
		t.Fatalf("confirmed attempt retained stop evidence: %d %v %v", at, got, err)
	}
}

func TestStopEvidenceWriteFailureIsAtomic(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/missing", nil, 1); err == nil {
		t.Fatal("unprepared attempt accepted")
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER reject_stop_evidence BEFORE UPDATE ON session_replacement WHEN NEW.phase='stopping' BEGIN SELECT RAISE(ABORT, 'stop evidence unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": {MessageID: "m"}}, 100); err == nil {
		t.Fatal("failed evidence write succeeded")
	}
	var phase string
	var at int64
	if err := database.db.QueryRow(`SELECT phase, stop_started_at FROM session_replacement`).Scan(&phase, &at); err != nil || phase != "prepared" || at != 0 {
		t.Fatalf("partial evidence: %s %d %v", phase, at, err)
	}
}
