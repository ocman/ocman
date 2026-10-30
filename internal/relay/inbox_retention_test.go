package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"filippo.io/age"
)

func TestInboxSuccessfulPollingRenewsRetention(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	h.now = h.now.Add(29 * 24 * time.Hour)
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries", nil, inbox.FetchToken); r.Code != http.StatusOK {
		t.Fatalf("empty poll: %d %s", r.Code, r.Body)
	}
	h.now = h.now.Add(29 * 24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken); r.Code != http.StatusOK {
		t.Fatalf("actively polled inbox expired: %d %s", r.Code, r.Body)
	}
	h.now = h.now.Add(24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken); r.Code != http.StatusNotFound {
		t.Fatalf("inbox retained after 30 days without polling: %d", r.Code)
	}
}

func TestInboxUnacknowledgedMessagesExpireAfter48Hours(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	old := ingestRetentionMessage(t, h, inbox, "old")
	h.now = h.now.Add(47 * time.Hour)
	fresh := ingestRetentionMessage(t, h, inbox, "fresh")
	// Fetching without acknowledging must not extend message retention.
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries/"+old, nil, inbox.FetchToken); r.Code != http.StatusOK {
		t.Fatalf("fetch: %d", r.Code)
	}
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries/"+old, nil, inbox.FetchToken); r.Code != http.StatusOK {
		t.Fatalf("message expired before 48 hours: %d", r.Code)
	}
	h.now = h.now.Add(time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		want int
	}{{old, http.StatusNotFound}, {fresh, http.StatusOK}} {
		if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries/"+tc.id, nil, inbox.FetchToken); r.Code != tc.want {
			t.Errorf("message %s: status %d, want %d", tc.id, r.Code, tc.want)
		}
	}
	if r := h.do(http.MethodPost, "/inboxes/"+inbox.ID+"/deliveries/"+fresh+"/ack", nil, inbox.AcknowledgmentToken); r.Code != http.StatusNoContent {
		t.Fatalf("ack: %d", r.Code)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries/"+fresh, nil, inbox.FetchToken); r.Code != http.StatusNotFound {
		t.Fatalf("acknowledged message retained: %d", r.Code)
	}
}

func TestInboxUnsuccessfulPollingDoesNotRenewRetention(t *testing.T) {
	for _, mode := range []string{"wrong-token", "invalid-cursor", "ingest-only", "management-only"} {
		t.Run(mode, func(t *testing.T) {
			h := newInboxHarness(t, nil)
			identity, _ := age.GenerateX25519Identity()
			inbox := registerInbox(t, h, identity.Recipient().String())
			h.now = h.now.Add(29 * 24 * time.Hour)
			switch mode {
			case "wrong-token":
				h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries", nil, "wrong")
			case "invalid-cursor":
				h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries?cursor=invalid", nil, inbox.FetchToken)
			case "ingest-only":
				ingestRetentionMessage(t, h, inbox, "unread")
			case "management-only":
				h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken)
			}
			h.now = h.now.Add(24 * time.Hour)
			if err := h.srv.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken); r.Code != http.StatusNotFound {
				t.Fatalf("inactive inbox retained: %d", r.Code)
			}
		})
	}
}

func ingestRetentionMessage(t *testing.T, h *harness, inbox inboxRegistrationResponse, body string) string {
	t.Helper()
	r := h.do(http.MethodPost, inbox.IngestionURL, []byte(body), "")
	if r.Code != http.StatusAccepted {
		t.Fatalf("ingest: %d %s", r.Code, r.Body)
	}
	var out inboxIngestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.DeliveryID
}

func TestInboxLegacyMessageGetsFullRetentionWindow(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	id := ingestRetentionMessage(t, h, inbox, "legacy")
	if err := h.store.DeletePrefix(t.Context(), inboxDeliveryTimeKey(inbox.ID, id)); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(7 * 24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(47 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries/"+id, nil, inbox.FetchToken)
	if r.Code != http.StatusOK {
		t.Fatalf("legacy message lost on upgrade: %d", r.Code)
	}
	if envelope, err := DecryptInboxEnvelope(identity, inbox.ID, id, r.Body.Bytes()); err != nil || string(envelope.Body) != "legacy" {
		t.Fatalf("legacy ciphertext changed: %v", err)
	}
	h.now = h.now.Add(time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries/"+id, nil, inbox.FetchToken); r.Code != http.StatusNotFound {
		t.Fatalf("legacy message not expired: %d", r.Code)
	}
}

func TestInboxPollingRetentionSurvivesRestart(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	h.now = h.now.Add(29 * 24 * time.Hour)
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries", nil, inbox.FetchToken); r.Code != http.StatusOK {
		t.Fatalf("poll: %d", r.Code)
	}
	srv, err := New(h.srv.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	h.now = h.now.Add(29 * 24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries", nil, inbox.FetchToken); r.Code != http.StatusOK {
		t.Fatalf("poll retention lost after restart: %d", r.Code)
	}
}

func TestInboxCustomDeliveryRetentionAndQuotaRelease(t *testing.T) {
	h := newInboxHarness(t, func(c *Config) {
		c.InboxDeliveryTTL = time.Hour
		c.MaxInboxPendingDeliveries = 1
	})
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	id := ingestRetentionMessage(t, h, inbox, "old")
	h.now = h.now.Add(time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	ingestRetentionMessage(t, h, inbox, "replacement")
	if _, err := h.store.Get(t.Context(), inboxDeliveryTimeKey(inbox.ID, id)); err == nil {
		t.Fatal("expired delivery timestamp retained")
	}
}

func TestInboxLegacyActivityGetsFullInactivityWindow(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	m, found, err := getInboxMeta(t.Context(), h.store, inbox.ID)
	if err != nil || !found {
		t.Fatalf("metadata: found %t, error %v", found, err)
	}
	m.LastPolledAt = 0
	if err := putInboxMeta(t.Context(), h.store, inbox.ID, m); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(60 * 24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(29 * 24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken); r.Code != http.StatusOK {
		t.Fatalf("legacy inbox lost on upgrade: %d", r.Code)
	}
	h.now = h.now.Add(24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken); r.Code != http.StatusNotFound {
		t.Fatalf("legacy inactivity window renewed without polling: %d", r.Code)
	}
}
