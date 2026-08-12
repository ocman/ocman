package server

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// registerSameIDOnTwoHosts registers two adapters that both own session
// "s1" — the collision multi-remote makes possible. The first registered
// adapter is what a bare-id reverse lookup resolves to, so a source that
// ignores its platform argument silently answers from the wrong machine.
func registerSameIDOnTwoHosts(t *testing.T, reg *platforms.Registry) {
	t.Helper()
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "local", 1)},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session: &db.Session{
					ID: id, Status: db.StatusDone,
					TotalInputTokens: 1, TotalOutputTokens: 1, TotalCost: 0.5,
				},
				Messages: []db.Message{{
					ID: "local-msg", TimeCreated: 100,
					Data: json.RawMessage(`{"role":"assistant"}`),
				}},
			}, nil
		},
	})
	reg.Register(&fakePlatform{
		id:       "r-A:fake",
		sessions: []db.Session{mkSession("r-A:fake", "s1", "remote", 1)},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session: &db.Session{
					ID: id, Status: db.StatusBusy,
					TotalInputTokens: 100, TotalOutputTokens: 200, TotalCost: 9,
				},
				Messages: []db.Message{{
					ID: "remote-msg", TimeCreated: 900,
					Data: json.RawMessage(`{"role":"assistant"}`),
				}},
			}, nil
		},
	})
}

// A workflow's session status must come from the platform the workflow
// recorded, not from whichever adapter happens to own the bare id first.
// Reading the wrong machine settles an agent attempt prematurely.
func TestWorkflowStatusInferer_ResolvesTheNamedPlatform(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerSameIDOnTwoHosts(t, reg)
	inferer := &workflowStatusInferer{s: srv}

	running, ok := inferer.TurnRunning(t.Context(), "r-A:fake", "s1")
	if !ok {
		t.Fatal("TurnRunning did not resolve the remote platform")
	}
	if !running {
		t.Fatal("TurnRunning = false, want the remote session's own busy status")
	}
	if running, ok := inferer.TurnRunning(t.Context(), "fake", "s1"); !ok || running {
		t.Fatalf("local TurnRunning = (%v, %v), want (false, true)", running, ok)
	}

	messageID, createdAt, running, _, ok := inferer.LatestMessageState(t.Context(), "r-A:fake", "s1")
	if !ok {
		t.Fatal("LatestMessageState did not resolve the remote platform")
	}
	if messageID != "remote-msg" || createdAt != 900 || !running {
		t.Fatalf("LatestMessageState = (%q, %d, %v), want the remote session's own state",
			messageID, createdAt, running)
	}

	// An unknown platform must fail closed rather than fall back to a
	// bare-id lookup on another machine.
	if _, ok := inferer.TurnRunning(t.Context(), "r-GONE:fake", "s1"); ok {
		t.Fatal("TurnRunning resolved an unregistered platform")
	}
}

// Budget accounting must attribute tokens and cost to the (platform,
// session) pair the attempt recorded. Summing by bare id billed a workflow
// for another machine's session.
func TestWorkflowUsage_AttributesToTheNamedPlatform(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerSameIDOnTwoHosts(t, reg)
	usage := &workflowUsage{s: srv}

	tokens, cost, ok := usage.SessionUsage(t.Context(), []state.Key{{Platform: "r-A:fake", SessionID: "s1"}})
	if !ok {
		t.Fatal("SessionUsage did not resolve the remote platform")
	}
	if tokens != 300 || cost != 9 {
		t.Fatalf("usage = (%d tokens, $%v), want the remote session's own (300, $9)", tokens, cost)
	}

	tokens, cost, ok = usage.SessionUsage(t.Context(), []state.Key{{Platform: "fake", SessionID: "s1"}})
	if !ok || tokens != 2 || cost != 0.5 {
		t.Fatalf("local usage = (%d, %v, %v), want (2, 0.5, true)", tokens, cost, ok)
	}

	// A reference whose platform is missing or unregistered contributes
	// nothing: guessing an owner would misbill the run.
	if _, _, ok := usage.SessionUsage(t.Context(), []state.Key{{SessionID: "s1"}}); ok {
		t.Fatal("SessionUsage resolved a reference with no platform")
	}
}
