package queuesvc

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newID returns a short random queued-message identifier.
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "q_" + hex.EncodeToString(b[:])
}

func nowMillis() int64 { return time.Now().UnixMilli() }
