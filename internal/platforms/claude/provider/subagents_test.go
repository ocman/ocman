package claudeprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

const startChild = `{"subtype":"task_started","task_type":"local_agent","task_id":"child","tool_use_id":"spawn","description":"Investigate"}`

func TestRunningSubagentStatusAndIdentity(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	child := db.Session{ID: "agent-child", ParentID: p.id, Status: db.StatusInterrupted}
	l.overlay(&child)
	if child.Status != db.StatusBusy || l.status[p.id].Status != db.StatusBusy {
		t.Fatalf("child = %q, parent = %q, want both busy", child.Status, l.status[p.id].Status)
	}
	for _, s := range l.pending(nil, "") {
		if s.ID == child.ID && s.ParentID == p.id && !s.LiveConnection {
			return
		}
	}
	t.Fatal("missing read-only child runtime identity")
}

func TestRunningSubagentInspectionBeforeTranscript(t *testing.T) {
	l, p := sdkLifecycle(t)
	idx := openSynced(t, fixture(t))
	l.system(p, []byte(startChild))
	if err := l.handle(p, []byte(`{"type":"assistant","parent_tool_use_id":"spawn","message":{"id":"child-message","role":"assistant","content":[{"type":"text","text":"Working"}]}}`)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&handler{idx: idx, live: l}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/agent-child", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect streamed child: HTTP %d %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Session  db.Session   `json:"session"`
		Messages []db.Message `json:"messages"`
		Parts    []db.Part    `json:"parts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Session.ParentID != p.id || len(detail.Messages) != 1 || len(detail.Parts) != 1 {
		t.Fatalf("child identity: %+v %v", detail, err)
	}
	if len(l.snapshot("agent-child")) < 2 {
		t.Fatal("child snapshot lost forwarded messages")
	}
	l.idx = idx
	if _, err := l.claimForTurn(t.Context(), "agent-child"); err == nil {
		t.Fatal("unindexed child became independently resumable")
	}
}

func TestSubagentTerminalLifecycle(t *testing.T) {
	for _, subtype := range []string{"task_notification", "task_updated"} {
		for status, want := range map[string]db.SessionStatus{"completed": db.StatusDone, "failed": db.StatusError, "killed": db.StatusInterrupted, "stopped": db.StatusInterrupted} {
			t.Run(subtype+"/"+status, func(t *testing.T) {
				l, p := sdkLifecycle(t)
				l.system(p, []byte(startChild))
				l.system(p, []byte(startChild)) // duplicate start cannot reopen the child
				l.system(p, []byte(`{"subtype":"task_updated","task_id":"child","patch":{"status":"running"}}`))
				if len(l.subs[p.id]) != 1 || l.status["agent-child"].Status != db.StatusBusy {
					t.Fatal("duplicate start or nonterminal update changed child lifecycle")
				}
				payload := map[string]any{"subtype": subtype, "task_id": "child", "status": status, "patch": map[string]string{"status": status}}
				raw, _ := json.Marshal(payload)
				l.system(p, raw)
				l.system(p, raw)
				if got := l.status["agent-child"].Status; got != want || !p.busy {
					t.Fatalf("child=%q, want %q; parent busy=%v", got, want, p.busy)
				}
				// A later parent result cannot overwrite an independently settled child.
				l.result(p, []byte(`{"subtype":"success"}`))
				if l.status["agent-child"].Status != want {
					t.Fatal("parent completion overwrote child outcome")
				}
			})
		}
	}
}

func TestSubagentCompletionAfterForegroundParentResult(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	l.result(p, []byte(`{"subtype":"success"}`))
	if p.busy || l.status["agent-child"].Status != db.StatusBusy {
		t.Fatal("foreground parent must settle without ending the child")
	}
	l.system(p, []byte(`{"subtype":"task_updated","task_id":"child","patch":{"status":"failed"}}`))
	if p.busy || l.status["agent-child"].Status != db.StatusError {
		t.Fatal("deferred parent settlement lost child failure")
	}
}

func TestSubagentParentExitSettlesOnlyActiveChildren(t *testing.T) {
	for _, status := range []db.SessionStatus{db.StatusDone, db.StatusError} {
		l, p := sdkLifecycle(t)
		l.system(p, []byte(startChild))
		p.busy = false // process exit without a task terminal event
		clear(p.tasks)
		l.settle(p.id, status, l.accepted[p.id])
		want := status
		if want == db.StatusDone {
			want = db.StatusInterrupted
		}
		if l.status["agent-child"].Status != want || l.sup.Active("agent-child") {
			t.Fatalf("child after parent exit: %+v", l.status["agent-child"])
		}
	}
}

func TestSubagentParentRetriesUnfinishedTerminalPublication(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	// A terminal status was saved, but the following idle write failed.
	l.emit("agent-child", "", "session.status", map[string]any{"sessionID": "agent-child", "status": map[string]string{"type": "error"}})
	p.busy = false
	clear(p.tasks)
	l.settle(p.id, db.StatusError, l.accepted[p.id])
	if l.sup.Active("agent-child") || l.status["agent-child"].Status != db.StatusError {
		t.Fatal("parent recovery stranded a child between terminal status and idle")
	}
}

func TestNestedSubagentRuntimeParentAndNonAgentTasks(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	if err := l.handle(p, []byte(`{"type":"assistant","parent_tool_use_id":"spawn","message":{"id":"child-message","role":"assistant","content":[{"type":"tool_use","id":"nested-call","name":"Agent","input":{"description":"Nested"}}]}}`)); err != nil {
		t.Fatal(err)
	}
	l.system(p, []byte(`{"subtype":"task_started","task_type":"local_agent","task_id":"nested","tool_use_id":"nested-call","description":"Nested"}`))
	if got := l.subagents["agent-nested"].session.ParentID; got != "agent-child" {
		t.Fatalf("nested parent = %q", got)
	}
	for _, taskType := range []string{"local_bash", "monitor_mcp", "local_workflow", "in_process_teammate"} {
		l.system(p, []byte(`{"subtype":"task_started","task_type":"`+taskType+`","task_id":"`+taskType+`","tool_use_id":"`+taskType+`"}`))
	}
	if len(l.subagents) != 2 {
		t.Fatal("non-agent tasks became child conversations")
	}
	l.system(p, []byte(`{"subtype":"task_notification","task_id":"unknown","status":"completed"}`))
	if _, known := l.status["agent-unknown"]; known {
		t.Fatal("unknown completion created a session")
	}
}

func TestSubagentSnapshotSurvivesNextParentTurn(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	if err := l.handle(p, []byte(`{"type":"assistant","parent_tool_use_id":"spawn","message":{"id":"child-message","role":"assistant","content":[{"type":"text","text":"Working"}]}}`)); err != nil {
		t.Fatal(err)
	}
	l.system(p, []byte(`{"subtype":"task_notification","task_id":"child","status":"completed"}`))
	l.clearViewLocked(p.id)
	raw, _ := json.Marshal(l.snapshot("agent-child"))
	if !strings.Contains(string(raw), "Working") || l.status["agent-child"].Status != db.StatusDone {
		t.Fatal("next parent turn lost child inspection snapshot")
	}
}

func TestNestedWorkflowSubagentDiscovery(t *testing.T) {
	config := fixture(t)
	sub := filepath.Join(config, "projects", "-work-repo", mainID, "subagents")
	nested := filepath.Join(sub, "workflows", "run-one")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(sub, childID+".jsonl"), filepath.Join(nested, childID+".jsonl")); err != nil {
		t.Fatal(err)
	}
	idx := openSynced(t, config)
	e, err := idx.get(t.Context(), childID)
	if err != nil || e == nil {
		t.Fatalf("nested subagent missing: %+v %v", e, err)
	}
	if e.RootID != mainID || e.ParentID != mainID {
		t.Fatalf("root = %q, parent = %q, want %q", e.RootID, e.ParentID, mainID)
	}
	var watched []string
	idx.watch = func(dir string) { watched = append(watched, dir) }
	if err := idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, dir := range watched {
		found = found || dir == nested
	}
	if !found {
		t.Fatal("nested transcript directory was not watched")
	}
}
