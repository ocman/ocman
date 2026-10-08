package state

import (
	"path/filepath"
	"testing"
)

func TestStoppingCrashReopenCannotOverwriteOriginalEvidence(t *testing.T) {
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
	handle := ReplacementRuntime{ManagedInstance: ManagedInstance{Endpoint: "http://127.0.0.1:1234", Kind: "native-tmux", RuntimeID: "original", PID: 123}, RepoRoot: "/root"}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", baseline, 100, handle); err != nil {
		t.Fatal(err)
	}
	// Runtime shutdown succeeds, then the owner crashes before marking stopped.
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": {MessageID: "assistant", BaselineStatus: "waiting"}}); err == nil {
		t.Error("unresolved stopping attempt accepted a fresh preparation")
	}
	at, got, err := database.SessionReplacementStopEvidence(t.Context(), "opencode", "/root")
	if err != nil || at != 100 || got["s"] != baseline["s"] {
		t.Fatalf("crash recovery overwrote original evidence: at=%d baseline=%v err=%v", at, got, err)
	}
	recovered, pending, err := database.SessionReplacementStopping(t.Context(), "opencode", "/root")
	if err != nil || !pending || recovered != handle {
		t.Fatalf("original runtime lost: %+v %t %v", recovered, pending, err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 200, ReplacementRuntime{ManagedInstance: ManagedInstance{Endpoint: "replacement"}}); err == nil {
		t.Fatal("unresolved stop accepted a new snapshot")
	}
	// Closure proof reconciles the original attempt before preparation resumes.
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	at, got, err = database.SessionReplacementStopEvidence(t.Context(), "opencode", "/root")
	if err != nil || at != 100 || got["s"] != baseline["s"] {
		t.Fatalf("closure changed crash evidence: %d %v %v", at, got, err)
	}
}

func TestStoppingWithoutLegacyRuntimeHandleFailsClosed(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 1); err != nil {
		t.Fatal(err)
	}
	if _, pending, err := database.SessionReplacementStopping(t.Context(), "opencode", "/root"); err == nil || !pending {
		t.Fatal("legacy unresolved stop silently authorized refresh")
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err == nil {
		t.Fatal("legacy unresolved stop was overwritten")
	}
}

func TestStoppingHandleCancelAndInventoryIndependence(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if _, pending, err := database.SessionReplacementStopping(t.Context(), "opencode", "/root"); err != nil || pending {
		t.Fatalf("missing pending handle: %t %v", pending, err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	handle := ReplacementRuntime{ManagedInstance: ManagedInstance{Endpoint: "http://127.0.0.1:1234", RuntimeID: "original"}}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 1, handle); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteManagedOpencode(t.Context(), "/root"); err != nil {
		t.Fatal(err)
	}
	got, pending, err := database.SessionReplacementStopping(t.Context(), "opencode", "/root")
	if err != nil || !pending || got != handle {
		t.Fatalf("inventory removed original handle: %+v %t %v", got, pending, err)
	}
	if err := database.CancelSessionReplacementStop(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if _, pending, err := database.SessionReplacementStopping(t.Context(), "opencode", "/root"); err != nil || pending {
		t.Fatalf("cancel retained stopping handle: %t %v", pending, err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
}

func TestStoppingRuntimeCorruptionBlocksRecovery(t *testing.T) {
	for _, data := range []string{"not JSON", `{}`} {
		t.Run(data, func(t *testing.T) {
			database := openTestStateDB(t)
			defer database.Close()
			if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
				t.Fatal(err)
			}
			if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 1, ReplacementRuntime{ManagedInstance: ManagedInstance{Endpoint: "http://127.0.0.1:1234"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.Exec(`UPDATE session_replacement SET stop_runtime_json=?`, data); err != nil {
				t.Fatal(err)
			}
			if _, pending, err := database.SessionReplacementStopping(t.Context(), "opencode", "/root"); err == nil || !pending {
				t.Fatal("corrupt cleanup handle silently authorized recovery")
			}
		})
	}
}
