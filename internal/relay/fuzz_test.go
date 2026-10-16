package relay

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/share"
)

// The only shapes an accepted share or delivery id may have: the base64
// decoder skips CR/LF, so a decode check alone is not enough.
var (
	wellFormedShareID    = regexp.MustCompile(`^[0-9]{8}-[A-Za-z0-9_-]{22}$`)
	wellFormedDeliveryID = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
)

// FuzzRelayIDs checks the boundary that keeps URL input out of storage
// keys. An accepted share id (or delivery id) has exactly the generated
// shape, maps to valid storage keys whose chunk sequence numbers round-trip,
// and every generated id is accepted. A share chunk sealed under an id and
// sequence number opens only under that id and number, and only unmodified.
func FuzzRelayIDs(f *testing.F) {
	f.Add("20260813-AAAAAAAAAAAAAAAAAAAAAA", uint64(3))
	f.Add("20260813-../../AAAAAAAAAAAAAAAAA", uint64(0))
	f.Add("20260813-AAAAAAAAAAAAAAAA\r\r\r\r\r\r", uint64(1))
	f.Add("20261332-AAAAAAAAAAAAAAAAAAAAAA", share.MaxSeq)
	f.Add("AAAAAAAAAAAAAAAAAAAAAA", share.MaxSeq+1)
	f.Fuzz(func(t *testing.T, id string, seq uint64) {
		if validDeliveryID(id) && !wellFormedDeliveryID.MatchString(id) {
			t.Fatalf("delivery id %q accepted", id)
		}
		if validID(id) {
			checkShareKeys(t, id, seq)
		} else if _, ok := prefixFor(id); ok {
			t.Fatalf("prefix for invalid id %q", id)
		}
		sum := sha256.Sum256([]byte(id))
		generated := "20260813-" + base64.RawURLEncoding.EncodeToString(sum[:idRandomBytes])
		if !validID(generated) || !validDeliveryID(generated[9:]) {
			t.Fatalf("generated id %q rejected", generated)
		}
		checkShareKeys(t, generated, seq)
		checkSealOpen(t, share.Key(sum), id, seq)
	})
}

func checkShareKeys(t *testing.T, id string, seq uint64) {
	t.Helper()
	if !wellFormedShareID.MatchString(id) {
		t.Fatalf("malformed share id %q accepted", id)
	}
	prefix, ok := prefixFor(id)
	if !ok || share.ValidatePrefix(prefix) != nil {
		t.Fatalf("invalid prefix %q for %q", prefix, id)
	}
	meta, _ := metaKey(id)
	if share.ValidateKey(meta) != nil || !strings.HasPrefix(meta, prefix) {
		t.Fatalf("invalid meta key %q for %q", meta, id)
	}
	if _, chunk := seqFromKey(meta); chunk {
		t.Fatalf("meta key %q parses as a chunk", meta)
	}
	if seq > share.MaxSeq {
		return
	}
	chunk, _ := chunkKey(id, seq)
	if share.ValidateKey(chunk) != nil || !strings.HasPrefix(chunk, prefix) {
		t.Fatalf("invalid chunk key %q for %q", chunk, id)
	}
	if got, ok := seqFromKey(chunk); !ok || got != seq {
		t.Fatalf("seqFromKey(%q) = %d, %v; want %d", chunk, got, ok, seq)
	}
}

func checkSealOpen(t *testing.T, k share.Key, id string, seq uint64) {
	t.Helper()
	if parsed, err := share.ParseKey(k.String()); err != nil || parsed != k {
		t.Fatalf("key does not round-trip: %v", err)
	}
	plain := []byte(id)
	sealed, err := share.Seal(k, id, seq, plain)
	if seq > share.MaxSeq {
		if err == nil {
			t.Fatalf("sealed seq %d beyond MaxSeq", seq)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if got, err := share.Open(k, id, seq, sealed); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open after seal: %q, %v", got, err)
	}
	if seq < share.MaxSeq {
		if _, err := share.Open(k, id, seq+1, sealed); !errors.Is(err, share.ErrOpen) {
			t.Fatalf("chunk %d opened as %d: %v", seq, seq+1, err)
		}
	}
	if _, err := share.Open(k, id+"x", seq, sealed); !errors.Is(err, share.ErrOpen) {
		t.Fatalf("chunk opened under another share id: %v", err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(id)%len(tampered)] ^= 0x01
	if _, err := share.Open(k, id, seq, tampered); !errors.Is(err, share.ErrOpen) {
		t.Fatalf("tampered chunk opened: %v", err)
	}
}
