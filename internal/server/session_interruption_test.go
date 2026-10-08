package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

type interruptionLifecyclePlatform struct {
	*fakePlatform
	lifecycle func(string) (*platforms.SessionLifecycle, error)
}

func recordStoppedReplacement(t *testing.T, srv *Server, root, reason string) error {
	t.Helper()
	if err := srv.recordOpencodeReplacement(t.Context(), root, reason); err != nil {
		return err
	}
	return srv.confirmOpencodeReplacement(t.Context(), root)
}

func (p *interruptionLifecyclePlatform) SessionLifecycle(_ context.Context, id string) (*platforms.SessionLifecycle, error) {
	return p.lifecycle(id)
}

func TestReplacementUsesFreshBoundedLifecycleRead(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{
			{ID: "active", Directory: "/repo", Status: db.StatusBusy},
			{ID: "settled", Directory: "/repo", Status: db.StatusBusy},
			{ID: "missing", Directory: "/repo", Status: db.StatusInterrupted},
		}},
		lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
			switch id {
			case "active":
				return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "latest"}, nil
			case "settled":
				return &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "old"}, nil
			default:
				return nil, nil
			}
		},
	})
	if err := recordStoppedReplacement(t, srv, "/repo", "requested restart"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"active", "settled", "missing"} {
		got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id)
		want := 0
		if id == "active" {
			want = 1
		}
		if err != nil || len(got) != want {
			t.Fatalf("%s notices=%v err=%v", id, got, err)
		}
		if id == "active" && got[0].MessageID != "latest" {
			t.Fatal("stale message identity")
		}
	}
}

func TestReplacementDoesNotFilterCandidatesByCachedTerminalStatus(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "waiting", Directory: "/repo", Status: db.StatusWaiting}, {ID: "done", Directory: "/repo", Status: db.StatusDone}}},
		lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
			return &platforms.SessionLifecycle{Status: db.StatusInterrupted, LatestMessageID: "latest-" + id}, nil
		},
	})
	if err := recordStoppedReplacement(t, srv, "/repo", "failed health probe"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"waiting", "done"} {
		got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id)
		if err != nil || len(got) != 1 || got[0].MessageID != "latest-"+id {
			t.Fatalf("cached terminal status lost interrupted turn: %s %v %v", id, got, err)
		}
	}
}

func TestReplacementFailsIfLifecycleCannotBeRead(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	readErr := errors.New("lifecycle unavailable")
	reg.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "active", Directory: "/repo", Status: db.StatusBusy}}},
		lifecycle:    func(string) (*platforms.SessionLifecycle, error) { return nil, readErr },
	})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "requested restart"); !errors.Is(err, readErr) {
		t.Fatalf("lost lifecycle failure: %v", err)
	}
}

func TestInterruptedTurnGetsDurableHistoryNotice(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		return &platforms.SessionLifecycle{Status: db.StatusInterrupted, LatestMessageID: "m1"}, nil
	}})
	detail := &platforms.SessionDetail{
		Session:  &db.Session{ID: "s1", Platform: "opencode", Status: db.StatusInterrupted},
		Messages: []db.Message{{ID: "m1", SessionID: "s1", TimeCreated: 100, Data: json.RawMessage(`{"role":"assistant","time":{"created":100}}`)}},
	}
	srv.enrichSessionDetail(t.Context(), "opencode", "s1", detail, true)
	if len(detail.Messages) != 2 || len(detail.Parts) != 1 || !strings.Contains(string(detail.Parts[0].Data), "interrupted") {
		t.Fatalf("missing persisted interruption in history: messages=%v parts=%v", detail.Messages, detail.Parts)
	}
	srv.enrichSessionDetail(t.Context(), "opencode", "s1", detail, true)
	if len(detail.Messages) != 2 || len(detail.Parts) != 1 {
		t.Fatal("repeated refresh duplicated notice")
	}
	followup := &platforms.SessionDetail{Session: &db.Session{ID: "s1", Status: db.StatusDone}, Messages: []db.Message{{ID: "m2", TimeCreated: detail.Messages[1].TimeCreated + 1, Data: json.RawMessage(`{"role":"user"}`)}}}
	srv.enrichSessionDetail(t.Context(), "opencode", "s1", followup, true)
	if len(followup.Messages) != 2 || len(followup.Parts) != 1 || followup.Messages[0].ID != detail.Messages[1].ID {
		t.Fatalf("notice lost or out of order after follow-up: %v", followup.Messages)
	}
}

func TestOlderHistoryPagesDoNotCreateInterruptionIdentities(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		return &platforms.SessionLifecycle{Status: db.StatusInterrupted, LatestMessageID: "current-turn"}, nil
	}})
	for _, id := range []string{"current-turn", "completed-turn", "older-completed-turn"} {
		detail := &platforms.SessionDetail{Session: &db.Session{Status: db.StatusInterrupted}, Messages: []db.Message{{ID: id, Data: json.RawMessage(`{"role":"assistant"}`)}}}
		srv.enrichSessionDetail(t.Context(), "opencode", "s1", detail, true)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s1")
	if err != nil || len(got) != 1 || got[0].MessageID != "current-turn" {
		t.Fatalf("older pages invented permanent interruptions: %v %v", got, err)
	}
}

func TestReplacementReadFailureDoesNotPersistPartialHistory(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	srv.broadcastHub = newBroadcastHub()
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	readErr := errors.New("second lifecycle read failed")
	reg.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "first", Directory: "/repo", Status: db.StatusBusy}, {ID: "second", Directory: "/repo", Status: db.StatusBusy}}},
		lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
			if id == "second" {
				return nil, readErr
			}
			return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m-first"}, nil
		},
	})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "requested restart"); !errors.Is(err, readErr) {
		t.Fatalf("lost preparation failure: %v", err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "first")
	if err != nil || len(got) != 0 {
		t.Fatalf("aborted replacement left false history: %v %v", got, err)
	}
	select {
	case event := <-sub.ch:
		t.Fatalf("aborted preparation broadcast a notice: %+v", event)
	default:
	}
}

func TestReplacementSkipsCandidatesDeletedDuringEnumeration(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "deleted-judge", Directory: "/repo"}, {ID: "active", Directory: "/repo"}}},
		lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
			if id == "deleted-judge" {
				return nil, platforms.ErrNotFound
			}
			return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "active-turn"}, nil
		},
	})
	if err := recordStoppedReplacement(t, srv, "/repo", "requested restart"); err != nil {
		t.Fatalf("vanished judge blocked restart: %v", err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "active")
	if err != nil || len(got) != 1 || got[0].MessageID != "active-turn" {
		t.Fatalf("surviving turn lost its notice: %v %v", got, err)
	}
	got, err = srv.stateDB.SessionInterruptions(t.Context(), "opencode", "deleted-judge")
	if err != nil || len(got) != 0 {
		t.Fatalf("vanished judge got a false notice: %v %v", got, err)
	}
}

func TestReplacementPreparationDoesNotClaimConfirmedInterruption(t *testing.T) {
	srv, reg := newInterruptionTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "active", Directory: "/repo"}}},
		lifecycle: func(string) (*platforms.SessionLifecycle, error) {
			return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "turn"}, nil
		},
	})
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "requested restart"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "active")
	if err != nil || len(got) != 0 {
		t.Fatalf("preparation already claims an interruption: %v %v", got, err)
	}
}

func TestReplacementHistoryIsOwnerScopedAndSurvivesAbort(t *testing.T) {
	for _, machine := range []bool{false, true} {
		t.Run(map[bool]string{false: "project", true: "machine"}[machine], func(t *testing.T) {
			ocv2.SetInstalledV2(machine)
			t.Cleanup(func() { ocv2.SetInstalledV2(false) })
			srv, reg := newInterruptionTestServer(t)
			reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{
				{ID: "active", Directory: "/projects/.worktrees/repo/a", Status: db.StatusBusy},
				{ID: "other", Directory: "/projects/other", Status: db.StatusBusy},
				{ID: "idle", Directory: "/projects/repo", Status: db.StatusWaiting},
			}, sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
				return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusWaiting}, Messages: []db.Message{{ID: "m-" + id, Data: json.RawMessage(`{"role":"assistant","error":{"name":"MessageAbortedError"}}`)}}}, nil
			}}, lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
				if id == "idle" {
					return &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageID: "m-idle"}, nil
				}
				return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m-" + id}, nil
			}})
			if err := recordStoppedReplacement(t, srv, "/projects/repo", "requested restart"); err != nil {
				t.Fatal(err)
			}
			if err := recordStoppedReplacement(t, srv, "/projects/repo", "failed health probe"); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"active", "other", "idle"} {
				notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id)
				want := id == "active" || machine && id == "other"
				wantCount := 0
				if want {
					wantCount = 1
				}
				if err != nil || len(notices) != wantCount {
					t.Fatalf("%s notices=%v err=%v", id, notices, err)
				}
				if want && !strings.Contains(notices[0].Message, "requested restart") {
					t.Fatal("first cause overwritten")
				}
			}
			owner := remote.NewServer(reg, nil, "owner", "test").UseSessionEnricher(srv.EnrichRemoteSessionDetail)
			resp, err := owner.Session(t.Context(), &pb.SessionReq{Platform: "opencode", SessionId: "active"})
			if err != nil {
				t.Fatal(err)
			}
			var detail platforms.SessionDetail
			if err := json.Unmarshal(resp.Payload, &detail); err != nil {
				t.Fatal(err)
			}
			if len(detail.Messages) != 2 || len(detail.Parts) != 1 {
				t.Fatalf("owner RPC lost restart notice: %+v", detail)
			}
		})
	}
}

func TestReplacementFindsFreshChildrenOmittedByDisplayList(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO session(id,directory,parent_id) VALUES ('parent','/repo',NULL), ('hidden-child','/repo','parent');
		INSERT INTO message(id,session_id,data) VALUES ('child-turn','hidden-child','{"role":"assistant"}');
		INSERT INTO part(id,message_id,session_id,data) VALUES ('child-step','child-turn','hidden-child','{"type":"step-start"}')`); err != nil {
		t.Fatal(err)
	}
	srv.registry.Register(&interruptionLifecyclePlatform{
		fakePlatform: &fakePlatform{id: "opencode", sessions: nil},
		lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
			if id == "parent" {
				return &platforms.SessionLifecycle{Status: db.StatusDone}, nil
			}
			return &platforms.SessionLifecycle{Status: db.StatusInterrupted, LatestMessageID: "child-turn"}, nil
		},
	})
	if err := recordStoppedReplacement(t, srv, "/repo", "requested restart"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "hidden-child")
	if err != nil || len(got) != 1 || got[0].MessageID != "child-turn" {
		t.Fatalf("display filtering lost fresh candidate: %v %v", got, err)
	}
}

func TestCompletedShellEnvelopeDoesNotGetInterruptionHistory(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	if _, err := raw.Exec(`
		INSERT INTO session(id,directory) VALUES ('shell','/repo'), ('open-turn','/repo'), ('running-shell','/repo');
		INSERT INTO message(id,session_id,time_created,data) VALUES
			('shell-message','shell',100,'{"role":"assistant","time":{"created":100}}'),
			('open-message','open-turn',100,'{"role":"assistant","time":{"created":100}}'),
			('running-message','running-shell',100,'{"role":"assistant","time":{"created":100}}');
		INSERT INTO part(id,message_id,session_id,data) VALUES
			('shell-part','shell-message','shell','{"type":"tool","tool":"bash","state":{"status":"completed"}}'),
			('open-part','open-message','open-turn','{"type":"step-start"}'),
			('running-part','running-message','running-shell','{"type":"tool","tool":"bash","state":{"status":"running"}}');
	`); err != nil {
		t.Fatal(err)
	}
	if err := recordStoppedReplacement(t, srv, "/repo", "requested restart"); err != nil {
		t.Fatal(err)
	}
	shell, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "shell")
	if err != nil || len(shell) != 0 {
		t.Fatalf("completed shell got permanent false history: %v %v", shell, err)
	}
	active, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "open-turn")
	if err != nil || len(active) != 1 {
		t.Fatalf("real unfinished turn lost its notice: %v %v", active, err)
	}
	running, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "running-shell")
	if err != nil || len(running) != 1 {
		t.Fatalf("unfinished shell lost its notice: %v %v", running, err)
	}
}

func TestConfirmationDropsCompletedTurnAndCapturesNewTurn(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo');
		INSERT INTO message(id,session_id,time_created,data) VALUES ('old','s',100,'{"role":"assistant"}');
		INSERT INTO part(id,message_id,session_id,data) VALUES ('p-old','old','s','{"type":"step-start"}')`); err != nil {
		t.Fatal(err)
	}
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "requested restart"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE message SET data='{"role":"assistant","finish":"stop"}' WHERE id='old';
		INSERT INTO message(id,session_id,time_created,data) VALUES ('new','s',200,'{"role":"assistant"}');
		INSERT INTO part(id,message_id,session_id,data) VALUES ('p-new','new','s','{"type":"step-start"}');
		INSERT INTO session(id,directory) VALUES ('new-session','/repo');
		INSERT INTO message(id,session_id,time_created,data) VALUES ('first-prompt','new-session',201,'{"role":"user"}')`); err != nil {
		t.Fatal(err)
	}
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0].MessageID != "new" {
		t.Fatalf("pre-stop snapshot blindly confirmed: %v %v", got, err)
	}
	newSession, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "new-session")
	if err != nil || len(newSession) != 1 || newSession[0].MessageID != "first-prompt" {
		t.Fatalf("submission during preparation lost its history: %v %v", newSession, err)
	}
}

func TestConfirmationDoesNotMarkTurnThatFinishedBeforeStop(t *testing.T) {
	srv, raw := newInterruptionRawServer(t)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo');
		INSERT INTO message(id,session_id,data) VALUES ('m','s','{"role":"assistant"}');
		INSERT INTO part(id,message_id,session_id,data) VALUES ('p','m','s','{"type":"step-start"}')`); err != nil {
		t.Fatal(err)
	}
	if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "requested restart"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE message SET data='{"role":"assistant","finish":"stop"}' WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 0 {
		t.Fatalf("normally completed turn got interruption history: %v %v", got, err)
	}
}
