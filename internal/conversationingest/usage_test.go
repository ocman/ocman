package conversationingest

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func accountedSnapshot() *db.HistorySnapshot {
	snapshot := historySnapshot("accounted")
	snapshot.Session.ParentID = "parent"
	snapshot.Session.ProjectID = "project"
	snapshot.Messages = []db.Message{
		{ID: "message", SessionID: "session", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant","agent":"build","providerID":"anthropic","modelID":"claude","cost":0.25,"finish":"stop","time":{"completed":2},"tokens":{"input":10,"output":4,"reasoning":1,"cache":{"read":2,"write":3}}}`)},
		{ID: "failed", SessionID: "session", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant","providerID":"openai","modelID":"gpt","cost":0.5,"finish":"error","error":{"name":"Boom"},"tokens":{"input":7,"output":3}}`)},
	}
	snapshot.Parts = []db.Part{{ID: "part", SessionID: "session", MessageID: "message", Data: json.RawMessage(`{"type":"text","text":"accounted"}`)}}
	return snapshot
}

func TestNormalizeMapsAnalyticsFields(t *testing.T) {
	batch, _, err := Normalize(statev2.Key{InstanceID: "i", SessionID: "session"}, accountedSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if batch.Session.ParentID != "parent" || batch.Session.ProjectID != "project" {
		t.Fatalf("session identity: %+v", batch.Session)
	}
	first, failed := batch.Messages[0], batch.Messages[1]
	if first.Agent != "build" || first.Provider != "anthropic" || first.Model != "anthropic/claude" || first.Cost != 0.25 || first.Finish != "stop" ||
		first.TokensInput != 10 || first.TokensOutput != 4 || first.TokensReasoning != 1 || first.TokensCacheRead != 2 || first.TokensCacheWrite != 3 ||
		first.HasError || first.ErrorName != "" {
		t.Fatalf("successful message: %+v", first)
	}
	if !failed.HasError || failed.ErrorName != "Boom" || failed.Finish != "error" || failed.Cost != 0.5 || failed.TokensCacheRead != 0 {
		t.Fatalf("failed message: %+v", failed)
	}
	if got := expectedUsage(batch); got.Messages != 2 || got.Cost != 0.75 || got.TokensInput != 17 || got.TokensOutput != 7 {
		t.Fatalf("expected usage: %+v", got)
	}
}

func TestNullErrorIsNotAFailure(t *testing.T) {
	snapshot := historySnapshot("ok")
	snapshot.Messages[0].Data = json.RawMessage(`{"role":"assistant","error":null}`)
	batch, _, err := Normalize(statev2.Key{InstanceID: "i", SessionID: "session"}, snapshot)
	if err != nil || batch.Messages[0].HasError {
		t.Fatalf("null error counted as failure: %+v %v", batch.Messages, err)
	}
}

func TestSameUsageToleratesOnlySummationOrder(t *testing.T) {
	want := statev2.Usage{Messages: 2, Cost: 0.3, TokensInput: 5}
	if !sameUsage(want, statev2.Usage{Messages: 2, Cost: 0.1 + 0.2, TokensInput: 5}) {
		t.Fatal("summation order rejected")
	}
	for name, got := range map[string]statev2.Usage{
		"message count": {Messages: 1, Cost: 0.3, TokensInput: 5},
		"cost":          {Messages: 2, Cost: 0.31, TokensInput: 5},
		"tokens":        {Messages: 2, Cost: 0.3, TokensInput: 6},
		"cache":         {Messages: 2, Cost: 0.3, TokensInput: 5, TokensCacheWrite: 1},
	} {
		if sameUsage(want, got) {
			t.Fatalf("%s mismatch accepted", name)
		}
	}
}

func TestVerificationMatchesStoredAccounting(t *testing.T) {
	f := fakeSource("first")
	f.snapshots["session"] = accountedSnapshot()
	f.snapshots["session"].Version = "v1"
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.verifiedAt = w.verifiedAt.Add(-24 * time.Hour)
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	tasks := listTasks(t, manager, tasksvc.Succeeded, verifyTaskKind)
	if len(tasks) != 1 {
		t.Fatalf("verification did not run: %+v", tasks)
	}
	if got := tasks[0].Progress.Counters; got["usage-mismatch"] != 0 || got["matched"] != 1 {
		t.Fatalf("verification counters: %+v", got)
	}
}

func TestUsageMatchesReportsStoreErrors(t *testing.T) {
	m, _ := testImporter(t, fakeSource("one"))
	if err := m.deps.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.workers[0].usageMatches(t.Context(), "session", statev2.Usage{}); err == nil || ok {
		t.Fatalf("closed store: ok=%v err=%v", ok, err)
	}
}

func TestUniqueSorted(t *testing.T) {
	if got := uniqueSorted([]string{"b", "a", "b", "c", "a"}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("uniqueSorted = %v", got)
	}
	if got := uniqueSorted(nil); got == nil || len(got) != 0 {
		t.Fatalf("uniqueSorted(nil) = %#v, want empty non-nil", got)
	}
}
