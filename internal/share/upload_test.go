package share

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"filippo.io/age"
)

func TestChunkUploaderSealsAndEnforcesLimits(t *testing.T) {
	var puts [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		puts = append(puts, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	key := NewKey()
	plain := []byte("chunk")
	sealed, err := Seal(key, "share", 0, plain)
	if err != nil {
		t.Fatal(err)
	}
	sealedSize := int64(len(sealed))
	alloc := RelayAllocation{ID: "share", MaxChunkBytes: sealedSize, MaxShareBytes: 2 * sealedSize}
	u := ChunkUploader{Client: RelayClient{BaseURL: srv.URL, HTTP: srv.Client()}, Alloc: alloc, Key: key}
	for seq := range uint64(2) {
		if err := u.Put(t.Context(), seq, plain); err != nil {
			t.Fatalf("put %d: %v", seq, err)
		}
	}
	if got, err := Open(key, "share", 1, puts[1]); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("uploaded chunk = %q, %v", got, err)
	}
	var tooLarge *RelayError
	if err := u.Put(t.Context(), 2, plain); !errors.As(err, &tooLarge) {
		t.Fatalf("share limit: %v", err)
	}
	if err := u.Put(t.Context(), 2, append(plain, 'x')); !errors.As(err, &tooLarge) {
		t.Fatalf("chunk limit: %v", err)
	}
	if len(puts) != 2 {
		t.Fatalf("oversized chunks were uploaded: %d puts", len(puts))
	}
}

func TestInboxEnvelopeRoundTripAndIdentity(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	envelope := InboxEnvelope{FormatVersion: InboxEnvelopeVersion, KeyVersion: 1, InboxID: "inbox", DeliveryID: "d1", Body: []byte("payload")}
	ciphertext, err := EncryptInboxEnvelope(identity.Recipient(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptInboxEnvelope(identity, "inbox", "d1", ciphertext)
	if err != nil || string(got.Body) != "payload" {
		t.Fatalf("decrypted %+v, %v", got, err)
	}
	if _, err := DecryptInboxEnvelope(identity, "inbox", "d2", ciphertext); err == nil {
		t.Fatal("delivery identity mismatch accepted")
	}
	if _, err := DecryptInboxEnvelope(identity, "inbox", "d1", []byte("not age")); err == nil {
		t.Fatal("garbage decrypted")
	}
}
