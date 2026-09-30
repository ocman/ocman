package webhook

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestMatchesHeaderAndJSONPointerPredicates(t *testing.T) {
	e := relay.InboxEnvelope{Body: []byte(`{"event":{"kind":"push"},"items":["a","b"]}`), Request: relay.InboxRequest{Header: http.Header{"X-Event": {"PUSH"}}}}
	trueValue := true
	sub := state.WebhookSubscription{HeaderPredicatesJSON: `{"x-event":{"oneOf":["push","merge"]}}`, JSONPredicatesJSON: `{"/event/kind":{"equals":"push"},"/items/1":{"exists":true}}`}
	if ok, err := matches(e, sub); err != nil || !ok {
		t.Fatalf("matches = %v, %v", ok, err)
	}
	sub.JSONPredicatesJSON = `{"/event~1kind":{"exists":true}}`
	if ok, err := matches(e, sub); err != nil || ok {
		t.Fatalf("escaped pointer matches = %v, %v", ok, err)
	}
	if !predicateMatch([]string{"yes"}, true, predicate{Exists: &trueValue}, true) {
		t.Fatal("exists predicate did not match")
	}
}

func TestMatchesInvalidJSONIsIgnored(t *testing.T) {
	e := relay.InboxEnvelope{Body: []byte("not json")}
	if ok, err := matches(e, state.WebhookSubscription{JSONPredicatesJSON: `{"/x":{"exists":true}}`}); err != nil || ok {
		t.Fatalf("invalid JSON = %v, %v", ok, err)
	}
}

func TestDispatchRecordsEachSubscriberOutcome(t *testing.T) {
	db := dispatchFixture(t)
	ctx := t.Context()
	got, err := db.ListWebhookDeliveries(ctx, "inbox", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("deliveries = %+v, %v", got, err)
	}
	assertDispatches(t, got[0])
}

// A redelivery runs every matching subscriber again under a new delivery,
// leaving the original's log entry as it was.
func TestRedeliverRunsTheDeliveryAgain(t *testing.T) {
	db := dispatchFixture(t)
	first, _ := db.ListRoutineRuns(t.Context(), "runs")
	if _, err := db.FinishRoutineRun(t.Context(), first[0].ID, "success", "", "", 8, 0, false, false); err != nil {
		t.Fatal(err)
	}
	newID, err := Redeliver(db, &sessionDispatcher{db: db}, "inbox", "d1", time.UnixMilli(9))
	if err != nil || newID == "d1" {
		t.Fatalf("redeliver = %q, %v", newID, err)
	}
	got, err := db.ListWebhookDeliveries(t.Context(), "inbox", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("deliveries = %+v, %v", got, err)
	}
	for _, delivery := range got {
		assertDispatches(t, delivery)
	}
	if runs, _ := db.ListRoutineRuns(t.Context(), "runs"); len(runs) != 2 {
		t.Fatalf("routine did not run again: %+v", runs)
	}
	if _, err := Redeliver(db, &sessionDispatcher{db: db}, "inbox", "missing", time.UnixMilli(10)); err == nil {
		t.Fatal("unknown delivery redelivered")
	}
}

func dispatchFixture(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	for _, r := range []state.Routine{
		{ID: "bad", Enabled: true}, {ID: "runs", Enabled: true}, {ID: "filtered", Enabled: true}, {ID: "off"}, {ID: "deleted", Enabled: true, Deleted: true, DeletedAt: 1},
	} {
		r.Name, r.Directory, r.RemoteID, r.ScheduleKind, r.ScheduleConfigJSON = r.ID, "/repo", "local", "none", "{}"
		if err := db.CreateRoutine(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveWebhookInbox(ctx, state.WebhookInbox{ID: "inbox", RoutineID: "inbox", RelayURL: "https://relay", Identity: "id"}); err != nil {
		t.Fatal(err)
	}
	// "bad" was stored before save-time validation existed; it must not block
	// the subscribers after it.
	subs := map[string]string{"bad": `{"/action":5}`, "runs": `{"/action":{"equals":"opened"}}`, "filtered": `{"/action":{"equals":"closed"}}`, "off": "", "deleted": ""}
	for id, pred := range subs {
		if err := db.SaveWebhookSubscription(ctx, state.WebhookSubscription{ID: id, InboxID: "inbox", RoutineID: id, JSONPredicatesJSON: pred}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AcceptWebhookDelivery(ctx, "inbox", "d1", "POST webhook", `{"action":"opened"}`, "{}", 1); err != nil {
		t.Fatal(err)
	}
	svc := &sessionDispatcher{db: db}
	e := relay.InboxEnvelope{InboxID: "inbox", DeliveryID: "d1", Body: []byte(`{"action":"opened"}`)}
	if err := Dispatch(db, svc, "inbox", "d1", e, time.UnixMilli(5)); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertDispatches(t *testing.T, delivery state.WebhookDelivery) {
	t.Helper()
	got := []state.WebhookDelivery{delivery}
	want := []state.WebhookDispatchResult{{RoutineID: "bad", State: "ignored", Error: "invalid predicates"}, {RoutineID: "filtered", State: "ignored"}, {RoutineID: "off", State: "ignored", Error: "routine disabled"}, {RoutineID: "runs", State: "terminal", Platform: "opencode", SessionID: "ses-1"}}
	if len(got[0].Dispatches) != len(want) {
		t.Fatalf("dispatches = %+v", got[0].Dispatches)
	}
	for i := range want {
		if got[0].Dispatches[i] != want[i] {
			t.Fatalf("dispatch %d = %+v, want %+v", i, got[0].Dispatches[i], want[i])
		}
	}
}

// sessionDispatcher records a routine run linked to a session, like the real
// routine service, so the delivery log can join it back.
type sessionDispatcher struct{ db *state.DB }

func (d *sessionDispatcher) RunWebhook(ctx context.Context, routineID, _ string, occurrence int64) (state.RoutineRun, error) {
	run, _, err := d.db.ClaimRoutineRun(ctx, state.RoutineRun{ID: fmt.Sprintf("run-%s-%d", routineID, occurrence), RoutineID: routineID, RoutineUpdatedAt: 1, RoutineName: routineID, Prompt: "p", Directory: "/repo", RemoteID: "local", SessionMode: "new", Trigger: "webhook", State: "running", OccurrenceAt: occurrence, CreatedAt: 1})
	if err != nil {
		return run, err
	}
	return run, d.db.LinkRoutineRun(ctx, run.ID, "opencode", "ses-1", 2, false)
}
