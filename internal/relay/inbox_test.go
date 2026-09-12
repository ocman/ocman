package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"github.com/NoUseFreak/ocman/internal/share"
)

const testEnrollmentToken = "enroll-secret"

type waitingStore struct {
	share.Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type failingDeliveryStore struct{ share.Store }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func (s failingDeliveryStore) Put(ctx context.Context, key string, data []byte) error {
	if strings.Contains(key, "/deliveries/") {
		return io.ErrClosedPipe
	}
	return s.Store.Put(ctx, key, data)
}

func (s *waitingStore) Put(ctx context.Context, key string, data []byte) error {
	if strings.Contains(key, "/deliveries/") {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	return s.Store.Put(ctx, key, data)
}

func newInboxHarness(t *testing.T, tweak func(*Config)) *harness {
	t.Helper()
	return newHarness(t, func(cfg *Config) {
		cfg.EnrollmentToken = testEnrollmentToken
		if tweak != nil {
			tweak(cfg)
		}
	})
}

func registerInbox(t *testing.T, h *harness, recipient string) inboxRegistrationResponse {
	t.Helper()
	body, _ := json.Marshal(inboxRegistrationRequest{Recipient: recipient})
	rec := h.do(http.MethodPost, "/inboxes", body, testEnrollmentToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: status %d, body %s", rec.Code, rec.Body)
	}
	var got inboxRegistrationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	return got
}

func TestInboxRoundTrip(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	registered := registerInbox(t, h, identity.Recipient().String())
	if registered.ID == "" || registered.IngestionURL == "" || registered.ManagementToken == "" || registered.FetchToken == "" || registered.AcknowledgmentToken == "" {
		t.Fatalf("registration omitted credentials: %+v", registered)
	}
	if registered.ManagementToken == registered.FetchToken || registered.FetchToken == registered.AcknowledgmentToken {
		t.Fatal("registration credentials are not separate")
	}

	body := []byte{0, 'e', 'x', 'a', 'c', 't', 0xff, '\n'}
	req := httptest.NewRequest(http.MethodPost, registered.IngestionURL+"?source=test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest: status %d, body %s", rec.Code, rec.Body)
	}
	var accepted inboxIngestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}

	listed := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries", nil, registered.FetchToken)
	if listed.Code != http.StatusOK {
		t.Fatalf("list: status %d, body %s", listed.Code, listed.Body)
	}
	var page inboxDeliveryPage
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Deliveries) != 1 || page.Deliveries[0].ID != accepted.DeliveryID || page.Cursor != "" {
		t.Fatalf("listed deliveries = %+v", page)
	}

	fetched := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries/"+accepted.DeliveryID, nil, registered.FetchToken)
	if fetched.Code != http.StatusOK {
		t.Fatalf("fetch: status %d, body %s", fetched.Code, fetched.Body)
	}
	if bytes.Contains(fetched.Body.Bytes(), body) {
		t.Fatal("stored delivery contains plaintext body")
	}
	envelope, err := DecryptInboxEnvelope(identity, registered.ID, accepted.DeliveryID, fetched.Body.Bytes())
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(envelope.Body, body) || envelope.KeyVersion != 1 || envelope.FormatVersion != 1 {
		t.Fatalf("envelope = %+v", envelope)
	}
	if envelope.Request.Query.Get("source") != "test" || envelope.Request.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("request metadata = %+v", envelope.Request)
	}

	for range 2 {
		acked := h.do(http.MethodPost, "/inboxes/"+registered.ID+"/deliveries/"+accepted.DeliveryID+"/ack", nil, registered.AcknowledgmentToken)
		if acked.Code != http.StatusNoContent {
			t.Fatalf("ack: status %d, body %s", acked.Code, acked.Body)
		}
	}
	empty := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries", nil, registered.FetchToken)
	if empty.Code != http.StatusOK {
		t.Fatalf("list after ack: status %d", empty.Code)
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &page); err != nil || len(page.Deliveries) != 0 {
		t.Fatalf("deliveries remain after ack: %+v, err %v", page, err)
	}
}

func TestInboxRegistrationRequiresEnrollmentTokenAndHashesCredentials(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	body, _ := json.Marshal(inboxRegistrationRequest{Recipient: identity.Recipient().String()})
	if rec := h.do(http.MethodPost, "/inboxes", body, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("registration status %d, want 401", rec.Code)
	}
	registered := registerInbox(t, h, identity.Recipient().String())

	objects, err := h.store.List(t.Context(), "inboxes/")
	if err != nil {
		t.Fatal(err)
	}
	ingestToken := strings.TrimPrefix(registered.IngestionURL, "/i/"+registered.ID+"/")
	secrets := []string{ingestToken, registered.ManagementToken, registered.FetchToken, registered.AcknowledgmentToken}
	for _, object := range objects {
		data, err := h.store.Get(t.Context(), object.Key)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("credential stored in plaintext at %s", object.Key)
			}
		}
	}
	if rec := h.do(http.MethodGet, "/inboxes/"+registered.ID, nil, registered.ManagementToken); rec.Code != http.StatusOK {
		t.Fatalf("management status %d", rec.Code)
	}
}

func TestInboxIngestionCredentialCannotFetchOrManage(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	registered := registerInbox(t, h, identity.Recipient().String())
	ingestToken := strings.TrimPrefix(registered.IngestionURL, "/i/"+registered.ID+"/")
	for _, path := range []string{
		"/inboxes/" + registered.ID,
		"/inboxes/" + registered.ID + "/deliveries",
	} {
		if rec := h.do(http.MethodGet, path, nil, ingestToken); rec.Code != http.StatusNotFound {
			t.Fatalf("%s with ingestion token: status %d, want 404", path, rec.Code)
		}
	}
	for name, token := range map[string]string{
		"ingestion":      ingestToken,
		"management":     registered.ManagementToken,
		"acknowledgment": registered.AcknowledgmentToken,
	} {
		if rec := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries", nil, token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s credential fetched deliveries: status %d", name, rec.Code)
		}
	}
	if rec := h.do(http.MethodGet, "/inboxes/"+registered.ID, nil, registered.FetchToken); rec.Code != http.StatusNotFound {
		t.Fatalf("fetch credential managed inbox: status %d", rec.Code)
	}
	ingested := h.do(http.MethodPost, registered.IngestionURL, []byte("payload"), "")
	var accepted inboxIngestResponse
	_ = json.Unmarshal(ingested.Body.Bytes(), &accepted)
	ackPath := "/inboxes/" + registered.ID + "/deliveries/" + accepted.DeliveryID + "/ack"
	for name, token := range map[string]string{
		"ingestion":  ingestToken,
		"management": registered.ManagementToken,
		"fetch":      registered.FetchToken,
	} {
		if rec := h.do(http.MethodPost, ackPath, nil, token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s credential acknowledged delivery: status %d", name, rec.Code)
		}
	}
	fetchPath := "/inboxes/" + registered.ID + "/deliveries/" + accepted.DeliveryID
	for name, token := range map[string]string{
		"ingestion":      ingestToken,
		"management":     registered.ManagementToken,
		"acknowledgment": registered.AcknowledgmentToken,
	} {
		if rec := h.do(http.MethodGet, fetchPath, nil, token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s credential fetched a delivery: status %d", name, rec.Code)
		}
	}
	for name, token := range map[string]string{
		"ingestion":      ingestToken,
		"fetch":          registered.FetchToken,
		"acknowledgment": registered.AcknowledgmentToken,
	} {
		if rec := h.do(http.MethodGet, "/inboxes/"+registered.ID, nil, token); rec.Code != http.StatusNotFound {
			t.Fatalf("%s credential managed inbox: status %d", name, rec.Code)
		}
	}
	if rec := h.do(http.MethodGet, fetchPath, nil, registered.FetchToken); rec.Code != http.StatusOK {
		t.Fatalf("wrong-role acknowledgment removed delivery: status %d", rec.Code)
	}
}

func TestInboxAcceptanceWaitsForStorage(t *testing.T) {
	var store *waitingStore
	h := newInboxHarness(t, func(cfg *Config) {
		store = &waitingStore{Store: cfg.Store, started: make(chan struct{}), release: make(chan struct{})}
		cfg.Store = store
	})
	identity, _ := age.GenerateX25519Identity()
	registered := registerInbox(t, h, identity.Recipient().String())
	done := make(chan int, 1)
	go func() { done <- h.do(http.MethodPost, registered.IngestionURL, []byte("payload"), "").Code }()
	<-store.started
	select {
	case <-done:
		t.Fatal("ingestion returned before storage completed")
	default:
	}
	close(store.release)
	if code := <-done; code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
}

func TestInboxRejectsFailedStorageAndOversizedBodies(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	t.Run("storage failure", func(t *testing.T) {
		h := newInboxHarness(t, func(cfg *Config) { cfg.Store = failingDeliveryStore{cfg.Store} })
		registered := registerInbox(t, h, identity.Recipient().String())
		if rec := h.do(http.MethodPost, registered.IngestionURL, []byte("payload"), ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d, want 500", rec.Code)
		}
	})
	t.Run("body limit", func(t *testing.T) {
		h := newInboxHarness(t, func(cfg *Config) { cfg.MaxInboxBodyBytes = 4 })
		registered := registerInbox(t, h, identity.Recipient().String())
		if rec := h.do(http.MethodPost, registered.IngestionURL, []byte("large"), ""); rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, want 413", rec.Code)
		}
	})
	t.Run("body read failure", func(t *testing.T) {
		h := newInboxHarness(t, nil)
		registered := registerInbox(t, h, identity.Recipient().String())
		if rec := h.doBody(http.MethodPost, registered.IngestionURL, failingReader{}, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", rec.Code)
		}
	})
}

func TestInboxDeliveryListingUsesCursor(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	registered := registerInbox(t, h, identity.Recipient().String())
	for range inboxPageSize + 1 {
		if rec := h.do(http.MethodPost, registered.IngestionURL, nil, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("ingest status %d", rec.Code)
		}
	}

	first := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries", nil, registered.FetchToken)
	var page inboxDeliveryPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Deliveries) != inboxPageSize || page.Cursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	inserted := h.do(http.MethodPost, registered.IngestionURL, nil, "")
	if inserted.Code != http.StatusAccepted {
		t.Fatalf("ingest between pages: status %d", inserted.Code)
	}
	var accepted inboxIngestResponse
	_ = json.Unmarshal(inserted.Body.Bytes(), &accepted)
	after, through, ok := decodeInboxCursor(page.Cursor)
	if !ok {
		t.Fatal("server returned an invalid cursor")
	}
	second := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries?cursor="+page.Cursor, nil, registered.FetchToken)
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	want := 1
	if accepted.DeliveryID > after && accepted.DeliveryID <= through {
		want++
	}
	if len(page.Deliveries) != want || page.Cursor != "" {
		t.Fatalf("second page = %+v", page)
	}
	fresh := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries", nil, registered.FetchToken)
	if err := json.Unmarshal(fresh.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Deliveries) != inboxPageSize {
		t.Fatalf("fresh snapshot returned %d deliveries, want %d", len(page.Deliveries), inboxPageSize)
	}
	found := false
	for _, delivery := range page.Deliveries {
		found = found || delivery.ID == accepted.DeliveryID
	}
	if !found {
		next := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries?cursor="+page.Cursor, nil, registered.FetchToken)
		if err := json.Unmarshal(next.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, delivery := range page.Deliveries {
			found = found || delivery.ID == accepted.DeliveryID
		}
	}
	if !found {
		t.Fatal("delivery inserted during pagination was lost")
	}
}

func TestDecryptInboxEnvelopeRejectsWrongIdentityTamperingAndVersions(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	wrong, _ := age.GenerateX25519Identity()
	registered := registerInbox(t, h, identity.Recipient().String())
	rec := h.do(http.MethodPost, registered.IngestionURL, []byte("secret"), "")
	var accepted inboxIngestResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &accepted)
	fetched := h.do(http.MethodGet, "/inboxes/"+registered.ID+"/deliveries/"+accepted.DeliveryID, nil, registered.FetchToken)

	if _, err := DecryptInboxEnvelope(wrong, registered.ID, accepted.DeliveryID, fetched.Body.Bytes()); err == nil {
		t.Fatal("wrong identity decrypted delivery")
	}
	tampered := append([]byte(nil), fetched.Body.Bytes()...)
	tampered[len(tampered)-1] ^= 1
	if _, err := DecryptInboxEnvelope(identity, registered.ID, accepted.DeliveryID, tampered); err == nil {
		t.Fatal("tampered delivery decrypted")
	}

	for _, unsupported := range []InboxEnvelope{
		{FormatVersion: 2, KeyVersion: 1, InboxID: registered.ID, DeliveryID: accepted.DeliveryID},
		{FormatVersion: 1, KeyVersion: 2, InboxID: registered.ID, DeliveryID: accepted.DeliveryID},
	} {
		plain, _ := json.Marshal(unsupported)
		var encrypted bytes.Buffer
		w, err := age.Encrypt(&encrypted, identity.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(plain)
		_ = w.Close()
		if _, err := DecryptInboxEnvelope(identity, registered.ID, accepted.DeliveryID, encrypted.Bytes()); err == nil {
			t.Fatalf("unsupported envelope version decrypted: %+v", unsupported)
		}
	}
}

func TestRegisterInboxRejectsNonX25519Recipient(t *testing.T) {
	h := newInboxHarness(t, nil)
	body := strings.NewReader(`{"recipient":"not-an-age-recipient"}`)
	req := httptest.NewRequest(http.MethodPost, "/inboxes", body)
	req.Header.Set("Authorization", "Bearer "+testEnrollmentToken)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
