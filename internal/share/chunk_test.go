package share

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func mustKey(t *testing.T) Key {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func TestSealOpen_RoundTrip(t *testing.T) {
	k := mustKey(t)
	plain := []byte(`{"session":{"id":"ses_1"},"messages":[]}`)

	ct, err := Seal(k, "20260813-abc", 7, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ct, []byte("ses_1")) {
		t.Fatal("ciphertext contains plaintext")
	}

	got, err := Open(k, "20260813-abc", 7, ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plain)
	}
}

// TestSeal_Deterministic proves an append retry is byte-identical, which
// is what makes PUT /s/{id}/{seq} idempotent (nonce is derived from seq,
// never random).
func TestSeal_Deterministic(t *testing.T) {
	k := mustKey(t)
	plain := []byte("same turn, retried")

	a, err := Seal(k, "id", 3, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := Seal(k, "id", 3, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("Seal is not deterministic; a retried append would not be idempotent")
	}
}

// TestSeal_NonceVariesWithSeq is the catastrophic-failure guard: AES-GCM
// nonce reuse across two different plaintexts under one key leaks the
// XOR of the plaintexts and the auth key. Different seq must mean a
// different keystream.
func TestSeal_NonceVariesWithSeq(t *testing.T) {
	k := mustKey(t)
	plain := []byte("identical plaintext")

	a, err := Seal(k, "id", 1, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := Seal(k, "id", 2, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("identical ciphertext for different seq: nonce is being reused")
	}
}

// TestOpen_RejectsResequencedChunk proves the sequence number is bound
// into the AAD. Without it a relay could renumber or reorder chunks
// undetected, since each chunk is individually authentic.
func TestOpen_RejectsResequencedChunk(t *testing.T) {
	k := mustKey(t)
	ct, err := Seal(k, "id", 5, []byte("turn five"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := Open(k, "id", 6, ct); err == nil {
		t.Fatal("chunk opened under a different seq: sequence is not authenticated")
	}
}

// TestOpen_RejectsCrossShareChunk proves the share id is bound into the
// AAD, so a chunk cannot be transplanted into another share.
func TestOpen_RejectsCrossShareChunk(t *testing.T) {
	k := mustKey(t)
	ct, err := Seal(k, "share-a", 1, []byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := Open(k, "share-b", 1, ct); err == nil {
		t.Fatal("chunk opened under a different share id: id is not authenticated")
	}
}

func TestOpen_RejectsWrongKey(t *testing.T) {
	a, b := mustKey(t), mustKey(t)
	ct, err := Seal(a, "id", 1, []byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := Open(b, "id", 1, ct); err == nil {
		t.Fatal("chunk opened under the wrong key")
	}
}

func TestOpen_RejectsTamperedCiphertext(t *testing.T) {
	k := mustKey(t)
	ct, err := Seal(k, "id", 1, []byte("secret payload"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	tampered := bytes.Clone(ct)
	tampered[len(tampered)/2] ^= 0xff
	if _, err := Open(k, "id", 1, tampered); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
}

func TestOpen_RejectsShortCiphertext(t *testing.T) {
	k := mustKey(t)
	if _, err := Open(k, "id", 1, []byte{0x01, 0x02}); err == nil {
		t.Fatal("truncated ciphertext opened")
	}
}

func TestSeal_RejectsSeqOverflow(t *testing.T) {
	k := mustKey(t)
	if _, err := Seal(k, "id", MaxSeq+1, []byte("x")); err == nil {
		t.Fatalf("Seal accepted seq above MaxSeq (%d)", MaxSeq)
	}
}

func TestKey_EncodeDecodeRoundTrip(t *testing.T) {
	k := mustKey(t)
	s := k.String()
	if strings.ContainsAny(s, "+/=") {
		t.Fatalf("key encoding %q is not URL-fragment safe", s)
	}
	got, err := ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	if got != k {
		t.Fatal("key round trip mismatch")
	}
}

func TestParseKey_Rejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"too short", "AAAA"},
		{"not base64url", "!!!!"},
		{"too long", strings.Repeat("A", 64)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseKey(tc.in); err == nil {
				t.Fatalf("ParseKey(%q) succeeded, want error", tc.in)
			}
		})
	}
}

func TestNewKey_NotAllZero(t *testing.T) {
	k := mustKey(t)
	if k == (Key{}) {
		t.Fatal("NewKey returned a zero key")
	}
}

// TestNonceLayout pins the nonce derivation so a future refactor cannot
// silently change it and break every already-published share.
func TestNonceLayout(t *testing.T) {
	n := nonceFor(0x0102030405060708)
	want := make([]byte, nonceSize)
	binary.BigEndian.PutUint64(want[nonceSize-8:], 0x0102030405060708)
	if !bytes.Equal(n[:], want) {
		t.Fatalf("nonce layout changed: got %x want %x", n, want)
	}
}
