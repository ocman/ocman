package webhook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/share"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
)

type webhookRoundTrip func(*http.Request) (*http.Response, error)

func (f webhookRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type webhookDispatchFunc func(context.Context) error

func (f webhookDispatchFunc) RunWebhook(ctx context.Context, _, _, _ string, _ int64) (state.RoutineRun, error) {
	return state.RoutineRun{}, f(ctx)
}

// All relay requests and routine launches stay in-process. Only this test's
// temporary state database and delivery files are writable.
type pollingFixture struct {
	poller    *Poller
	transport webhookRoundTrip
	deliver   func()
	pending   bool
	acks      int
}

func newPollingFixture(t *testing.T) *pollingFixture {
	t.Helper()
	db := statetest.Open(t, tasksvc.Schema)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	inbox := state.WebhookInbox{ID: "inbox", RoutineID: "routine", RelayURL: "https://relay.invalid", Identity: identity.String()}
	if err := db.CreateRoutine(t.Context(), state.Routine{ID: "routine", Name: "Fake routine", Directory: t.TempDir(), RemoteID: "local", ScheduleKind: "none", ScheduleConfigJSON: "{}", Enabled: true, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveWebhookInbox(t.Context(), inbox); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveWebhookSubscription(t.Context(), state.WebhookSubscription{ID: "sub", InboxID: inbox.ID, RoutineID: "routine"}); err != nil {
		t.Fatal(err)
	}
	f := &pollingFixture{poller: &Poller{Store: db, Inbox: inbox, Routines: &fakeRoutines{}}}
	sequence := 0
	var deliveryID string
	var ciphertext []byte
	f.deliver = func() {
		sequence++
		deliveryID = fmt.Sprintf("delivery-%d", sequence)
		var err error
		ciphertext, err = share.EncryptInboxEnvelope(identity.Recipient(), share.InboxEnvelope{
			FormatVersion: share.InboxEnvelopeVersion, KeyVersion: 1, InboxID: inbox.ID, DeliveryID: deliveryID,
			Body: []byte(`{"action":"opened"}`), Request: share.InboxRequest{Method: http.MethodPost, ReceivedAt: time.Now().UnixMilli()},
		})
		if err != nil {
			t.Fatal(err)
		}
		f.pending = true
	}
	f.transport = func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		status, body := http.StatusOK, ""
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/inboxes/inbox/deliveries":
			body = `{"deliveries":[]}`
			if f.pending {
				body = fmt.Sprintf(`{"deliveries":[{"id":%q}]}`, deliveryID)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/inboxes/inbox/deliveries/"+deliveryID:
			body = string(ciphertext)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ack"):
			status, f.pending = http.StatusNoContent, false
			f.acks++
		default:
			t.Errorf("unexpected fake relay request: %s %s", r.Method, r.URL.Path)
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	f.poller.HTTP = &http.Client{Transport: f.transport}
	return f
}

func TestWebhookPollingResumesAfterReceiptStorageRecovers(t *testing.T) {
	f := newPollingFixture(t)
	m := tasksvc.New(f.poller.Store, 1)
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	ctx := module.WithScheduleObserver(t.Context(), m)
	spec := module.Schedule{Name: "webhook-poll.inbox", Title: "Poll webhook inbox", Interval: PollInterval}
	poll := func() error { return module.RunScheduled(ctx, spec, f.poller.Poll) }
	if _, err := f.poller.Store.SQL().Exec(`CREATE TRIGGER reject_completion BEFORE UPDATE ON background_task
		WHEN NEW.status <> 'running' BEGIN SELECT RAISE(FAIL,'completion write failed'); END`); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	if err := poll(); err == nil || f.pending || f.acks != 1 {
		t.Fatalf("first delivery or failure injection: pending=%v acks=%d err=%v", f.pending, f.acks, err)
	}
	if _, err := f.poller.Store.SQL().Exec(`DROP TRIGGER reject_completion`); err != nil {
		t.Fatal(err)
	}
	f.deliver()
	if err := poll(); err != nil || f.pending || f.acks != 2 || f.poller.Routines.(*fakeRoutines).runs.Load() != 2 {
		t.Fatalf("webhook consumption stalled after storage recovered: pending=%v acks=%d err=%v", f.pending, f.acks, err)
	}
}
