package share

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayClientLifecycle(t *testing.T) {
	var gotChunk []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/s":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"share-id","deleteToken":"secret"}`)
		case r.Method == http.MethodPut && r.URL.Path == "/s/share-id/2":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Fatal("PUT missing bearer token")
			}
			gotChunk, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/s/share-id":
			if r.Header.Get("Authorization") != "Bearer secret" {
				t.Fatal("DELETE missing bearer token")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := RelayClient{BaseURL: srv.URL, HTTP: srv.Client()}
	a, err := c.Create(context.Background())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := c.Put(context.Background(), a, 2, []byte("ciphertext")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if string(gotChunk) != "ciphertext" {
		t.Fatalf("chunk = %q", gotChunk)
	}
	if err := c.Delete(context.Background(), a); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRelayClientRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := (RelayClient{BaseURL: srv.URL}).Create(context.Background())
	if err == nil {
		t.Fatal("Create succeeded on 429")
	}
}

func TestRelayInboxClientRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := RelayClient{BaseURL: srv.URL}

	tests := map[string]func() error{
		"register": func() error { _, err := c.RegisterInbox(t.Context(), "recipient", "token"); return err },
		"register with secret": func() error {
			_, err := c.RegisterInboxWithSecret(t.Context(), "recipient", "token", "secret", "X-Secret")
			return err
		},
		"rotate": func() error { _, err := c.RotateInbox(t.Context(), "inbox", "token", "recipient"); return err },
		"revoke": func() error { return c.RevokeInbox(t.Context(), "inbox", "token") },
		"list": func() error {
			_, err := c.ListInboxDeliveries(t.Context(), "inbox", "token", "cursor")
			return err
		},
		"fetch": func() error {
			_, err := c.FetchInboxDelivery(t.Context(), "inbox", "delivery", "token")
			return err
		},
		"acknowledge": func() error { return c.AcknowledgeInboxDelivery(t.Context(), "inbox", "delivery", "token") },
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("request succeeded on 429")
			}
		})
	}
}
