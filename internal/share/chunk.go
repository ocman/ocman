// Package share implements the wire format and storage abstraction
// behind ocman's cross-machine conversation sharing.
//
// A share is an append-only log of independently sealed chunks. Chunk 0
// is a full snapshot of a conversation; every later chunk is an upsert
// delta carrying the messages and parts of one completed turn. Because
// every chunk is an upsert, replaying from seq 0 always reconstructs the
// current state, and a writer that lost its bookkeeping can simply push
// a fresh full chunk.
//
// Chunks are sealed with AES-256-GCM under a per-share key that never
// reaches the relay: it lives in the sharing instance's state database
// and in the viewer's URL fragment, which browsers do not transmit. The
// relay therefore stores ciphertext it cannot read.
//
// Two properties of the sealing are load-bearing and must not be
// "simplified" later:
//
//   - The nonce is derived from the sequence number, never random. The
//     sequence is allocated by the single writer and is monotonic, so
//     uniqueness is guaranteed by construction. This also makes Seal
//     deterministic, which is what allows a retried append to be a
//     byte-identical overwrite.
//   - The share id and sequence number are bound into the additional
//     authenticated data. Individually authentic chunks do not make the
//     *sequence* authentic; without this a relay could reorder, renumber,
//     or transplant chunks between shares undetected.
package share

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// nonceSize is the standard AES-GCM nonce length in bytes.
const nonceSize = 12

// MaxSeq is the highest usable chunk sequence number. The nonce carries
// the sequence in its low 8 bytes, so the ceiling is really uint64, but
// capping it well below that keeps sequence numbers renderable as
// fixed-width, lexicographically sortable object keys.
const MaxSeq = uint64(999999999)

// ErrOpen reports a chunk that failed to authenticate: wrong key, wrong
// share, wrong sequence, or tampered bytes. Callers must not distinguish
// these — all of them mean the chunk is unusable.
var ErrOpen = errors.New("share: chunk authentication failed")

// Key is the symmetric key protecting a single share.
type Key [KeySize]byte

// NewKey returns a cryptographically random share key.
func NewKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("share: generating key: %w", err)
	}
	return k, nil
}

// String encodes the key as unpadded base64url, the form carried in a
// share URL's fragment.
func (k Key) String() string {
	return base64.RawURLEncoding.EncodeToString(k[:])
}

// ParseKey decodes a key produced by Key.String.
func ParseKey(s string) (Key, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("share: decoding key: %w", err)
	}
	if len(b) != KeySize {
		return Key{}, fmt.Errorf("share: key is %d bytes, want %d", len(b), KeySize)
	}
	var k Key
	copy(k[:], b)
	return k, nil
}

// Seal encrypts and authenticates one chunk of a share.
//
// The result is deterministic for a given (key, id, seq, plaintext), so
// re-uploading a chunk after a failed request writes identical bytes.
func Seal(k Key, id string, seq uint64, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	if seq > MaxSeq {
		return nil, fmt.Errorf("share: seq %d exceeds maximum %d", seq, MaxSeq)
	}
	nonce := nonceFor(seq)
	return gcm.Seal(nil, nonce[:], plaintext, aad(id, seq)), nil
}

// Open authenticates and decrypts one chunk. It returns ErrOpen for any
// failure, including a chunk presented under a different share id or
// sequence number than it was sealed with.
func Open(k Key, id string, seq uint64, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	if seq > MaxSeq {
		return nil, fmt.Errorf("share: seq %d exceeds maximum %d", seq, MaxSeq)
	}
	nonce := nonceFor(seq)
	plain, err := gcm.Open(nil, nonce[:], ciphertext, aad(id, seq))
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}

// newGCM builds the AEAD for a share key.
func newGCM(k Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("share: building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("share: building GCM: %w", err)
	}
	return gcm, nil
}

// nonceFor derives the GCM nonce for a sequence number: the sequence in
// big-endian in the low 8 bytes, leading bytes zero. Deliberately not
// random — see the package comment.
func nonceFor(seq uint64) [nonceSize]byte {
	var n [nonceSize]byte
	binary.BigEndian.PutUint64(n[nonceSize-8:], seq)
	return n
}

// aad binds a chunk to its share and position, so it cannot be reordered,
// renumbered, or moved to another share without failing authentication.
func aad(id string, seq uint64) []byte {
	b := make([]byte, 0, len(id)+8)
	b = append(b, id...)
	return binary.BigEndian.AppendUint64(b, seq)
}
