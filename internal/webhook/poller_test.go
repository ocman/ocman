package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
)

type pollerDispatcher struct {
	mu      sync.Mutex
	count   int
	payload []string
}

func (d *pollerDispatcher) RunWebhook(_ context.Context, _ string, payload string, _ int64) (state.RoutineRun, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.count++
	d.payload = append(d.payload, payload)
	return state.RoutineRun{}, nil
}

type pollerTransport struct {
	base     http.RoundTripper
	poisonID string
	failAck  bool
	mu       sync.Mutex
}

func (t *pollerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/deliveries/"+t.poisonID) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte("poison"))), Request: req}, nil
	}
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/ack") {
		t.mu.Lock()
		fail := t.failAck
		t.failAck = false
		t.mu.Unlock()
		if fail {
			return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("temporary failure")), Request: req}, nil
		}
	}
	return t.base.RoundTrip(req)
}

func TestPollerContinuesPastPoisonAndDeduplicatesAfterAckLoss(t *testing.T) {
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, err := relay.New(relay.Config{Store: store, EnrollmentToken: "enroll"})
	if err != nil {
		t.Fatal(err)
	}
	relayServer := httptest.NewServer(server)
	defer relayServer.Close()

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := (share.RelayClient{BaseURL: relayServer.URL}).RegisterInbox(t.Context(), identity.Recipient().String(), "enroll")
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(body string) string {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, relayServer.URL+allocation.IngestionURL, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("ingest status %d", resp.StatusCode)
		}
		var result struct {
			DeliveryID string `json:"deliveryId"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result.DeliveryID
	}
	deliver("valid")
	deliver("poison")
	page, err := (share.RelayClient{BaseURL: relayServer.URL}).ListInboxDeliveries(t.Context(), allocation.ID, allocation.FetchToken, "")
	if err != nil || len(page.Deliveries) != 2 {
		t.Fatalf("list = %+v, %v", page, err)
	}
	poisonID := page.Deliveries[0].ID

	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutine(t.Context(), state.Routine{ID: "routine", Name: "Webhook", Directory: "/repo", RemoteID: "local", ScheduleKind: "none", ScheduleConfigJSON: "{}", Enabled: true, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	inbox := state.WebhookInbox{ID: allocation.ID, RoutineID: "routine", RelayURL: relayServer.URL, FetchToken: allocation.FetchToken, AcknowledgmentToken: allocation.AcknowledgmentToken, Identity: identity.String()}
	if err := db.SaveWebhookInbox(t.Context(), inbox); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveWebhookSubscription(t.Context(), state.WebhookSubscription{ID: "sub", InboxID: allocation.ID, RoutineID: "routine"}); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	transport := &pollerTransport{base: http.DefaultTransport, poisonID: poisonID, failAck: true}
	dispatcher := &pollerDispatcher{}
	poller := &Poller{Store: db, Inbox: inbox, HTTP: &http.Client{Transport: transport}, Now: func() time.Time { return clock }, Routines: dispatcher}
	if err := poller.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	dispatcher.mu.Lock()
	if dispatcher.count != 1 {
		t.Fatalf("first poll dispatches = %d, payloads = %v", dispatcher.count, dispatcher.payload)
	}
	dispatcher.mu.Unlock()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock = clock.Add(3 * time.Second)
	secondDispatcher := &pollerDispatcher{}
	poller.Store, poller.Routines = db, secondDispatcher
	if err := poller.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	secondDispatcher.mu.Lock()
	if secondDispatcher.count != 0 {
		t.Fatalf("lost acknowledgement rescheduled %d routine runs", secondDispatcher.count)
	}
	secondDispatcher.mu.Unlock()
	items, err := db.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("durable inbox items = %+v, %v", items, err)
	}
	remaining, err := (share.RelayClient{BaseURL: relayServer.URL}).ListInboxDeliveries(t.Context(), allocation.ID, allocation.FetchToken, "")
	if err != nil || len(remaining.Deliveries) != 1 || remaining.Deliveries[0].ID != poisonID {
		t.Fatalf("remaining deliveries = %+v, %v", remaining, err)
	}
}
