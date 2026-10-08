package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestStopSnapshotRefreshesAdvancedIdentityAndLiveStatusTogether(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('old','s',1,'{"role":"assistant","finish":"stop"}')`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		calls++
		if calls == 2 {
			// Arrives after the authoritative read but before the raw abort check.
			if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('new-user','s',2,'{"role":"user"}')`); err != nil {
				return nil, err
			}
		}
		if calls <= 2 {
			return &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "old", LatestMessageCreated: 1}, nil
		}
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "new-user", LatestMessageCreated: 2}, nil
	}})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	at, baseline, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if baseline["s"].MessageID != "new-user" || baseline["s"].BaselineStatus != string(db.StatusBusy) {
		t.Errorf("identity and authoritative lifecycle diverged: %+v", baseline["s"])
	}
	// The assistant envelope was created before the Stop entry boundary, then
	// Stop aborted it. Its identity advances from the sampled user message.
	if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('assistant','s',3,?)`, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, at)); err != nil {
		t.Fatal(err)
	}
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0].MessageID != "assistant" {
		t.Fatalf("advanced user lost stop abort: %v %v", got, err)
	}
}

func TestStopSnapshotContinuousAdvanceFailsBeforeStop(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('m0','s',0,'{"role":"user"}')`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	advance := false
	srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		id := fmt.Sprintf("m%d", calls)
		if advance {
			calls++
			if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES (?,'s',?,'{"role":"user"}')`, fmt.Sprintf("m%d", calls), calls); err != nil {
				return nil, err
			}
		}
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: id}, nil
	}})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	advance = true
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err == nil {
		t.Fatal("unstable snapshot permitted Stop")
	}
	if calls != 3 {
		t.Fatalf("unbounded snapshot retries: %d", calls)
	}
	at, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
	if err != nil || at != 0 {
		t.Fatalf("unstable snapshot persisted boundary: %d %v", at, err)
	}
}

func TestStoppingCrashRecoversOriginalRuntimeAndAbortEvidence(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	path := filepath.Join(t.TempDir(), "crash-state.db")
	durable, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srv.stateDB = durable
	defer func() { _ = srv.stateDB.Close() }()
	if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('user','s',1,'{"role":"user"}')`); err != nil {
		t.Fatal(err)
	}
	registerBoundaryLifecycle(srv, raw, map[string]db.SessionStatus{"s": db.StatusBusy})
	original := &ocruntime.Instance{ID: "original", Endpoint: "http://127.0.0.1:1234", Kind: ocruntime.KindNativeTmux, PID: 123, RepoRoot: "/repo"}
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := srv.beginOpencodeReplacementStopWithInstance(t.Context(), "/repo", original); err != nil {
		t.Fatal(err)
	}
	at, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	// Fake runtime shutdown writes the aborted envelope, then the owner crashes
	// before AfterStop. No managed runtime row exists to supply the old handle.
	rt := &recoveryWiringRuntime{stop: func() error {
		_, err := raw.Exec(`INSERT OR IGNORE INTO message(id,session_id,time_created,data) VALUES ('assistant','s',?,?)`, at, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, at))
		return err
	}}
	if err := rt.Stop(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	srv.stateDB, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := srv.opencodeReplacementStopping(t.Context(), "/repo")
	if err != nil || recovered == nil || *recovered != *original {
		t.Fatalf("lost original stop handle: %+v %v", recovered, err)
	}
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "recovery"); !errors.Is(err, state.ErrReplacementStopping) {
		t.Fatalf("recovery refreshed unresolved evidence: %v", err)
	}
	// This is the host recovery contract: prove closure using the original
	// handle, then confirm without resampling or creating another attempt.
	if err := rt.Stop(t.Context(), recovered); err != nil {
		t.Fatal(err)
	}
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0].MessageID != "assistant" {
		t.Fatalf("crash lost original abort: %v %v", got, err)
	}
	if pending, err := srv.opencodeReplacementStopping(t.Context(), "/repo"); err != nil || pending != nil {
		t.Fatalf("confirmed stop still pending: %+v %v", pending, err)
	}
}

func TestStoppingCallbacksNilAndValidatedAliveRelease(t *testing.T) {
	if pending, err := (&Server{}).opencodeReplacementStopping(t.Context(), "/repo"); err != nil || pending != nil {
		t.Fatalf("nil state: %+v %v", pending, err)
	}
	if err := (&Server{}).cancelOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	srv, _ := newInterruptionTestServer(t)
	if err := srv.stateDB.PrepareSessionInterruptions(t.Context(), "opencode", "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.BeginSessionReplacementStop(t.Context(), "opencode", "/repo", nil, 1, state.ReplacementRuntime{ManagedInstance: state.ManagedInstance{Endpoint: "http://127.0.0.1:1234", RuntimeID: "original"}}); err != nil {
		t.Fatal(err)
	}
	// The host has positively validated the original instance is still alive.
	if err := srv.cancelOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "retry"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := srv.opencodeReplacementStopping(ctx, "/repo"); err == nil {
		t.Fatal("cancelled state lookup succeeded")
	}
}
