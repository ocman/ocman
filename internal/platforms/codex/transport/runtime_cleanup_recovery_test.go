package codextransport

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func TestCleanupRejectsUnattachedLiveCodexSupervisor(t *testing.T) {
	r := newRig(t)
	p := r.platform(t.Context())
	if _, err := p.EnsureRuntime(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	native, server := cleanupCodex(t)
	fakeCodex(server)
	server.Handle(codexappserver.MethodThreadList, func(json.RawMessage) (any, error) { return map[string]any{"data": []any{}}, nil })
	oldHistory := p.history
	t.Cleanup(oldHistory.close)
	p.history = native.history
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	p.mu.Lock()
	attached := p.adapter
	p.adapter = nil
	p.mu.Unlock()
	t.Cleanup(attached.Close)
	rows, err := p.WorktreeRuntimeSessions(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("persisted pre-transcript session omitted: %+v %v", rows, err)
	}
	row, ok, err := r.store.GetManagedProvider(t.Context(), "codex", p.root)
	if err != nil || !ok {
		t.Fatalf("persisted supervisor: %+v %v", row, err)
	}
	row.Endpoint = "unix://" + filepath.Join(r.data, "unreadable")
	if err := r.store.UpsertManagedProvider(t.Context(), "codex", p.root, row); err != nil {
		t.Fatal(err)
	}
	if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("unattached live supervisor omitted from cleanup inventory")
	}
	if _, err := p.WorktreeRuntimeLifecycle(t.Context(), "thr"); err == nil {
		t.Fatal("unattached live supervisor omitted from cleanup lifecycle")
	}
	r.killSupervisors()
	if _, err := p.WorktreeRuntimeSessions(t.Context()); err != nil {
		t.Fatalf("confirmed stopped supervisor blocked cleanup: %v", err)
	}
}

func TestCleanupRejectsCodexDrainWithoutAttachment(t *testing.T) {
	p, s := cleanupCodex(t)
	fakeCodex(s)
	p.draining = true
	if _, err := p.WorktreeRuntimeSessions(t.Context()); err == nil {
		t.Fatal("draining supervisor accepted")
	}
	if _, err := p.WorktreeRuntimeLifecycle(t.Context(), "thr"); err == nil {
		t.Fatal("draining lifecycle accepted")
	}
}

func TestStoppedCodexSupervisorPreservesPluginFollowupIdentity(t *testing.T) {
	p, s := cleanupCodex(t)
	fakeCodex(s)
	s.Handle(codexappserver.MethodThreadTurns, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []codexappserver.Turn{{ID: "turn", Status: "completed", StartedAt: 1, CompletedAt: 3}}}, nil
	})
	s.Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []codexappserver.ItemEntry{{TurnID: "turn", Item: codexappserver.Item{Type: "agentMessage", ID: "reply", Text: "Completed reply"}, StartedAtMs: 2000, CompletedAtMs: 3000}}}, nil
	})
	snapshot, err := p.HistorySnapshot(t.Context(), "thr")
	if err != nil {
		t.Fatal(err)
	}
	latest, text := db.LatestAssistantTextVerbatim(snapshot.Messages, snapshot.Parts)
	if latest == "" || text == "" {
		t.Fatalf("missing native reply: %+v", snapshot)
	}
	lifecycle, err := p.SessionLifecycle(t.Context(), "thr")
	if err != nil || lifecycle.Status != snapshot.InferredStatus || lifecycle.LatestMessageID != latest || lifecycle.LatestMessageRole != "assistant" || lifecycle.LatestMessageCreated != 2000 {
		t.Fatalf("matching plugin receipt would be rejected: latest=%s lifecycle=%+v err=%v", latest, lifecycle, err)
	}
	if p.current() != nil {
		t.Fatal("ordinary lifecycle launched execution supervisor")
	}
	for _, call := range s.Calls() {
		if call.Method == "thread/resume" {
			t.Fatal("lifecycle resumed a thread")
		}
	}
	var _ platforms.LifecycleReader = p
}
