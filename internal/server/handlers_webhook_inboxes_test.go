package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func fakeInboxRelay(t *testing.T) *httptest.Server {
	t.Helper()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/inboxes":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"inbox","ingestionUrl":"/i/inbox/token","managementToken":"manage","fetchToken":"fetch","acknowledgmentToken":"ack","keyVersion":1,"secretHeader":"Authorization"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/inboxes/inbox/rotate":
			_, _ = w.Write([]byte(`{"keyVersion":2}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/inboxes/inbox":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && r.URL.Path == "/inboxes/inbox/secret":
			var body struct{ Secret, SecretHeader string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Secret != "" && body.SecretHeader == "" {
				body.SecretHeader = "X-Webhook-Secret"
			}
			if body.Secret == "" {
				body.SecretHeader = ""
			}
			_, _ = w.Write([]byte(`{"secretHeader":"` + body.SecretHeader + `"}`))
		default:
			http.Error(w, "relay failed", http.StatusBadGateway)
		}
	}))
	t.Cleanup(relay.Close)
	return relay
}

func createTestRoutine(t *testing.T, handler http.Handler) state.Routine {
	t.Helper()
	created := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", validRoutineBody)
	var routine state.Routine
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &routine) != nil {
		t.Fatalf("create routine: %d %s", created.Code, created.Body.String())
	}
	return routine
}

func TestWebhookInboxesLifecycle(t *testing.T) {
	srv, handler, _ := routineHTTPServer(t)
	srv.relayURL = fakeInboxRelay(t).URL
	routine := createTestRoutine(t, handler)
	const base = "/api/webhook-inboxes"

	if rec := doRoutineRequest(t, handler, http.MethodGet, base, ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodPost, base, `{"name":"  "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: %d", rec.Code)
	}
	rec := doRoutineRequest(t, handler, http.MethodPost, base, `{"name":"forgejo","enrollmentToken":"enroll","secret":"Bearer s","secretHeader":"Authorization"}`)
	var view webhookInboxView
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Name != "forgejo" || view.SecretHeader != "Authorization" || len(view.Subscriptions) != 0 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	stored, err := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox")
	if err != nil || stored.RoutineID != "inbox" {
		t.Fatalf("standalone inbox = %+v, %v", stored, err)
	}
	for _, path := range []string{base, base + "/inbox"} {
		rec := doRoutineRequest(t, handler, http.MethodGet, path, "")
		for _, secret := range []string{"manage", "fetch", "ack", "AGE-SECRET"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("%s leaked %q: %s", path, secret, rec.Body.String())
			}
		}
	}

	for _, tc := range []struct {
		body, name, header string
		code               int
	}{
		{`{"name":" "}`, "", "", http.StatusBadRequest},
		{`{"name":" renamed "}`, "renamed", "Authorization", http.StatusOK},
		{`{"secret":"s2"}`, "renamed", "X-Webhook-Secret", http.StatusOK},
		{`{"secret":"Bearer t","secretHeader":" Authorization "}`, "renamed", "Authorization", http.StatusOK},
		{`{"secret":""}`, "renamed", "", http.StatusOK},
	} {
		rec := doRoutineRequest(t, handler, http.MethodPatch, base+"/inbox", tc.body)
		if rec.Code != tc.code {
			t.Fatalf("patch %s: %d %s", tc.body, rec.Code, rec.Body.String())
		}
		if tc.code != http.StatusOK {
			continue
		}
		got, _ := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox")
		if got.Name != tc.name || got.SecretHeader != tc.header || got.IngestionURL != "/i/inbox/token" {
			t.Fatalf("patch %s stored %+v", tc.body, got)
		}
	}
	subs := base + "/inbox/subscriptions"
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"routineId":"missing"}`, http.StatusNotFound},
		{`{"routineId":"` + routine.ID + `","headerPredicates":"[1]"}`, http.StatusBadRequest},
		{`{"`, http.StatusBadRequest},
		{`{"routineId":"` + routine.ID + `","headerPredicates":"{\"x-forgejo-event\":{\"equals\":\"pull_request\"}}"}`, http.StatusOK},
	} {
		if rec := doRoutineRequest(t, handler, http.MethodPut, subs, tc.body); rec.Code != tc.code {
			t.Fatalf("put %s: %d %s", tc.body, rec.Code, rec.Body.String())
		}
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, subs, ""); !strings.Contains(rec.Body.String(), `"routineId":"`+routine.ID+`"`) {
		t.Fatalf("list subs: %s", rec.Body.String())
	}
	if got, _ := srv.stateDB.ListRoutineWebhookSubscriptions(t.Context(), routine.ID); len(got) != 1 || got[0].InboxID != "inbox" {
		t.Fatalf("routine subs = %+v", got)
	}

	// Delivery log reports each routine's outcome.
	db := srv.stateDB
	if _, err := db.AcceptWebhookDelivery(t.Context(), "inbox", "d1", "POST webhook", `{"action":"opened"}`, `{"X-Forgejo-Event":["pull_request"]}`, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimWebhookDispatch(t.Context(), "inbox", "d1", routine.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishWebhookDispatch(t.Context(), "inbox", "d1", routine.ID, "terminal", "", 3); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordWebhookDeliveryError(t.Context(), "inbox", "d0", "decrypt failed", time.Now()); err != nil {
		t.Fatal(err)
	}
	rec = doRoutineRequest(t, handler, http.MethodGet, base+"/inbox/deliveries", "")
	var deliveries []state.WebhookDelivery
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &deliveries) != nil || len(deliveries) != 2 {
		t.Fatalf("deliveries: %d %s", rec.Code, rec.Body.String())
	}
	byID := map[string]state.WebhookDelivery{deliveries[0].DeliveryID: deliveries[0], deliveries[1].DeliveryID: deliveries[1]}
	if d := byID["d1"]; d.Body != `{"action":"opened"}` || len(d.Dispatches) != 1 || d.Dispatches[0].RoutineID != routine.ID || d.Dispatches[0].State != "terminal" {
		t.Fatalf("d1 = %+v", d)
	}
	if d := byID["d0"]; d.LastError != "decrypt failed" || len(d.Dispatches) != 0 {
		t.Fatalf("d0 = %+v", d)
	}

	if rec := doRoutineRequest(t, handler, http.MethodDelete, subs, `{"routineId":"`+routine.ID+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete sub: %d", rec.Code)
	}
	if rec := doRoutineRequest(t, handler, http.MethodPut, base+"/inbox", `{"reset":true}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"keyVersion":2`) {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := db.GetWebhookInboxByID(t.Context(), "inbox"); got.Name != "renamed" || got.KeyVersion != 2 || got.Identity == stored.Identity {
		t.Fatalf("rotated inbox = %+v", got)
	}
	if rec := doRoutineRequest(t, handler, http.MethodPut, subs, `{"routineId":"`+routine.ID+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("resubscribe: %d", rec.Code)
	}
	if rec := doRoutineRequest(t, handler, http.MethodDelete, base+"/inbox", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := db.ListRoutineWebhookSubscriptions(t.Context(), routine.ID); len(got) != 0 {
		t.Fatalf("subscriptions survived revoke: %+v", got)
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, base+"/inbox", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("after revoke: %d", rec.Code)
	}
}

func TestWebhookInboxesFailures(t *testing.T) {
	srv, handler, _ := routineHTTPServer(t)
	const base = "/api/webhook-inboxes"
	if rec := doRoutineRequest(t, handler, http.MethodPost, base, `{"name":"x"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no relay: %d", rec.Code)
	}
	relay := fakeInboxRelay(t)
	srv.relayURL = relay.URL + "/broken"
	if rec := doRoutineRequest(t, handler, http.MethodPost, base, `{"name":"x"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("relay failure: %d", rec.Code)
	}
	if err := srv.stateDB.SaveWebhookInbox(t.Context(), state.WebhookInbox{ID: "other", RoutineID: "other", RelayURL: relay.URL, Identity: "identity"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{http.MethodPatch, base, "", http.StatusMethodNotAllowed},
		{http.MethodPost, base, `{"`, http.StatusBadRequest},
		{http.MethodGet, base + "/missing", "", http.StatusNotFound},
		{http.MethodGet, base + "/other/nope", "", http.StatusNotFound},
		{http.MethodGet, base + "/other/deliveries/x", "", http.StatusNotFound},
		{http.MethodPost, base + "/other/deliveries", "", http.StatusMethodNotAllowed},
		{http.MethodPost, base + "/other", "", http.StatusMethodNotAllowed},
		{http.MethodPatch, base + "/other/subscriptions", "", http.StatusMethodNotAllowed},
		{http.MethodDelete, base + "/other/subscriptions", `{"`, http.StatusBadRequest},
		{http.MethodPut, base + "/other/subscriptions", `{"routineId":"other","headerPredicates":"{\"X-Event\":5}"}`, http.StatusBadRequest},
		{http.MethodPut, base + "/other/subscriptions", `{"routineId":"other","headerPredicates":"{\"X-Event\":{\"exists\":\"yes\"}}"}`, http.StatusBadRequest},
		{http.MethodPut, base + "/other/subscriptions", `{"routineId":"other","jsonPredicates":"{\"/a\":null}"}`, http.StatusBadRequest},
		{http.MethodPut, base + "/other", `{"`, http.StatusBadRequest},
		{http.MethodPut, base + "/other", `{"recipient":"r"}`, http.StatusInternalServerError},
		{http.MethodDelete, base + "/other", "", http.StatusInternalServerError},
		{http.MethodPatch, base + "/other", `{"`, http.StatusBadRequest},
		{http.MethodPatch, base + "/other", `{"secret":"x"}`, http.StatusBadGateway},
	} {
		if rec := doRoutineRequest(t, handler, tc.method, tc.path, tc.body); rec.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, base, nil)
	req.RemoteAddr = "10.0.0.1:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("non-local request allowed")
	}
	rec = httptest.NewRecorder()
	(&Server{}).handleWebhookInboxes(rec, httptest.NewRequest(http.MethodGet, base, nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no state: %d", rec.Code)
	}
}

func TestWebhookInboxPatchKeepsConcurrentKeyAndRevocation(t *testing.T) {
	srv, handler, _ := routineHTTPServer(t)
	srv.relayURL = fakeInboxRelay(t).URL
	if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/webhook-inboxes", `{"name":"forgejo"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}
	stale, err := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox")
	if err != nil {
		t.Fatal(err)
	}
	// A key reset lands while a rename still holds the pre-reset snapshot.
	if rec := doRoutineRequest(t, handler, http.MethodPut, "/api/webhook-inboxes/inbox", `{"reset":true}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	rotated, _ := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox")
	rec := httptest.NewRecorder()
	srv.updateWebhookInbox(rec, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"renamed","secret":"s"}`)), stale)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox")
	if got.Identity != rotated.Identity || got.KeyVersion != rotated.KeyVersion || got.Identity == stale.Identity || got.Name != "renamed" || got.SecretHeader != "X-Webhook-Secret" {
		t.Fatalf("patch clobbered the rotated key: stale v%d, rotated v%d, got %+v", stale.KeyVersion, rotated.KeyVersion, got)
	}
	var view webhookInboxView
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.KeyVersion != rotated.KeyVersion {
		t.Fatalf("view = %s", rec.Body.String())
	}

	// A rename that races a revoke must not bring the inbox back.
	if err := srv.stateDB.DeleteWebhookInboxByID(t.Context(), "inbox"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.updateWebhookInbox(rec, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"name":"ghost"}`)), stale)
	if _, err := srv.stateDB.GetWebhookInboxByID(t.Context(), "inbox"); err == nil {
		t.Fatal("patch resurrected a revoked inbox")
	}
}
