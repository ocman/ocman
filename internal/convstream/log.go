package convstream

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
)

// Per-session log bounds (design §6.4).
const (
	maxLogBytes   = 4 << 20
	maxLogEntries = 20000
)

type logEntry struct {
	seq   uint64
	frame []byte
	// key is the supersession key this entry registered under, if any.
	key     string
	dropped bool
}

// sessionLog is the bounded replay log of one epoch. Entries superseded by
// a later entry are dropped (compacted); the oldest are evicted (trimmed).
// A cursor is resumable while it is at or after floor, the newest evicted
// live entry: every compacted entry has its superseder later in the log.
type sessionLog struct {
	epoch   string
	last    uint64
	floor   uint64
	entries []logEntry
	bytes   int
	live    int
	dropped int
	byKey   map[string][]uint64
	// maxBytes and maxEntries default to the package bounds.
	maxBytes, maxEntries int
}

func newLog() *sessionLog {
	return &sessionLog{epoch: newEpoch(), byKey: map[string][]uint64{}, maxBytes: maxLogBytes, maxEntries: maxLogEntries}
}

func newEpoch() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// next reserves the next sequence number.
func (l *sessionLog) next() uint64 {
	l.last++
	return l.last
}

// append records frame at seq after dropping every entry registered under
// the supersedes keys. It returns the change in retained bytes.
func (l *sessionLog) append(seq uint64, frame []byte, supersedes []string, key string) int {
	before := l.bytes
	for _, k := range supersedes {
		for _, s := range l.byKey[k] {
			l.drop(s)
		}
		delete(l.byKey, k)
	}
	l.entries = append(l.entries, logEntry{seq: seq, frame: frame, key: key})
	l.bytes += len(frame)
	l.live++
	if key != "" {
		l.byKey[key] = append(l.byKey[key], seq)
	}
	l.trim(l.maxBytes, l.maxEntries)
	l.repack()
	return l.bytes - before
}

func (l *sessionLog) index(seq uint64) int {
	i := sort.Search(len(l.entries), func(i int) bool { return l.entries[i].seq >= seq })
	if i < len(l.entries) && l.entries[i].seq == seq {
		return i
	}
	return -1
}

func (l *sessionLog) drop(seq uint64) {
	i := l.index(seq)
	if i < 0 || l.entries[i].dropped {
		return
	}
	e := &l.entries[i]
	l.bytes -= len(e.frame)
	e.frame, e.dropped, e.key = nil, true, ""
	l.live--
	l.dropped++
}

// trim evicts the oldest entries until the log is within bounds, trimming
// to 90% at once so a full log does not shift on every append.
func (l *sessionLog) trim(maxBytes, maxEntries int) {
	if l.bytes <= maxBytes && l.live <= maxEntries {
		return
	}
	targetBytes, targetEntries := maxBytes*9/10, maxEntries*9/10
	n := 0
	for n < len(l.entries) && (l.bytes > targetBytes || l.live > targetEntries) {
		l.evict(&l.entries[n])
		n++
	}
	l.entries = append([]logEntry(nil), l.entries[n:]...)
}

func (l *sessionLog) evict(e *logEntry) {
	if e.dropped {
		l.dropped--
		return
	}
	l.floor = e.seq
	l.bytes -= len(e.frame)
	l.live--
	if e.key != "" {
		seqs := l.byKey[e.key]
		for i, s := range seqs {
			if s == e.seq {
				seqs = append(seqs[:i:i], seqs[i+1:]...)
				break
			}
		}
		if len(seqs) == 0 {
			delete(l.byKey, e.key)
		} else {
			l.byKey[e.key] = seqs
		}
	}
}

// repack removes dropped entries once they are most of the slice.
func (l *sessionLog) repack() {
	if l.dropped < 64 || l.dropped*2 < len(l.entries) {
		return
	}
	kept := make([]logEntry, 0, l.live)
	for _, e := range l.entries {
		if !e.dropped {
			kept = append(kept, e)
		}
	}
	l.entries, l.dropped = kept, 0
}

// resumable reports whether a client at epoch:seq can continue from the log.
func (l *sessionLog) resumable(epoch string, seq uint64) bool {
	return epoch == l.epoch && seq >= l.floor && seq <= l.last
}

// after returns the retained entries with seq greater than seq.
func (l *sessionLog) after(seq uint64) []frameMsg {
	i := sort.Search(len(l.entries), func(i int) bool { return l.entries[i].seq > seq })
	var out []frameMsg
	for _, e := range l.entries[i:] {
		if !e.dropped {
			out = append(out, frameMsg{seq: e.seq, frame: e.frame})
		}
	}
	return out
}
