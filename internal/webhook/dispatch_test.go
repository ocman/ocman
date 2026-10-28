package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/relay"
	"github.com/ocman/ocman/internal/state"
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
	if !headerMatch([]string{"yes"}, predicate{Exists: &trueValue}) {
		t.Fatal("exists predicate did not match")
	}
}

// RFC 6901: an array index is "0" or a decimal without leading zeros, and
// "~1" is unescaped before "~0". fmt.Sscan used to accept "01", "+1", "0x1",
// "1abc", "1_0" and " 1".
func TestPointerValueFollowsRFC6901(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"items":["a","b","c","d","e","f","g","h","i","j","k"],"a/b":1,"m~n":2,"m~2n":6,"m~":7,"~1":3,"":4,"x":{"":5}}`), &doc); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		pointer string
		want    any
		ok      bool
	}{
		{"/items/0", "a", true},
		{"/items/1", "b", true},
		{"/items/10", "k", true},
		{"/items/01", nil, false},
		{"/items/00", nil, false},
		{"/items/+1", nil, false},
		{"/items/-1", nil, false},
		{"/items/-", nil, false},
		{"/items/0x1", nil, false},
		{"/items/1abc", nil, false},
		{"/items/1_0", nil, false},
		{"/items/ 1", nil, false},
		{"/items/1 ", nil, false},
		{"/items/", nil, false},
		{"/items/11", nil, false},
		{"/items/99999999999999999999", nil, false},
		{"/a~1b", float64(1), true},
		{"/m~0n", float64(2), true},
		{"/~01", float64(3), true},
		{"/m~2n", nil, false},
		{"/m~", nil, false},
		{"/", float64(4), true},
		{"/x/", float64(5), true},
		{"items", nil, false},
	}
	for _, test := range tests {
		got, ok := pointerValue(doc, test.pointer)
		if ok != test.ok || (ok && got != test.want) {
			t.Errorf("pointerValue(%q) = %v, %v; want %v, %v", test.pointer, got, ok, test.want, test.ok)
		}
	}
}

// The routine form stores every typed value as a JSON string, so a string
// still matches the JSON text of a scalar ("5" ~ 5, "true" ~ true). Composite
// values only match a structurally equal expected value; fmt.Sprint used to
// let "[a b]" match ["a b"], "map[k:v]" match {"k":"v"} and "<nil>" match null,
// and wrote 100000000 as "1e+08", so the UI's "100000000" missed it.
func TestPredicateValueEquality(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"same string", `"push"`, `"push"`, true},
		{"different string", `"push"`, `"Push"`, false},
		{"ui string matches number", `5`, `"5"`, true},
		{"ui string matches number by value", `5`, `"5.0"`, true},
		{"ui string matches large number", `100000000`, `"100000000"`, true},
		{"ui string matches fraction", `1.5`, `"1.5"`, true},
		{"ui string is not a number", `1`, `"01"`, false},
		{"ui string with spaces", `1`, `" 1"`, false},
		{"ui string hex", `1`, `"0x1"`, false},
		{"ui string matches bool", `true`, `"true"`, true},
		{"ui string bool mismatch", `true`, `"false"`, false},
		{"ui string bool case", `true`, `"True"`, false},
		{"ui string matches null", `null`, `"null"`, true},
		{"go nil text no longer matches null", `null`, `"<nil>"`, false},
		{"typed bool", `false`, `false`, true},
		{"typed bool matches string body", `"false"`, `false`, true},
		{"typed number", `100000000`, `1e8`, true},
		{"typed number mismatch", `2`, `1`, false},
		{"typed null", `null`, `null`, true},
		{"typed null vs string", `""`, `null`, false},
		{"array text does not match array", `["a b"]`, `"[a b]"`, false},
		{"object text does not match object", `{"k":"v"}`, `"map[k:v]"`, false},
		{"array deep equal", `["a",1,{"k":true}]`, `["a",1,{"k":true}]`, true},
		{"array order matters", `["a","b"]`, `["b","a"]`, false},
		{"object deep equal", `{"a":1,"b":[2]}`, `{"b":[2],"a":1}`, true},
		{"object mismatch", `{"a":1}`, `{"a":"1"}`, false},
		{"array vs string", `"a"`, `["a"]`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := decodeJSON([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []predicate{
				{Equals: json.RawMessage(test.want)},
				{Op: "equals", Value: json.RawMessage(test.want)},
				{OneOf: []json.RawMessage{json.RawMessage(`"unrelated"`), json.RawMessage(test.want)}},
			} {
				if got := valueMatch(value, p); got != test.ok {
					t.Fatalf("valueMatch(%s, %+v) = %v, want %v", test.body, p, got, test.ok)
				}
			}
		})
	}
}

// Numbers compare by exact decimal value. Decoding through float64 rounded
// 9007199254740993 to ...992 and 0.1000000000000000000001 to 0.1, so distinct
// values matched, and 1e400 (beyond float64) made the whole body unreadable.
func TestPredicateNumbersCompareExactly(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
		ok   bool
	}{
		{"adjacent large integers", `9007199254740992`, `9007199254740993`, false},
		{"adjacent large integers as text", `9007199254740992`, `"9007199254740993"`, false},
		{"large integer as text", `9007199254740993`, `"9007199254740993"`, true},
		{"high precision decimal", `0.1000000000000000000001`, `0.1`, false},
		{"high precision decimal as text", `0.1000000000000000000001`, `"0.1"`, false},
		{"high precision decimal equal", `0.1000000000000000000001`, `"0.1000000000000000000001"`, true},
		{"same value other spelling", `5`, `5.0`, true},
		{"exponent spelling", `5`, `5e0`, true},
		{"negative exponent spelling", `5`, `"50e-1"`, true},
		{"negative zero", `-0`, `0`, true},
		{"sign matters", `-5`, `5`, false},
		{"beyond float64", `1e400`, `"1e400"`, true},
		{"beyond float64 mismatch", `1e400`, `1e401`, false},
		{"huge exponent", `1e1000000000`, `"10e999999999"`, true},
		{"array of large integers", `[9007199254740992]`, `[9007199254740993]`, false},
		{"array numbers by value", `[5]`, `[5.0]`, true},
		{"object numbers by value", `{"n":9007199254740993}`, `{"n":9007199254740993}`, true},
		{"object large integer mismatch", `{"n":9007199254740992}`, `{"n":9007199254740993}`, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := relay.InboxEnvelope{Body: []byte(`{"v":` + test.body + `}`)}
			sub := state.WebhookSubscription{JSONPredicatesJSON: `{"/v":{"equals":` + test.want + `}}`}
			if ok, err := matches(e, sub); err != nil || ok != test.ok {
				t.Fatalf("matches(%s, %s) = %v, %v, want %v", test.body, test.want, ok, err, test.ok)
			}
		})
	}
}

func TestDecodeJSONRejectsTrailingData(t *testing.T) {
	for _, raw := range []string{`{"v":1} x`, `1 2`, ``} {
		if _, err := decodeJSON([]byte(raw)); err == nil {
			t.Errorf("decodeJSON(%q) accepted", raw)
		}
	}
	if v, err := decodeJSON([]byte(" 12345678901234567890 \n")); err != nil || v != json.Number("12345678901234567890") {
		t.Fatalf("decodeJSON = %#v, %v", v, err)
	}
}

func TestHeaderPredicateNumbersCompareExactly(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"9007199254740992", `9007199254740993`, false},
		{"9007199254740993", `9007199254740993`, true},
		{"0.1", `0.1000000000000000000001`, false},
		{"5.0", `5`, true},
		{"5", `"5"`, true},
		{"05", `5`, false},
	}
	for _, test := range tests {
		e := relay.InboxEnvelope{Body: []byte(`{}`), Request: relay.InboxRequest{Header: http.Header{"X-N": {test.header}}}}
		sub := state.WebhookSubscription{HeaderPredicatesJSON: `{"x-n":{"equals":` + test.want + `}}`}
		if ok, err := matches(e, sub); err != nil || ok != test.ok {
			t.Errorf("header %q vs %s = %v, %v, want %v", test.header, test.want, ok, err, test.ok)
		}
	}
}

func TestHeaderPredicateValueText(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"PUSH", `"push"`, true},
		{"5", `5`, true},
		{"100000000", `100000000`, true},
		{"true", `true`, true},
		{"[a]", `["a"]`, false},
		{"map[k:v]", `{"k":"v"}`, false},
		{"<nil>", `null`, false},
	}
	for _, test := range tests {
		for _, p := range []predicate{{Equals: json.RawMessage(test.want)}, {OneOf: []json.RawMessage{json.RawMessage(test.want)}}} {
			if got := headerMatch([]string{test.header}, p); got != test.ok {
				t.Errorf("headerMatch(%q, %s) = %v, want %v", test.header, test.want, got, test.ok)
			}
		}
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
	newID, err := Redeliver(t.Context(), db, &sessionDispatcher{db: db}, "inbox", "d1", time.UnixMilli(9))
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
	if _, err := Redeliver(t.Context(), db, &sessionDispatcher{db: db}, "inbox", "missing", time.UnixMilli(10)); err == nil {
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
	if _, err := Redeliver(t.Context(), db, svc, "inbox", "q1", time.UnixMilli(9)); err != nil {
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
	if err := Dispatch(t.Context(), db, svc, "inbox", "../d1", relay.InboxEnvelope{InboxID: "inbox", DeliveryID: "../d1", Body: []byte(body), Request: relay.InboxRequest{Header: http.Header{"x-forgejo-event": {"pull_request"}}}}, time.UnixMilli(5)); err != nil {
		t.Fatal(err)
	}
	if len(svc.labels) != 1 || svc.labels[0] != "#7 opened" {
		t.Fatalf("labels = %q", svc.labels)
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
	if err := Dispatch(t.Context(), db, svc, "inbox", "d1", e, time.UnixMilli(5)); err != nil {
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
	labels   []string
}

func (d *sessionDispatcher) RunWebhook(ctx context.Context, routineID, payload, label string, occurrence int64) (state.RoutineRun, error) {
	d.payloads = append(d.payloads, payload)
	d.labels = append(d.labels, label)
	run, _, err := d.db.ClaimRoutineRun(ctx, state.RoutineRun{ID: fmt.Sprintf("run-%s-%d", routineID, occurrence), RoutineID: routineID, RoutineUpdatedAt: 1, RoutineName: routineID, Prompt: "p", Directory: "/repo", RemoteID: "local", SessionMode: "new", Trigger: "webhook", State: "running", OccurrenceAt: occurrence, CreatedAt: 1})
	if err != nil {
		return run, err
	}
	return run, d.db.LinkRoutineRun(ctx, run.ID, "opencode", "ses-1", 2, false)
}

// cancellingDispatcher cancels the caller's context mid-run, like a client
// that disconnected during a redelivery.
type cancellingDispatcher struct {
	cancel context.CancelFunc
	got    context.Context
}

func (d *cancellingDispatcher) RunWebhook(ctx context.Context, _, _, _ string, _ int64) (state.RoutineRun, error) {
	d.got = ctx
	d.cancel()
	return state.RoutineRun{}, ctx.Err()
}

// The caller's context reaches the routine run, and a claimed dispatch
// still records its outcome after that context is cancelled.
func TestDispatchPropagatesCallerContext(t *testing.T) {
	db := dispatchFixture(t)
	if _, err := db.AcceptWebhookDelivery(t.Context(), "inbox", "d2", "POST webhook", `{"action":"opened"}`, "{}", "", 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	svc := &cancellingDispatcher{cancel: cancel}
	e := relay.InboxEnvelope{InboxID: "inbox", DeliveryID: "d2", Body: []byte(`{"action":"opened"}`)}
	if err := Dispatch(ctx, db, svc, "inbox", "d2", e, time.UnixMilli(6)); err != nil {
		t.Fatal(err)
	}
	if svc.got == nil || svc.got.Err() == nil {
		t.Fatal("routine run did not receive the caller's context")
	}
	got, err := db.ListWebhookDeliveries(t.Context(), "inbox", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range got {
		if delivery.DeliveryID != "d2" {
			continue
		}
		for _, d := range delivery.Dispatches {
			if d.RoutineID == "runs" && (d.State != "failure" || !strings.Contains(d.Error, "canceled")) {
				t.Fatalf("claimed dispatch = %+v, want a recorded failure", d)
			}
		}
	}

	// An already-cancelled caller claims nothing.
	if err := Dispatch(ctx, db, &sessionDispatcher{db: db}, "inbox", "d3", e, time.UnixMilli(7)); err == nil {
		t.Fatal("dispatch with a cancelled context succeeded")
	}
}

// headerMatch matches present header values the way matches does.
func headerMatch(values []string, p predicate) bool {
	p.Op = ""
	return predicateMatch(p, true, func(want any) bool {
		return slices.ContainsFunc(values, func(v string) bool { return headerEquals(v, want, true) })
	})
}

// valueMatch matches a present JSON body value the way matches does.
func valueMatch(value any, p predicate) bool {
	return predicateMatch(p, true, func(want any) bool { return jsonEqual(value, want) })
}

func TestMatchesPresenceRules(t *testing.T) {
	e := relay.InboxEnvelope{Body: []byte(`{"a":1}`), Request: relay.InboxRequest{Header: http.Header{"X-A": {"1"}}}}
	for _, test := range []struct {
		headers, pointers string
		ok                bool
	}{
		{`{"x-missing":{"exists":false}}`, ``, true},
		{`{"x-a":{"exists":false}}`, ``, false},
		{`{"x-missing":{"equals":"1"}}`, ``, false},
		{``, `{"/missing":{"exists":false}}`, true},
		{``, `{"/missing":{"equals":1}}`, false},
		{``, `{"/a":{"exists":true,"op":"equals","value":1}}`, true},
	} {
		sub := state.WebhookSubscription{HeaderPredicatesJSON: test.headers, JSONPredicatesJSON: test.pointers}
		if ok, err := matches(e, sub); err != nil || ok != test.ok {
			t.Errorf("matches(%s %s) = %v, %v, want %v", test.headers, test.pointers, ok, err, test.ok)
		}
	}
}

// The label names the delivery's item in the run title. The body is untrusted,
// so only a positive number and a short lowercase action are used.
func TestDeliveryLabel(t *testing.T) {
	for body, want := range map[string]string{
		`{"action":"synchronized","number":1181}`: "#1181 synchronized",
		`{"number":12}`:                                           "#12",
		`{"action":"opened"}`:                                     "",
		`{"action":"opened","number":0}`:                          "",
		`{"action":"opened","number":1.5}`:                        "",
		`{"action":"opened","number":"7"}`:                        "",
		`{"action":"Opened; rm -rf","number":3}`:                  "#3",
		`{"action":"` + strings.Repeat("a", 40) + `","number":3}`: "#3",
		`[1,2]`:    "",
		`not json`: "",
	} {
		if got := deliveryLabel([]byte(body)); got != want {
			t.Errorf("deliveryLabel(%s) = %q, want %q", body, got, want)
		}
	}
}
