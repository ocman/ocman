package convstream

import (
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Ledger bounds.
const (
	// ledgerTTL keeps a detached session's unconfirmed content for revisits.
	ledgerTTL = 10 * time.Minute
	// maxLedgerParts evicts the oldest settled parts beyond this count.
	maxLedgerParts = 2048
	// maxLedgerBytes bounds every ledger together; ended turns of sessions
	// nobody watches are evicted first. A running turn is never evicted.
	maxLedgerBytes = 64 << 20
	// releaseGrace keeps confirmed entries in reads that queried history
	// before the confirming read, since reads and composition are not atomic.
	releaseGrace = 30 * time.Second
)

// ledger is the content a session's stream delivered that stored history has
// not confirmed yet. Stored history is ingested after the live stream, so a
// conversation read composes both (Hub.Compose). Unlike the connection's
// overlay, it outlives idle edges, source restarts and detaching, and only a
// read that stores an entry at least as completely releases it.
type ledger struct {
	mu      sync.Mutex
	content inflight
	// settled names messages whose turn ended; their parts are settled too.
	settled map[string]bool
	// confirmed records when a read first stored an entry ("m:" or "p:" id).
	confirmed map[string]time.Time
	// removed tombstones entries the stream removed, so a read taken before
	// the removal cannot restore them.
	removed map[string]time.Time
	// rev counts changes per entry, so a read composed from an older capture
	// cannot release newer content.
	rev     map[string]uint64
	touched time.Time
}

func (l *ledger) mark(set *map[string]time.Time, key string, now time.Time) {
	if *set == nil {
		*set = map[string]time.Time{}
	}
	if _, ok := (*set)[key]; !ok {
		(*set)[key] = now
	}
}

// changed records new content: earlier confirmations and tombstones no
// longer describe it.
func (l *ledger) changed(keys ...string) {
	if l.rev == nil {
		l.rev = map[string]uint64{}
	}
	for _, k := range keys {
		delete(l.confirmed, k)
		delete(l.removed, k)
		l.rev[k]++
	}
}

func (l *ledger) apply(ev convevents.Event, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch e := ev.(type) {
	case convevents.PartDelta:
		l.content.delta(e)
		l.changed("p:" + e.PartID)
	case convevents.PartUpserted:
		l.content.upsert(e.Part, e.Final)
		delete(l.settled, e.Part.MessageID)
		l.changed("p:"+e.Part.ID, "m:"+e.Part.MessageID)
		l.evictPartsLocked(maxLedgerParts)
	case convevents.MessageUpserted:
		l.content.message(e.Message)
		delete(l.settled, e.Message.ID)
		l.changed("m:" + e.Message.ID)
	case convevents.MessageRemoved:
		l.content.removeMessage(e.MessageID)
		delete(l.settled, e.MessageID)
		l.changed("m:" + e.MessageID)
		l.mark(&l.removed, "m:"+e.MessageID, now)
	case convevents.PartRemoved:
		l.content.removePart(e.PartID)
		l.changed("p:" + e.PartID)
		l.mark(&l.removed, "p:"+e.PartID, now)
	case convevents.HistoryRewritten:
		// A rewrite is authoritative for ended turns; a running turn continues.
		for id := range l.settled {
			l.content.removeMessage(id)
		}
		l.settled = nil
	default:
		return
	}
	l.touched = now
}

// touch restarts the expiry, for example when the last viewer leaves.
func (l *ledger) touch(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = now
}

// settle marks the ended turn's content; it stays until history stores it.
func (l *ledger) settle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.settled == nil {
		l.settled = map[string]bool{}
	}
	for _, id := range l.content.msgOrder {
		l.settled[id] = true
	}
	for _, lp := range l.content.parts {
		l.settled[lp.part.MessageID] = true
	}
}

func (l *ledger) snapshot() convevents.Inflight {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.content.snapshot()
}

func (l *ledger) expired(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.content.parts) == 0 && len(l.content.msgs) == 0 && len(l.removed) == 0 || now.Sub(l.touched) > ledgerTTL
}

// size approximates the retained bytes without walking content.
func (l *ledger) size() (bytes int, touched time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, lp := range l.content.parts {
		bytes += lp.bytes()
	}
	for _, m := range l.content.msgs {
		bytes += len(m.Data)
	}
	return bytes, l.touched
}

// evict drops the oldest settled message groups (a message with its parts)
// until at most keep bytes remain, and returns the bytes it freed. Running
// content is never evicted.
func (l *ledger) evict(keep int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	held := 0
	groups := map[string]int{}
	var order []string
	add := func(id string, n int) {
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] += n
		held += n
	}
	for _, id := range l.content.msgOrder {
		add(id, len(l.content.msgs[id].Data))
	}
	for _, id := range l.content.partOrder {
		lp := l.content.parts[id]
		add(lp.part.MessageID, lp.bytes())
	}
	freed := 0
	for _, id := range order {
		if held-freed <= keep {
			break
		}
		if l.settled[id] {
			freed += groups[id]
			l.content.removeMessage(id)
			delete(l.settled, id)
		}
	}
	return freed
}

// evictPartsLocked drops the oldest settled parts beyond maxParts.
func (l *ledger) evictPartsLocked(maxParts int) {
	for _, id := range slices.Clone(l.content.partOrder) {
		if len(l.content.parts) <= maxParts {
			return
		}
		if lp := l.content.parts[id]; l.settled[lp.part.MessageID] {
			l.content.removePart(id)
		}
	}
}

// capture copies what composing a read needs, so the JSON work runs without
// holding the session lock that serialises its stream. Entries confirmed
// longer than the grace period ago are already released.
func (l *ledger) capture(now time.Time) *ledgerView {
	l.mu.Lock()
	defer l.mu.Unlock()
	released := func(key string) bool {
		at, ok := l.confirmed[key]
		return ok && now.Sub(at) >= releaseGrace
	}
	v := &ledgerView{msgs: map[string]db.Message{}, settled: maps.Clone(l.settled), confirmed: maps.Clone(l.confirmed),
		removed: maps.Clone(l.removed), rev: maps.Clone(l.rev), parts: make([]capturedPart, 0, len(l.content.partOrder))}
	for _, id := range l.content.msgOrder {
		if !released("m:" + id) {
			v.msgOrder = append(v.msgOrder, id)
			v.msgs[id] = l.content.msgs[id]
		}
	}
	for _, id := range l.content.partOrder {
		if lp := l.content.parts[id]; !released("p:" + id) {
			v.parts = append(v.parts, capturedPart{part: lp.part.Part, values: lp.values(), final: lp.final})
		}
	}
	return v
}

// compose composes one read of a ledger no session lock guards.
func (l *ledger) compose(d *platforms.SessionDetail, limit, offset int, now time.Time) []string {
	plan, added := l.capture(now).compose(d, limit, offset)
	l.commit(plan, now)
	return added
}

// commit applies a composed read's decisions to entries it saw unchanged,
// then releases confirmations older than the grace period.
func (l *ledger) commit(p composePlan, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	same := func(key string, rev uint64) bool { return l.rev[key] == rev }
	for id, rev := range p.removedMessages {
		if same("m:"+id, rev) && l.settled[id] {
			l.content.removeMessage(id)
			delete(l.settled, id)
			l.mark(&l.removed, "m:"+id, now)
		}
	}
	for key, rev := range p.confirmed {
		if same(key, rev) {
			l.mark(&l.confirmed, key, now)
		}
	}
	for id, rev := range p.orphans {
		if same("p:"+id, rev) {
			l.content.removePart(id)
		}
	}
	for key, at := range l.confirmed {
		if now.Sub(at) < releaseGrace {
			continue
		}
		delete(l.confirmed, key)
		switch id := key[2:]; key[:2] {
		case "p:":
			l.content.removePart(id)
		case "m:":
			delete(l.content.msgs, id)
			l.content.msgOrder = without(l.content.msgOrder, id)
			delete(l.settled, id)
		}
	}
	for key, at := range l.removed {
		if now.Sub(at) > ledgerTTL {
			delete(l.removed, key)
		}
	}
	withParts := map[string]bool{}
	for _, lp := range l.content.parts {
		withParts[lp.part.MessageID] = true
	}
	for key := range l.rev {
		if _, ok := l.removed[key]; ok {
			continue
		}
		id := key[2:]
		_, msg := l.content.msgs[id]
		if key[:2] == "p:" && l.content.parts[id] == nil || key[:2] == "m:" && !msg && !withParts[id] {
			delete(l.rev, key)
		}
	}
}

func completed(data json.RawMessage) bool {
	var m struct {
		Finish string `json:"finish"`
		Time   struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
	}
	return json.Unmarshal(data, &m) == nil && (m.Finish != "" || m.Time.Completed != 0)
}
