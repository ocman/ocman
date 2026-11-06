package webhook

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/share"
	"github.com/ocman/ocman/internal/state"
)

type launchDispatcher struct{ legacy, project string }

func (d *launchDispatcher) RunWebhook(_ context.Context, id, _, _ string, _ int64) (state.RoutineRun, error) {
	d.legacy = id
	return state.RoutineRun{}, nil
}

func TestProjectSubscriptionPersistsAndDispatches(t *testing.T) {
	store := dispatchFixture(t)
	routine, err := store.GetRoutine(t.Context(), "runs")
	if err != nil {
		t.Fatal(err)
	}
	routine.Directory = ""
	if err := store.UpdateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: store, Routines: &fakeRoutines{}})
	handler := moduletest.Mount(m)
	for _, tc := range []struct {
		pointer string
		status  int
	}{{"bad", 400}, {"/bad~2", 400}, {"/repository/clone_url", 200}} {
		response := do(t, handler, http.MethodPut, "/api/webhook-inboxes/inbox/subscriptions", `{"routineId":"runs","projectPointer":"`+tc.pointer+`"}`)
		if response.Code != tc.status {
			t.Fatalf("save %q: %d %s", tc.pointer, response.Code, response.Body.String())
		}
	}
	subs, err := store.ListWebhookSubscriptions(t.Context(), "inbox")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sub := range subs {
		if sub.RoutineID == "runs" {
			found = sub.ProjectPointer == "/repository/clone_url"
		}
	}
	if !found {
		t.Fatalf("missing pointer: %+v", subs)
	}
	dispatcher := &launchDispatcher{}
	body := `{"action":"opened","repository":{"clone_url":"https://forge/org/repo.git"}}`
	if _, err := store.AcceptWebhookDelivery(t.Context(), "inbox", "project-delivery", "POST webhook", body, "{}", "", 2); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch(t.Context(), store, dispatcher, "inbox", "project-delivery", share.InboxEnvelope{Body: []byte(body)}, time.UnixMilli(3)); err != nil {
		t.Fatal(err)
	}
	if dispatcher.project != "https://forge/org/repo.git" {
		t.Fatalf("project = %q", dispatcher.project)
	}
}

func (d *launchDispatcher) RunWebhookInProject(_ context.Context, _, _, _ string, _ int64, project string) (state.RoutineRun, error) {
	d.project = project
	return state.RoutineRun{}, nil
}

func TestLaunchWebhookProjectField(t *testing.T) {
	for _, tc := range []struct {
		name, directory, pointer, body, project string
		failure, legacy                         bool
	}{
		{"repository", "", "/repository/clone_url", `{"repository":{"clone_url":"https://forge/org/repo.git"}}`, "https://forge/org/repo.git", false, false},
		{"directory", "", "/project", `{"project":"/repo"}`, "/repo", false, false},
		{"fixed ignores field", "/fixed", "/project", `not json`, "", false, true},
		{"legacy subscription", "/fixed", "", `not json`, "", false, true},
		{"missing", "", "/project", `{}`, "", true, false},
		{"number", "", "/project", `{"project":1}`, "", true, false},
		{"blank", "", "/project", `{"project":" "}`, "", true, false},
		{"invalid JSON", "", "/project", `invalid`, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &launchDispatcher{}
			_, err := launchWebhook(t.Context(), d, state.Routine{Directory: tc.directory}, state.WebhookSubscription{RoutineID: "r", ProjectPointer: tc.pointer}, []byte(tc.body), "data", "label", 1)
			if (err != nil) != tc.failure || d.project != tc.project || (d.legacy != "") != tc.legacy {
				t.Fatalf("launch = %+v, %v", d, err)
			}
		})
	}
}

func TestValidProjectPointer(t *testing.T) {
	for _, pointer := range []string{"", "/repository/clone_url", "/a~1b/~0", "/"} {
		if !validProjectPointer(pointer) {
			t.Errorf("rejected %q", pointer)
		}
	}
	for _, pointer := range []string{"project", "/bad~", "/bad~2"} {
		if validProjectPointer(pointer) {
			t.Errorf("accepted %q", pointer)
		}
	}
}
