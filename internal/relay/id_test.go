package relay

import (
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/share"
)

func TestNewID_RoundTripsThroughSplit(t *testing.T) {
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	id, err := newID(now)
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	date, random, ok := splitID(id)
	if !ok {
		t.Fatalf("splitID(%q) failed", id)
	}
	if date != "20260813" {
		t.Fatalf("date = %q, want 20260813", date)
	}
	if random == "" {
		t.Fatal("random component is empty")
	}
}

func TestNewID_UsesUTC(t *testing.T) {
	// 00:30 on the 14th in +02:00 is 22:30 on the 13th UTC. The date
	// partition must be UTC or a share would be filed under a day the
	// sweeper computes differently.
	zone := time.FixedZone("test", 2*60*60)
	id, err := newID(time.Date(2026, 8, 14, 0, 30, 0, 0, zone))
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	if !strings.HasPrefix(id, "20260813-") {
		t.Fatalf("id = %q, want the UTC date 20260813", id)
	}
}

func TestNewID_IsUnique(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	for range 100 {
		id, err := newID(now)
		if err != nil {
			t.Fatalf("newID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestSplitID_Rejects(t *testing.T) {
	valid, err := newID(time.Now())
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	_, random, _ := splitID(valid)

	tests := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"no separator", "20260813" + random},
		{"short date", "2026813-" + random},
		{"non numeric date", "yyyymmdd-" + random},
		{"impossible date", "20261345-" + random},
		{"empty random", "20260813-"},
		{"short random", "20260813-abc"},
		{"non base64url random", strings.Repeat("!", 22)},
		{"traversal", "../../etc-passwd"},
		{"slash injection", "20260813-aa/bb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := splitID(tc.id); ok {
				t.Fatalf("splitID(%q) accepted an invalid id", tc.id)
			}
			if validID(tc.id) {
				t.Fatalf("validID(%q) = true", tc.id)
			}
		})
	}
}

// TestKeysAreValidStoreKeys ties id derivation to the storage contract:
// anything prefixFor/chunkKey/metaKey produce must be accepted by the
// Store, and anything rejected as an id must never reach it.
func TestKeysAreValidStoreKeys(t *testing.T) {
	id, err := newID(time.Now())
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	mk, ok := metaKey(id)
	if !ok {
		t.Fatal("metaKey failed for a fresh id")
	}
	if err := share.ValidateKey(mk); err != nil {
		t.Fatalf("meta key %q rejected by store: %v", mk, err)
	}
	ck, ok := chunkKey(id, 42)
	if !ok {
		t.Fatal("chunkKey failed for a fresh id")
	}
	if err := share.ValidateKey(ck); err != nil {
		t.Fatalf("chunk key %q rejected by store: %v", ck, err)
	}
	prefix, ok := prefixFor(id)
	if !ok {
		t.Fatal("prefixFor failed for a fresh id")
	}
	if err := share.ValidatePrefix(prefix); err != nil {
		t.Fatalf("prefix %q rejected by store: %v", prefix, err)
	}
}

func TestChunkKey_IsFixedWidthAndSortable(t *testing.T) {
	id, err := newID(time.Now())
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	low, _ := chunkKey(id, 9)
	high, _ := chunkKey(id, 10)
	if low >= high {
		t.Fatalf("chunk keys do not sort numerically: %q >= %q", low, high)
	}
	if !strings.HasSuffix(low, "000000009") {
		t.Fatalf("chunk key %q is not zero padded", low)
	}
}

func TestSeqFromKey(t *testing.T) {
	id, err := newID(time.Now())
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	ck, _ := chunkKey(id, 123)
	seq, ok := seqFromKey(ck)
	if !ok || seq != 123 {
		t.Fatalf("seqFromKey(%q) = %d, %v; want 123, true", ck, seq, ok)
	}

	// The meta object shares the prefix and must never be mistaken for
	// a chunk, or it would be handed to the viewer as ciphertext.
	mk, _ := metaKey(id)
	if _, ok := seqFromKey(mk); ok {
		t.Fatalf("seqFromKey(%q) treated the meta object as a chunk", mk)
	}
	if _, ok := seqFromKey("noslash"); ok {
		t.Fatal("seqFromKey accepted a key with no prefix")
	}
}

func TestDatePrefix(t *testing.T) {
	got := datePrefix(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if got != "20260102/" {
		t.Fatalf("datePrefix = %q, want 20260102/", got)
	}
}
