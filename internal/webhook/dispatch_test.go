package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// The routine prompt carries the request query, so a replay must too.
func TestRedeliverKeepsTheQuery(t *testing.T) {
	db := dispatchFixture(t)
	if _, err := db.AcceptWebhookDelivery(t.Context(), "inbox", "q1", "POST webhook", `{"action":"closed"}`, "{}", `{"ref":["main"]}`, 1); err != nil {
		t.Fatal(err)
	}
	svc := &sessionDispatcher{db: db}
	if _, err := Redeliver(db, svc, "inbox", "q1", time.UnixMilli(9)); err != nil {
		t.Fatal(err)
	}
	if len(svc.payloads) == 0 || !strings.Contains(svc.payloads[0], `"query":{"ref":["main"]}`) {
		t.Fatalf("replayed payloads = %q", svc.payloads)
	}
}

// The prompt references the body by file instead of inlining it, so a session
// can read just the fields it needs.
func TestDispatchReferencesBodyFile(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	if err := db.CreateRoutine(ctx, state.Routine{ID: "r", Name: "r", Directory: "/repo", RemoteID: "local", ScheduleKind: "none", ScheduleConfigJSON: "{}", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveWebhookInbox(ctx, state.WebhookInbox{ID: "inbox", RoutineID: "inbox", RelayURL: "https://relay", Identity: "id"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveWebhookSubscription(ctx, state.WebhookSubscription{ID: "s", InboxID: "inbox", RoutineID: "r"}); err != nil {
		t.Fatal(err)
	}
	body := `{"action":"opened","number":7}`
	svc := &sessionDispatcher{db: db}
	if err := Dispatch(db, svc, "inbox", "../d1", relay.InboxEnvelope{InboxID: "inbox", DeliveryID: "../d1", Body: []byte(body), Request: relay.InboxRequest{Header: http.Header{"x-forgejo-event": {"pull_request"}}}}, time.UnixMilli(5)); err != nil {
		t.Fatal(err)
	}
	if len(svc.payloads) != 1 || strings.Contains(svc.payloads[0], "bodyBase64") || strings.Contains(svc.payloads[0], "pull_request") {
		t.Fatalf("payloads = %q", svc.payloads)
	}
	raw := strings.TrimSuffix(strings.SplitN(svc.payloads[0], "```json\n", 2)[1], "\n```")
	var got DispatchPayload
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(got.BodyPath)
	if err != nil || string(data) != body || got.BodyBytes != len(body) {
		t.Fatalf("body file %q = %q, %v (bytes %d)", got.BodyPath, data, err, got.BodyBytes)
	}
	headers, err := os.ReadFile(got.HeadersPath)
	if err != nil || string(headers) != `{"X-Forgejo-Event":["pull_request"]}` {
		t.Fatalf("headers file %q = %q, %v", got.HeadersPath, headers, err)
	}
	if !filepath.IsAbs(got.BodyPath) || strings.Contains(filepath.Base(got.BodyPath), "d1") {
		t.Fatalf("body path %q is not an absolute hashed name", got.BodyPath)
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
		{ID: "bad", Enabled: true}, {ID: "runs", Enabled: true}, {ID: "filtered", Enabled: true}, {ID: "off"}, {ID: "deleted", Enabled: true, Deleted: true, DeletedAt: 1}, {ID: "remote", Enabled: true, RemoteID: "r1"},
	} {
		r.Name, r.Directory, r.ScheduleKind, r.ScheduleConfigJSON = r.ID, "/repo", "none", "{}"
		if r.RemoteID == "" {
			r.RemoteID = "local"
		}
		if err := db.CreateRoutine(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveWebhookInbox(ctx, state.WebhookInbox{ID: "inbox", RoutineID: "inbox", RelayURL: "https://relay", Identity: "id"}); err != nil {
		t.Fatal(err)
	}
	// "bad" was stored before save-time validation existed; it must not block
	// the subscribers after it.
	subs := map[string]string{"bad": `{"/action":5}`, "runs": `{"/action":{"equals":"opened"}}`, "filtered": `{"/action":{"equals":"closed"}}`, "off": "", "deleted": "", "remote": ""}
	for id, pred := range subs {
		if err := db.SaveWebhookSubscription(ctx, state.WebhookSubscription{ID: id, InboxID: "inbox", RoutineID: id, JSONPredicatesJSON: pred}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AcceptWebhookDelivery(ctx, "inbox", "d1", "POST webhook", `{"action":"opened"}`, "{}", "", 1); err != nil {
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
	want := []state.WebhookDispatchResult{{RoutineID: "bad", State: "ignored", Error: "invalid predicates"}, {RoutineID: "filtered", State: "ignored"}, {RoutineID: "off", State: "ignored", Error: "routine disabled"}, {RoutineID: "remote", State: "ignored", Error: "routine runs on another machine"}, {RoutineID: "runs", State: "terminal", Platform: "opencode", SessionID: "ses-1"}}
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
type sessionDispatcher struct {
	db       *state.DB
	payloads []string
}

func (d *sessionDispatcher) RunWebhook(ctx context.Context, routineID, payload string, occurrence int64) (state.RoutineRun, error) {
	d.payloads = append(d.payloads, payload)
	run, _, err := d.db.ClaimRoutineRun(ctx, state.RoutineRun{ID: fmt.Sprintf("run-%s-%d", routineID, occurrence), RoutineID: routineID, RoutineUpdatedAt: 1, RoutineName: routineID, Prompt: "p", Directory: "/repo", RemoteID: "local", SessionMode: "new", Trigger: "webhook", State: "running", OccurrenceAt: occurrence, CreatedAt: 1})
	if err != nil {
		return run, err
	}
	return run, d.db.LinkRoutineRun(ctx, run.ID, "opencode", "ses-1", 2, false)
}
