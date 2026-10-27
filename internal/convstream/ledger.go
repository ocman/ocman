package convstream

import (
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
)

// Ledger bounds.
const (
	// ledgerTTL is a backstop for content no read ever confirms.
	ledgerTTL = 10 * time.Minute
	// maxLedgerParts and maxLedgerMessages evict the oldest settled entries
	// beyond these counts.
	maxLedgerParts    = 2048
	maxLedgerMessages = 512
	// maxLedgerBytes bounds every ledger together (Hub.budgetLocked).
	maxLedgerBytes = 64 << 20
)

// ledger is the content a session's stream delivered that stored history has
// not confirmed yet. Stored history is ingested after the live stream, so a
// conversation read composes both (Hub.BeginRead). Unlike the connection's
// overlay, it outlives idle edges, source restarts and detaching.
//
// Reads take a ticket before they query history. History only moves forward,
// so a read that starts after a confirming read stores what it confirmed:
// confirmed entries and cleared tombstones apply to earlier tickets only, and
// leave once no earlier read is still open. No wall clock is involved.
type ledger struct {
	mu      sync.Mutex
	content inflight
	// settled names messages whose turn ended; their parts are settled too.
	settled map[string]bool
	// confirmed is the last ticket issued when a read first stored an entry
	// ("m:" or "p:" id).
	confirmed map[string]uint64
	// removed tombstones entries the stream removed, so a read of history
	// that still holds them cannot restore them.
	removed map[string]tombstone
	// rev counts changes per entry, so a read composed from an older capture
	// cannot confirm newer content.
	rev     map[string]uint64
	issued  uint64
	open    map[uint64]struct{}
	touched time.Time
}

type tombstone struct {
	at time.Time
	// cleared is the last ticket issued when a read no longer held the row;
	// later reads trust history again. 0 while history still holds it.
	cleared uint64
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

func (l *ledger) tomb(key string, now time.Time) {
	if l.removed == nil {
		l.removed = map[string]tombstone{}
	}
	l.removed[key] = tombstone{at: now}
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
		l.evictCountsLocked()
	case convevents.MessageUpserted:
		l.content.message(e.Message)
		delete(l.settled, e.Message.ID)
		l.changed("m:" + e.Message.ID)
		l.evictCountsLocked()
	case convevents.MessageRemoved:
		l.content.removeMessage(e.MessageID)
		delete(l.settled, e.MessageID)
		l.changed("m:" + e.MessageID)
		l.tomb("m:"+e.MessageID, now)
	case convevents.PartRemoved:
		l.content.removePart(e.PartID)
		l.changed("p:" + e.PartID)
		l.tomb("p:"+e.PartID, now)
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
	return len(l.open) == 0 && (len(l.content.parts) == 0 && len(l.content.msgs) == 0 && len(l.removed) == 0 || now.Sub(l.touched) > ledgerTTL)
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

// begin issues a read ticket; end must follow.
func (l *ledger) begin() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open == nil {
		l.open = map[uint64]struct{}{}
	}
	l.issued++
	l.open[l.issued] = struct{}{}
	return l.issued
}

func (l *ledger) end(ticket uint64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.open, ticket)
	l.purgeLocked(now)
}

// oldestOpenLocked is the earliest read still open, or the next ticket.
func (l *ledger) oldestOpenLocked() uint64 {
	oldest := l.issued + 1
	for t := range l.open {
		oldest = min(oldest, t)
	}
	return oldest
}

// purgeLocked drops confirmations and cleared tombstones no open read
// predates, and bookkeeping for entries that are gone.
func (l *ledger) purgeLocked(now time.Time) {
	oldest := l.oldestOpenLocked()
	for key, at := range l.confirmed {
		if at >= oldest {
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
	for key, t := range l.removed {
		if t.cleared != 0 && t.cleared < oldest || t.cleared == 0 && now.Sub(t.at) > ledgerTTL {
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

// groupsLocked lists message groups (a message with its parts) oldest first
// with their bytes.
func (l *ledger) groupsLocked() (order []string, bytes map[string]int, held int) {
	bytes = map[string]int{}
	add := func(id string, n int) {
		if _, ok := bytes[id]; !ok {
			order = append(order, id)
		}
		bytes[id] += n
		held += n
	}
	for _, id := range l.content.msgOrder {
		add(id, len(l.content.msgs[id].Data))
	}
	for _, id := range l.content.partOrder {
		lp := l.content.parts[id]
		add(lp.part.MessageID, lp.bytes())
	}
	return order, bytes, held
}

// evict drops message groups until at most keep bytes remain and returns
// the bytes it freed: confirmed ended groups first, then other ended groups,
// and running groups only when force is set.
func (l *ledger) evict(keep int, force bool) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	order, bytes, held := l.groupsLocked()
	freed := 0
	for _, eligible := range []func(string) bool{
		func(id string) bool { _, ok := l.confirmed["m:"+id]; return ok && l.settled[id] },
		func(id string) bool { return l.settled[id] },
		func(string) bool { return force },
	} {
		for _, id := range order {
			if held-freed <= keep {
				return freed
			}
			if _, present := bytes[id]; present && eligible(id) {
				freed += bytes[id]
				delete(bytes, id)
				l.content.removeMessage(id)
				delete(l.settled, id)
			}
		}
	}
	return freed
}

// evictCountsLocked drops the oldest settled parts and message groups beyond
// the per-ledger counts.
func (l *ledger) evictCountsLocked() {
	for _, id := range slices.Clone(l.content.partOrder) {
		if len(l.content.parts) <= maxLedgerParts {
			break
		}
		if lp := l.content.parts[id]; l.settled[lp.part.MessageID] {
			l.content.removePart(id)
		}
	}
	for _, id := range slices.Clone(l.content.msgOrder) {
		if len(l.content.msgOrder) <= maxLedgerMessages {
			break
		}
		if l.settled[id] {
			l.content.removeMessage(id)
			delete(l.settled, id)
		}
	}
}

// capture copies what composing read ticket needs, so the JSON work runs
// without holding the session lock that serialises its stream. Entries
// confirmed before the ticket was issued are history's already.
func (l *ledger) capture(ticket uint64) *ledgerView {
	l.mu.Lock()
	defer l.mu.Unlock()
	known := func(key string) bool {
		at, ok := l.confirmed[key]
		return ok && at < ticket
	}
	v := &ledgerView{msgs: map[string]db.Message{}, settled: maps.Clone(l.settled), confirmed: map[string]bool{},
		removed: map[string]bool{}, rev: maps.Clone(l.rev), parts: make([]capturedPart, 0, len(l.content.partOrder))}
	for key := range l.confirmed {
		v.confirmed[key] = true
	}
	for key, t := range l.removed {
		// Only cleared after this read started: history may still hold it.
		v.removed[key] = t.cleared == 0 || ticket <= t.cleared
	}
	for _, id := range l.content.msgOrder {
		if !known("m:" + id) {
			v.msgOrder = append(v.msgOrder, id)
			v.msgs[id] = l.content.msgs[id]
		}
	}
	for _, id := range l.content.partOrder {
		if lp := l.content.parts[id]; !known("p:" + id) {
			v.parts = append(v.parts, capturedPart{part: lp.part.Part, values: lp.values(), final: lp.final})
		}
	}
	return v
}

// commit applies a composed read's decisions to entries it saw unchanged.
func (l *ledger) commit(p composePlan) {
	l.mu.Lock()
	defer l.mu.Unlock()
	same := func(key string, rev uint64) bool { return l.rev[key] == rev }
	for id, rev := range p.dropped {
		// Inferred from history, not a stream removal: forget it, no tombstone.
		if same("m:"+id, rev) && l.settled[id] {
			l.content.removeMessage(id)
			delete(l.settled, id)
		}
	}
	for key, rev := range p.confirmed {
		if _, ok := l.confirmed[key]; !ok && same(key, rev) {
			if l.confirmed == nil {
				l.confirmed = map[string]uint64{}
			}
			l.confirmed[key] = l.issued
		}
	}
	for key := range p.cleared {
		if t, ok := l.removed[key]; ok && t.cleared == 0 {
			t.cleared = l.issued
			l.removed[key] = t
		}
	}
	for id, rev := range p.orphans {
		if same("p:"+id, rev) {
			l.content.removePart(id)
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
