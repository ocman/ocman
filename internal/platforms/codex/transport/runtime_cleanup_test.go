package codextransport

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestRuntimeCleanupProbesAreSupportedAndFailClosedWithoutHistory(t *testing.T) {
	if _, ok := any(&Platform{}).(platforms.RuntimeSessionReader); ok {
		t.Fatal("strict cleanup inventory must not replace startup migration reads")
	}
	reader, ok := any(&Platform{}).(platforms.WorktreeRuntimeReader)
	if !ok {
		t.Fatal("Codex integration cannot provide cleanup safety inventory")
	}
	if _, err := reader.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("missing source accepted as empty")
	}
	if _, err := reader.WorktreeRuntimeLifecycle(t.Context(), "missing"); err == nil {
		t.Fatal("missing source accepted as an idle session")
	}
}

func cleanupCodex(t *testing.T) (*Platform, *codextest.Server) {
	t.Helper()
	s, pipes := codextest.New(t)
	c, err := codexappserver.Attach(t.Context(), pipes.Reader, pipes.Writer, codexappserver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &Platform{history: &history{client: c, start: func(context.Context) (*codexappserver.Client, error) {
		t.Fatal("unexpected reader restart")
		return nil, errors.New("restart")
	}}}, s
}

func TestRuntimeCleanupNativeCodexInventoryAndLifecycle(t *testing.T) {
	p, s := cleanupCodex(t)
	s.Handle(codexappserver.MethodThreadList, func(raw json.RawMessage) (any, error) {
		var params codexappserver.ThreadListParams
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatal(err)
		}
		if params.Archived == nil || len(params.SourceKinds) != len(codexappserver.AllSourceKinds) {
			t.Fatalf("incomplete query: %+v", params)
		}
		id := "root"
		if *params.Archived {
			id = "child"
		}
		row := thread(id)
		if id == "child" {
			row["parentThreadId"] = "root"
			row["ephemeral"] = true
		}
		return map[string]any{"data": []any{row}}, nil
	})
	rows, err := p.WorktreeRuntimeSessions(t.Context())
	if err != nil || len(rows) != 2 || rows[1].ParentID != "root" {
		t.Fatalf("hidden native child missing: %+v %v", rows, err)
	}
	p.home = "/fixture/codex"
	catalog, err := sessioncatalog.New(statetest.Open(t, sessioncatalog.Schema))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := owned.New(t.Context(), p, catalog, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	canonicalRows, err := canonical.GetSessionIdentities(t.Context())
	if err != nil || len(canonicalRows) != 2 || canonicalRows[1].ParentID != canonicalRows[0].ID || canonicalRows[1].ID == "child" {
		t.Fatalf("canonical cleanup inventory: %+v %v", canonicalRows, err)
	}
	for _, tc := range []struct {
		native string
		want   db.SessionStatus
		bad    bool
	}{{"active", db.StatusBusy, false}, {"idle", db.StatusDone, false}, {"notLoaded", db.StatusDone, false}, {"systemError", db.StatusError, false}, {"unknown", "", true}} {
		t.Run(tc.native, func(t *testing.T) {
			s.Handle(codexappserver.MethodThreadRead, func(raw json.RawMessage) (any, error) {
				var params struct {
					IncludeTurns bool `json:"includeTurns"`
				}
				_ = json.Unmarshal(raw, &params)
				if params.IncludeTurns {
					t.Error("cleanup requested transcript contents")
				}
				row := thread("child")
				row["status"] = map[string]any{"type": tc.native}
				return map[string]any{"thread": row}, nil
			})
			life, err := p.WorktreeRuntimeLifecycle(t.Context(), "child")
			if tc.bad {
				if err == nil {
					t.Fatal("unknown runtime status accepted")
				}
				return
			}
			if err != nil || life.Status != tc.want || life.Directory != "/repo" || life.LatestMessageCreated != 2000 {
				t.Fatalf("lifecycle: %+v %v", life, err)
			}
			if got, err := canonical.GetSessionLifecycle(t.Context(), canonicalRows[1].ID); err != nil || got != life {
				t.Fatalf("canonical native lifecycle: %+v %v", got, err)
			}
		})
	}
	s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) { return map[string]any{"thread": thread("child")}, nil })
	if life, err := p.SessionLifecycle(t.Context(), "child"); err != nil || life.Status != db.StatusDone {
		t.Fatalf("non-launching lifecycle: %+v %v", life, err)
	}
	if p.current() != nil {
		t.Fatal("cleanup launched execution supervisor")
	}
	for _, call := range s.Calls() {
		if call.Method != codexappserver.MethodInitialize && call.Method != codexappserver.MethodThreadList && call.Method != codexappserver.MethodThreadRead {
			t.Fatalf("unexpected runtime operation: %s", call.Method)
		}
	}
	s.Handle(codexappserver.MethodThreadList, func(json.RawMessage) (any, error) { return nil, errors.New("source offline") })
	if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("failed inventory accepted")
	}
	s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) { return nil, errors.New("source offline") })
	if _, err := p.WorktreeRuntimeLifecycle(t.Context(), "child"); err == nil {
		t.Fatal("failed lifecycle accepted")
	}
}

func TestRuntimeCleanupRunningCodexSupervisor(t *testing.T) {
	r := newRig(t)
	p := r.platform(t.Context())
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	adapter := p.current()
	rows, err := p.WorktreeRuntimeSessions(t.Context())
	if err != nil || len(rows) == 0 {
		t.Fatalf("running inventory: %+v %v", rows, err)
	}
	if life, err := p.WorktreeRuntimeLifecycle(t.Context(), created.ID); err != nil || life.Directory != "/repo" {
		t.Fatalf("running lifecycle: %+v %v", life, err)
	}
	if _, err := p.SessionLifecycle(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if p.current() != adapter {
		t.Fatal("cleanup replaced execution supervisor")
	}
	r.killSupervisors()
	if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("disconnected supervisor accepted")
	}
	if p.current() != adapter {
		t.Fatal("failed cleanup probe relaunched supervisor")
	}
}
