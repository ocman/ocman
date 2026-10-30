package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/ocman/ocman/internal/share"
)

type retentionFaultStore struct {
	Store
	op, key string
}

func (s retentionFaultStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s.op == "get" && key == s.key {
		return nil, io.ErrUnexpectedEOF
	}
	return s.Store.Get(ctx, key)
}

func (s retentionFaultStore) Put(ctx context.Context, key string, data []byte) error {
	if s.op == "put" && key == s.key {
		return io.ErrClosedPipe
	}
	return s.Store.Put(ctx, key, data)
}

func (s retentionFaultStore) List(ctx context.Context, prefix string) ([]share.Object, error) {
	if s.op == "list" && prefix == s.key {
		return nil, io.ErrUnexpectedEOF
	}
	return s.Store.List(ctx, prefix)
}

func (s retentionFaultStore) DeletePrefix(ctx context.Context, prefix string) error {
	if s.op == "delete" && prefix == s.key {
		return io.ErrClosedPipe
	}
	return s.Store.DeletePrefix(ctx, prefix)
}

func TestInboxRetentionStorageFailures(t *testing.T) {
	for _, mode := range []string{
		"metadata-read", "legacy-activity-write", "delivery-list", "timestamp-read", "legacy-timestamp-write",
		"invalid-timestamp", "zero-timestamp", "delivery-delete", "timestamp-delete", "inbox-delete",
	} {
		t.Run(mode, func(t *testing.T) {
			h := newInboxHarness(t, nil)
			identity, _ := age.GenerateX25519Identity()
			inbox := registerInbox(t, h, identity.Recipient().String())
			id := ingestRetentionMessage(t, h, inbox, "keep on error")
			timeKey := inboxDeliveryTimeKey(inbox.ID, id)
			fault := retentionFaultStore{Store: h.store}
			h.now = h.now.Add(48 * time.Hour)
			switch mode {
			case "metadata-read":
				fault.op, fault.key = "get", inboxMetaKey(inbox.ID)
			case "legacy-activity-write":
				m, found, err := getInboxMeta(t.Context(), h.store, inbox.ID)
				if err != nil || !found {
					t.Fatalf("metadata: found %t, error %v", found, err)
				}
				m.LastPolledAt = 0
				if err := putInboxMeta(t.Context(), h.store, inbox.ID, m); err != nil {
					t.Fatal(err)
				}
				fault.op, fault.key = "put", inboxMetaKey(inbox.ID)
			case "delivery-list":
				fault.op, fault.key = "list", inboxDeliveryPrefix(inbox.ID)
			case "timestamp-read":
				fault.op, fault.key = "get", timeKey
			case "legacy-timestamp-write":
				if err := h.store.DeletePrefix(t.Context(), timeKey); err != nil {
					t.Fatal(err)
				}
				fault.op, fault.key = "put", timeKey
			case "invalid-timestamp", "zero-timestamp":
				value := []byte("invalid")
				if mode == "zero-timestamp" {
					value = []byte("0")
				}
				if err := h.store.Put(t.Context(), timeKey, value); err != nil {
					t.Fatal(err)
				}
			case "delivery-delete":
				fault.op, fault.key = "delete", inboxDeliveryKey(inbox.ID, id)
			case "timestamp-delete":
				fault.op, fault.key = "delete", timeKey
			case "inbox-delete":
				h.now = h.now.Add(30 * 24 * time.Hour)
				fault.op, fault.key = "delete", "inboxes/"+inbox.ID
			}
			h.srv.cfg.Store = fault
			if err := h.srv.Sweep(t.Context()); err == nil {
				t.Fatal("storage error hidden by sweep")
			}
		})
	}
}

func TestInboxFailedPollMetadataWriteDoesNotRenew(t *testing.T) {
	h := newInboxHarness(t, nil)
	identity, _ := age.GenerateX25519Identity()
	inbox := registerInbox(t, h, identity.Recipient().String())
	h.now = h.now.Add(29 * 24 * time.Hour)
	h.srv.cfg.Store = retentionFaultStore{Store: h.store, op: "put", key: inboxMetaKey(inbox.ID)}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID+"/deliveries", nil, inbox.FetchToken); r.Code != http.StatusInternalServerError {
		t.Fatalf("failed renewal returned success: %d", r.Code)
	}
	h.srv.cfg.Store = h.store
	h.now = h.now.Add(24 * time.Hour)
	if err := h.srv.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/inboxes/"+inbox.ID, nil, inbox.ManagementToken); r.Code != http.StatusNotFound {
		t.Fatalf("failed poll renewed inbox: %d", r.Code)
	}
}

func TestInboxRetentionMissingAndMalformedMetadata(t *testing.T) {
	h := newInboxHarness(t, nil)
	if err := h.srv.sweepInbox(t.Context(), "missing", h.now); err != nil {
		t.Fatalf("revoked inbox: %v", err)
	}
	if err := h.store.Put(t.Context(), inboxMetaKey("invalid"), []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.sweepInbox(t.Context(), "invalid", h.now); err != nil {
		t.Fatalf("malformed metadata: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/inboxes/missing/deliveries", nil)
	rec := httptest.NewRecorder()
	h.srv.finishInboxPoll(rec, req, "missing", inboxDeliveryPage{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("poll recreated revoked inbox: %d", rec.Code)
	}
}

func TestInboxDeliveryWriteFailureCleansTimestamp(t *testing.T) {
	for _, failTimestamp := range []bool{false, true} {
		h := newInboxHarness(t, nil)
		key := inboxDeliveryKey("inbox", "delivery")
		if failTimestamp {
			key = inboxDeliveryTimeKey("inbox", "delivery")
		}
		h.srv.cfg.Store = retentionFaultStore{Store: h.store, op: "put", key: key}
		if err := h.srv.putInboxDelivery(t.Context(), "inbox", "delivery", []byte("ciphertext")); err == nil {
			t.Fatal("delivery write error hidden")
		}
		if _, err := h.store.Get(t.Context(), inboxDeliveryTimeKey("inbox", "delivery")); !errors.Is(err, share.ErrNotFound) {
			t.Fatalf("failed delivery left timestamp: %v", err)
		}
	}
}
