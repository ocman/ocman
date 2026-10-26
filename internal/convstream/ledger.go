package convstream

import (
	"encoding/json"
	"sort"
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

// changed forgets a confirmation or tombstone the stream superseded.
func (l *ledger) changed(keys ...string) {
	for _, k := range keys {
		delete(l.confirmed, k)
		delete(l.removed, k)
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
		l.evictLocked()
	case convevents.MessageUpserted:
		l.content.message(e.Message)
		delete(l.settled, e.Message.ID)
		l.changed("m:" + e.Message.ID)
	case convevents.MessageRemoved:
		l.content.removeMessage(e.MessageID)
		delete(l.settled, e.MessageID)
		l.mark(&l.removed, "m:"+e.MessageID, now)
	case convevents.PartRemoved:
		l.content.removePart(e.PartID)
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
	return len(l.content.parts) == 0 && len(l.content.msgs) == 0 || now.Sub(l.touched) > ledgerTTL
}

func (l *ledger) evictLocked() {
	for _, id := range l.content.partOrder {
		if len(l.content.parts) <= maxLedgerParts {
			return
		}
		if l.settled[l.content.parts[id].part.MessageID] {
			l.content.removePart(id)
		}
	}
}

// compose releases what a stored read contains and lays the rest over it. It
// only applies to the newest page, the one live content belongs to.
func (l *ledger) compose(d *platforms.SessionDetail, limit, offset int, now time.Time) {
	if d == nil || offset != 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.releaseLocked(d, now)
	if len(l.content.msgs) == 0 && len(l.content.parts) == 0 && len(l.removed) == 0 {
		return
	}
	// The read may be shared with a cache; compose into copies.
	d.Messages = withoutRemoved(l.removed, d.Messages, func(m db.Message) []string { return []string{"m:" + m.ID} })
	d.Parts = withoutRemoved(l.removed, d.Parts, func(p db.Part) []string { return []string{"p:" + p.ID, "m:" + p.MessageID} })
	l.overlayLocked(d, limit)
}

func withoutRemoved[T any](removed map[string]time.Time, rows []T, keys func(T) []string) []T {
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		gone := false
		for _, k := range keys(row) {
			_, gone = removed[k]
			if gone {
				break
			}
		}
		if !gone {
			out = append(out, row)
		}
	}
	return out
}

func (l *ledger) releaseLocked(d *platforms.SessionDetail, now time.Time) {
	stored := map[string]db.Message{}
	newest := int64(-1 << 62)
	for _, m := range d.Messages {
		stored[m.ID] = m
		if _, held := l.content.msgs[m.ID]; !held && m.TimeCreated > newest {
			newest = m.TimeCreated
		}
	}
	storedParts := map[string]db.Part{}
	for _, p := range d.Parts {
		storedParts[p.ID] = p
	}
	// An ended message missing from a read that stores later messages was
	// removed while the stream missed its event.
	for _, id := range append([]string(nil), l.content.msgOrder...) {
		if _, ok := stored[id]; !ok && l.settled[id] && l.content.msgs[id].TimeCreated < newest {
			l.content.removeMessage(id)
			delete(l.settled, id)
			l.mark(&l.removed, "m:"+id, now)
		}
	}
	for _, id := range l.content.partOrder {
		lp := l.content.parts[id]
		if !l.settled[lp.part.MessageID] {
			continue
		}
		// A completed stored message owns its parts even when it keeps less
		// text (for example signature-only thinking) or omits some.
		have, ok := storedParts[id]
		if completed(stored[lp.part.MessageID].Data) || ok && covers(have.Data, lp) {
			l.mark(&l.confirmed, "p:"+id, now)
		}
	}
	for _, id := range l.content.msgOrder {
		have, ok := stored[id]
		if ok && l.settled[id] && !l.pendingPartsLocked(id) && (!completed(l.content.msgs[id].Data) || completed(have.Data)) {
			l.mark(&l.confirmed, "m:"+id, now)
		}
	}
	// Confirmed entries leave once every concurrent read has caught up.
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
	// Parts whose message is gone from both sides have nothing to attach to.
	for _, id := range append([]string(nil), l.content.partOrder...) {
		mid := l.content.parts[id].part.MessageID
		if _, ok := l.content.msgs[mid]; !ok && l.settled[mid] {
			if _, ok := stored[mid]; ok {
				continue
			}
			l.content.removePart(id)
		}
	}
}

// pendingPartsLocked reports parts of messageID no read has stored yet.
func (l *ledger) pendingPartsLocked(messageID string) bool {
	for id, lp := range l.content.parts {
		if _, ok := l.confirmed["p:"+id]; lp.part.MessageID == messageID && !ok {
			return true
		}
	}
	return false
}

func (l *ledger) overlayLocked(d *platforms.SessionDetail, limit int) {
	index := map[string]int{}
	oldest := int64(-1 << 62)
	for i, m := range d.Messages {
		index[m.ID] = i
		if limit > 0 && len(d.Messages) >= limit && (i == 0 || m.TimeCreated < oldest) {
			oldest = m.TimeCreated
		}
	}
	added := 0
	for _, id := range l.content.msgOrder {
		m := l.content.msgs[id]
		i, ok := index[id]
		switch {
		case !ok && m.TimeCreated >= oldest:
			d.Messages = append(d.Messages, m)
			added++
		case ok && completed(m.Data) && !completed(d.Messages[i].Data):
			d.Messages[i] = m
		}
	}
	if added > 0 {
		sort.SliceStable(d.Messages, func(a, b int) bool { return d.Messages[a].TimeCreated < d.Messages[b].TimeCreated })
		d.TotalMessages += added
	}
	present := map[string]bool{}
	for _, m := range d.Messages {
		present[m.ID] = true
	}
	parts := map[string]int{}
	for i, p := range d.Parts {
		parts[p.ID] = i
	}
	for _, id := range l.content.partOrder {
		lp := l.content.parts[id]
		live := lp.part.Part
		live.Data = withFields(live.Data, lp.fields)
		i, ok := parts[id]
		switch {
		case !ok && present[live.MessageID]:
			d.Parts = append(d.Parts, live)
		case ok && lp.final && !covers(d.Parts[i].Data, lp):
			d.Parts[i] = live
		case ok:
			d.Parts[i].Data = withFields(d.Parts[i].Data, lp.fields)
		}
	}
}

// covers reports whether a stored part holds every streamed field at least
// as long as the ledger's and, for a final part, the same tool status.
func covers(stored json.RawMessage, lp *livePart) bool {
	have := streamedFields(stored)
	for field, value := range lp.fields {
		if convevents.UTF16Len(have[field]) < convevents.UTF16Len(value) {
			return false
		}
	}
	return !lp.final || readPart(stored).State.Status == readPart(lp.part.Data).State.Status
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
