package routines

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/webhook"
)

// RunWebhook reports a launch failure as a failed run with a nil error; the
// delivery log must still show it as failed, and the Inbox must say so once.
func TestWebhookLaunchFailureIsLoggedAsFailure(t *testing.T) {
	h := newHarness(t)
	h.host.err = errors.New("opencode would not start")
	routine, err := h.svc.Create(t.Context(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := h.db.SaveWebhookInbox(ctx, state.WebhookInbox{ID: "inbox", RoutineID: "inbox", RelayURL: "https://relay", Identity: "id"}); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SaveWebhookSubscription(ctx, state.WebhookSubscription{ID: "sub", InboxID: "inbox", RoutineID: routine.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.AcceptWebhookDelivery(ctx, "inbox", "d1", "POST webhook", "{}", "{}", "", 1); err != nil {
		t.Fatal(err)
	}
	e := relay.InboxEnvelope{InboxID: "inbox", DeliveryID: "d1", Body: []byte("{}")}
	if err := webhook.Dispatch(h.db, h.svc, "inbox", "d1", e, time.UnixMilli(h.now.Load())); err != nil {
		t.Fatal(err)
	}
	log, err := h.db.ListWebhookDeliveries(ctx, "inbox", 10)
	if err != nil || len(log) != 1 || len(log[0].Dispatches) != 1 {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if d := log[0].Dispatches[0]; d.State != "failure" || !strings.Contains(d.Error, "would not start") {
		t.Fatalf("launch failure logged as %+v", d)
	}
	// The failed run already posts its own notice; the dispatch must not add one.
	if items, err := h.db.ListInboxItems(ctx); err != nil || len(items) != 1 {
		t.Fatalf("inbox items = %+v, %v", items, err)
	}
}
