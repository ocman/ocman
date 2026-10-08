package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestStopBoundaryAdvancingAborts(t *testing.T) {
	for _, kind := range []string{"user-to-assistant", "assistant-to-assistant", "new-turn-during-preparation", "new-session-during-preparation", "new-session-during-stop"} {
		t.Run(kind, func(t *testing.T) {
			srv, raw := newInterruptionRawServer(t)
			defer raw.Close()
			statuses := map[string]db.SessionStatus{"s": db.StatusBusy}
			role := "assistant"
			if kind == "user-to-assistant" {
				role = "user"
			}
			if kind == "new-turn-during-preparation" {
				statuses["s"] = db.StatusDone
			}
			if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('old','s',1,?)`, fmt.Sprintf(`{"role":%q,"finish":"tool-calls"}`, role)); err != nil {
				t.Fatal(err)
			}
			registerBoundaryLifecycle(srv, raw, statuses)
			if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
				t.Fatal(err)
			}
			id := "s"
			if kind == "new-session-during-preparation" || kind == "new-session-during-stop" {
				id = "new-session"
			}
			if kind == "new-turn-during-preparation" || kind == "new-session-during-preparation" {
				if id != "s" {
					if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES (?, '/repo')`, id); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('new-user',?,2,'{"role":"user"}')`, id); err != nil {
					t.Fatal(err)
				}
				statuses[id] = db.StatusBusy
			}
			if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			if kind == "new-session-during-stop" {
				if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES (?, '/repo')`, id); err != nil {
					t.Fatal(err)
				}
			}
			at := time.Now().UnixMilli()
			if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('aborted',?,?,?)`, id, at, fmt.Sprintf(`{"role":"assistant","error":{"name":"MessageAbortedError"},"time":{"completed":%d}}`, at)); err != nil {
				t.Fatal(err)
			}
			statuses[id] = db.StatusWaiting
			if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id)
			if err != nil || len(got) != 1 || got[0].MessageID != "aborted" {
				t.Fatalf("advancing stop abort lost: %v %v", got, err)
			}
		})
	}
}

func TestStopBoundaryExcludesManualAbortDuringPreparation(t *testing.T) {
	for _, laggingLive := range []bool{false, true} {
		t.Run(fmt.Sprintf("lagging-live=%t", laggingLive), func(t *testing.T) {
			srv, raw := newInterruptionRawServer(t)
			defer raw.Close()
			statuses := map[string]db.SessionStatus{"s": db.StatusBusy}
			if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,data) VALUES ('old','s','{"role":"assistant","finish":"tool-calls"}')`); err != nil {
				t.Fatal(err)
			}
			registerBoundaryLifecycle(srv, raw, statuses)
			if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UnixMilli()
			if _, err := raw.Exec(`UPDATE message SET data=? WHERE id='old'`, fmt.Sprintf(`{"role":"assistant","error":{"name":"MessageAbortedError"},"time":{"completed":%d}}`, at)); err != nil {
				t.Fatal(err)
			}
			if !laggingLive {
				statuses["s"] = db.StatusWaiting
			}
			if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
			if err != nil || len(got) != 0 {
				t.Fatalf("pre-stop manual abort attributed to Stop: %v %v", got, err)
			}
		})
	}
}

func TestStopBoundaryRevalidationFailurePreventsPartialEvidence(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	readErr := errors.New("pre-stop lifecycle unavailable")
	fail := false
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s", Directory: "/repo"}}}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		if fail {
			return nil, readErr
		}
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m"}, nil
	}})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); !errors.Is(err, readErr) {
		t.Fatalf("lost stop revalidation error: %v", err)
	}
	at, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
	if err != nil || at != 0 {
		t.Fatalf("failed revalidation persisted stop boundary: %d %v", at, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := srv.beginOpencodeReplacementStop(ctx, "/repo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled revalidation proceeded: %v", err)
	}
}

func TestStopBoundaryAdvancingAbortRecovery(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	path := filepath.Join(t.TempDir(), "boundary-state.db")
	durable, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srv.stateDB = durable
	defer func() { _ = srv.stateDB.Close() }()
	statuses := map[string]db.SessionStatus{"s": db.StatusBusy}
	if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('user','s',1,'{"role":"user"}')`); err != nil {
		t.Fatal(err)
	}
	registerBoundaryLifecycle(srv, raw, statuses)
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	at, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
	if err != nil || at == 0 {
		t.Fatalf("missing stop boundary: %d %v", at, err)
	}
	if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('assistant','s',?,'invalid JSON')`, at); err != nil {
		t.Fatal(err)
	}
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err == nil {
		t.Fatal("invalid final evidence did not fail confirmation")
	}
	if err := srv.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	srv.stateDB, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Recovery must not resample the waiting view or change the original boundary.
	statuses["s"] = db.StatusWaiting
	if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	recoveredAt, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
	if err != nil || recoveredAt != at {
		t.Fatalf("recovery replaced boundary: %d %d %v", at, recoveredAt, err)
	}
	if _, err := raw.Exec(`UPDATE message SET data=? WHERE id='assistant'`, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, at)); err != nil {
		t.Fatal(err)
	}
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0].MessageID != "assistant" {
		t.Fatalf("advancing abort lost on recovery: %v %v", got, err)
	}
}

func TestStopBoundaryAbortEvidence(t *testing.T) {
	for _, tc := range []struct {
		name               string
		created, completed int64
		prior              state.SessionInterruption
		sampled            bool
		want               bool
	}{
		{"manual before stop", 1, 99, state.SessionInterruption{MessageID: "m", BaselineStatus: "busy"}, true, false},
		{"same active envelope", 1, 100, state.SessionInterruption{MessageID: "m", BaselineStatus: "busy"}, true, true},
		{"already observed abort", 1, 100, state.SessionInterruption{MessageID: "m", BaselineStatus: "busy", BaselineErrorName: "AbortError"}, true, false},
		{"settled envelope", 1, 100, state.SessionInterruption{MessageID: "m", BaselineStatus: "waiting"}, true, false},
		{"new during stop", 100, 100, state.SessionInterruption{}, false, true},
		{"ancient unsampled abort", 1, 100, state.SessionInterruption{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := db.SessionLifecycle{LatestMessageID: "m", LatestMessageCreated: tc.created, LatestCompleted: tc.completed, LatestErrorName: "AbortError"}
			if got := replacementAbortDuringStop(l, tc.prior, tc.sampled, 100); got != tc.want {
				t.Fatalf("abort evidence=%t want=%t", got, tc.want)
			}
			if replacementAbortDuringStop(l, tc.prior, tc.sampled, 0) {
				t.Fatal("preparation without a stop boundary attributed abort")
			}
		})
	}
}

func registerBoundaryLifecycle(srv *Server, raw *sql.DB, statuses map[string]db.SessionStatus) {
	srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
		var message string
		var created int64
		err := raw.QueryRow(`SELECT id,time_created FROM message WHERE session_id=? ORDER BY time_created DESC,id DESC LIMIT 1`, id).Scan(&message, &created)
		return &platforms.SessionLifecycle{Status: statuses[id], LatestMessageID: message, LatestMessageCreated: created}, err
	}})
}
