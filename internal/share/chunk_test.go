package share

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestSealOpen_RoundTrip(t *testing.T) {
	k := NewKey()
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

// TestSeal_NonceDerivedFromSeq proves an append retry is byte-identical,
// which is what makes PUT /s/{id}/{seq} idempotent (nonce is derived from
// seq, never random). It is also the catastrophic-failure guard: AES-GCM
// nonce reuse across two different plaintexts under one key leaks the XOR
// of the plaintexts and the auth key, so a different seq must mean a
// different keystream.
func TestSeal_NonceDerivedFromSeq(t *testing.T) {
	k := NewKey()
	plain := []byte("same turn, retried")
	seal := func(seq uint64) []byte {
		t.Helper()
		ct, err := Seal(k, "id", seq, plain)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		return ct
	}

	if !bytes.Equal(seal(3), seal(3)) {
		t.Fatal("Seal is not deterministic; a retried append would not be idempotent")
	}
	if bytes.Equal(seal(1), seal(2)) {
		t.Fatal("identical ciphertext for different seq: nonce is being reused")
	}
}

// TestOpen_Rejects proves the key, the share id and the sequence number
// are all authenticated. Binding seq and id into the AAD stops a relay
// from renumbering, reordering or transplanting chunks, since each chunk
// is individually authentic.
func TestOpen_Rejects(t *testing.T) {
	k := NewKey()
	ct, err := Seal(k, "share-a", 5, []byte("secret payload"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	tampered := bytes.Clone(ct)
	tampered[len(tampered)/2] ^= 0xff

	tests := []struct {
		name string
		key  Key
		id   string
		seq  uint64
		ct   []byte
	}{
		{"different seq", k, "share-a", 6, ct},
		{"different share id", k, "share-b", 5, ct},
		{"wrong key", NewKey(), "share-a", 5, ct},
		{"tampered ciphertext", k, "share-a", 5, tampered},
		{"short ciphertext", k, "share-a", 5, []byte{0x01, 0x02}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Open(tc.key, tc.id, tc.seq, tc.ct); err == nil {
				t.Fatal("Open succeeded, want error")
			}
		})
	}
}

func TestSeal_RejectsSeqOverflow(t *testing.T) {
	k := NewKey()
	if _, err := Seal(k, "id", MaxSeq+1, []byte("x")); err == nil {
		t.Fatalf("Seal accepted seq above MaxSeq (%d)", MaxSeq)
	}
}

func TestKey_EncodeDecodeRoundTrip(t *testing.T) {
	k := NewKey()
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
	k := NewKey()
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
