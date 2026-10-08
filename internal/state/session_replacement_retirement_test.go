package state

import (
	"testing"
	"time"
)

func TestConfirmationRetirementFailureIsAtomic(t *testing.T) {
	for _, progressive := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "progressive"}[progressive], func(t *testing.T) {
			database := openTestStateDB(t)
			defer database.Close()
			original := ManagedInstance{Endpoint: "http://127.0.0.1:1234", Kind: "native-tmux", RuntimeID: "original", PID: 1}
			if err := database.UpsertManagedOpencode(t.Context(), "/root", original, time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}
			if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
				t.Fatal(err)
			}
			notice := SessionInterruption{MessageID: "m", ObservedAt: 100, Message: "original cause"}
			publish := func() error {
				_, err := database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": notice})
				return err
			}
			if progressive {
				if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 50, ReplacementRuntime{ManagedInstance: original, RepoRoot: "/root"}); err != nil {
					t.Fatal(err)
				}
				if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
					t.Fatal(err)
				}
				candidate := ReplacementCandidate{SessionID: "s", Directory: "/root"}
				if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", []ReplacementCandidate{candidate}); err != nil {
					t.Fatal(err)
				}
				if err := database.CheckpointReplacementCandidate(t.Context(), "opencode", "/root", candidate, true, notice); err != nil {
					t.Fatal(err)
				}
				publish = func() error {
					_, err := database.PublishReplacementReconciliation(t.Context(), "opencode", "/root")
					return err
				}
			}
			if _, err := database.db.Exec(`CREATE TRIGGER reject_retirement BEFORE DELETE ON managed_opencode BEGIN SELECT RAISE(ABORT,'retirement unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			if err := publish(); err == nil {
				t.Fatal("confirmation succeeded without retiring inventory")
			}
			if _, found, err := database.GetManagedOpencode(t.Context(), "/root"); err != nil || !found {
				t.Fatalf("failed retirement lost original handle: %t %v", found, err)
			}
			if got, err := database.SessionInterruptions(t.Context(), "opencode", "s"); err != nil || len(got) != 0 {
				t.Fatalf("retirement failure published history: %v %v", got, err)
			}
			if progressive {
				if stopped, err := database.ReplacementStopped(t.Context(), "opencode", "/root"); err != nil || !stopped {
					t.Fatalf("retirement failure cleared closure proof: %t %v", stopped, err)
				}
			}
			if _, err := database.db.Exec(`DROP TRIGGER reject_retirement`); err != nil {
				t.Fatal(err)
			}
			// Failure later in the same transaction must also restore inventory.
			if _, err := database.db.Exec(`CREATE TRIGGER reject_notice BEFORE INSERT ON session_interruption BEGIN SELECT RAISE(ABORT,'notice unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			if err := publish(); err == nil {
				t.Fatal("notice failure ignored")
			}
			if _, found, err := database.GetManagedOpencode(t.Context(), "/root"); err != nil || !found {
				t.Fatalf("failed publication left inventory retired: %t %v", found, err)
			}
			if _, err := database.db.Exec(`DROP TRIGGER reject_notice`); err != nil {
				t.Fatal(err)
			}
			if err := publish(); err != nil {
				t.Fatal(err)
			}
			if _, found, err := database.GetManagedOpencode(t.Context(), "/root"); err != nil || found {
				t.Fatalf("successful confirmation retained stopped row: %t %v", found, err)
			}
			if got, err := database.SessionInterruptions(t.Context(), "opencode", "s"); err != nil || len(got) != 1 || got[0] != notice {
				t.Fatalf("original notice changed: %v %v", got, err)
			}
			fresh := ManagedInstance{Endpoint: "http://127.0.0.1:4321", RuntimeID: "fresh"}
			if err := database.UpsertManagedOpencode(t.Context(), "/root", fresh, time.Unix(2, 0)); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
				t.Fatal(err)
			}
			if got, found, err := database.GetManagedOpencode(t.Context(), "/root"); err != nil || !found || got.RuntimeID != "fresh" {
				t.Fatalf("confirmed replay retired fresh runtime: %+v %t %v", got, found, err)
			}
		})
	}
}

func TestRetirementPreservesNewHandleOtherRootAndPlatform(t *testing.T) {
	for _, platform := range []string{"opencode", "another-platform"} {
		t.Run(platform, func(t *testing.T) {
			database := openTestStateDB(t)
			defer database.Close()
			original := ManagedInstance{Endpoint: "http://127.0.0.1:1234", Kind: "native-tmux", RuntimeID: "original", PID: 1}
			if err := database.PrepareSessionInterruptions(t.Context(), platform, "/root", nil); err != nil {
				t.Fatal(err)
			}
			if err := database.BeginSessionReplacementStop(t.Context(), platform, "/root", nil, 10, ReplacementRuntime{ManagedInstance: original, RepoRoot: "/root"}); err != nil {
				t.Fatal(err)
			}
			if err := database.MarkReplacementStopped(t.Context(), platform, "/root"); err != nil {
				t.Fatal(err)
			}
			fresh := original
			if platform == "opencode" {
				fresh.RuntimeID = "new-owner-at-same-endpoint"
			}
			if err := database.UpsertManagedOpencode(t.Context(), "/root", fresh, time.Unix(2, 0)); err != nil {
				t.Fatal(err)
			}
			if err := database.UpsertManagedOpencode(t.Context(), "/other-root", original, time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ConfirmSessionInterruptions(t.Context(), platform, "/root", nil); err != nil {
				t.Fatal(err)
			}
			if got, found, err := database.GetManagedOpencode(t.Context(), "/root"); err != nil || !found || got.RuntimeID != fresh.RuntimeID {
				t.Fatalf("retired different owner/handle: %+v %t %v", got, found, err)
			}
			if _, found, err := database.GetManagedOpencode(t.Context(), "/other-root"); err != nil || !found {
				t.Fatalf("retired other root: %t %v", found, err)
			}
		})
	}
}
