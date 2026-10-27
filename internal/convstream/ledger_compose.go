package convstream

import (
	"sort"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ledgerView is a ledger captured for composing one read.
type ledgerView struct {
	msgOrder  []string
	msgs      map[string]db.Message
	parts     []capturedPart
	settled   map[string]bool
	confirmed map[string]bool
	// removed names tombstones; true filters this read's history rows.
	removed map[string]bool
	rev     map[string]uint64
}

type capturedPart struct {
	part   db.Part // without streamed values
	values map[string]string
	final  bool
}

// composePlan is what a read decided about the captured entries, keyed with
// the revision it saw.
type composePlan struct {
	confirmed map[string]uint64
	// dropped are ended messages history moved past without them.
	dropped map[string]uint64
	// cleared are tombstones whose rows this read no longer holds.
	cleared map[string]bool
	orphans map[string]uint64
}

// compose lays the captured content over a stored read of the newest page
// and decides what the read confirms. It returns the plan and the messages it
// added, which are not stored history.
func (v *ledgerView) compose(d *platforms.SessionDetail, limit, offset int) (composePlan, []string) {
	p := composePlan{confirmed: map[string]uint64{}, dropped: map[string]uint64{}, cleared: map[string]bool{}, orphans: map[string]uint64{}}
	if d == nil || offset != 0 {
		return p, nil
	}
	v.decide(d, &p)
	if len(v.msgs) == 0 && len(v.parts) == 0 && len(v.removed) == 0 {
		return p, nil
	}
	// The read may be shared with a cache; compose into copies.
	d.Messages = withoutRemoved(v.removed, d.Messages, func(m db.Message) []string { return []string{"m:" + m.ID} })
	d.Parts = withoutRemoved(v.removed, d.Parts, func(p db.Part) []string { return []string{"p:" + p.ID, "m:" + p.MessageID} })
	return p, v.overlay(d, limit, p.dropped)
}

func withoutRemoved[T any](removed map[string]bool, rows []T, keys func(T) []string) []T {
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		gone := false
		for _, k := range keys(row) {
			if gone = removed[k]; gone {
				break
			}
		}
		if !gone {
			out = append(out, row)
		}
	}
	return out
}

func (v *ledgerView) decide(d *platforms.SessionDetail, p *composePlan) {
	stored := map[string]db.Message{}
	done := map[string]bool{}
	newest := int64(-1 << 62)
	for _, m := range d.Messages {
		stored[m.ID] = m
		done[m.ID] = completed(m.Data)
		if _, held := v.msgs[m.ID]; !held && m.TimeCreated > newest {
			newest = m.TimeCreated
		}
	}
	storedParts := map[string]db.Part{}
	for _, sp := range d.Parts {
		storedParts[sp.ID] = sp
	}
	// History that no longer holds a removed row has caught up with the
	// removal; later reads trust it again, including a re-added row.
	for key := range v.removed {
		_, msg := stored[key[2:]]
		_, part := storedParts[key[2:]]
		if key[:2] == "m:" && !msg || key[:2] == "p:" && !part {
			p.cleared[key] = true
		}
	}
	// An ended message missing from a read that stores later messages was
	// probably removed while the stream missed it. Ingestion order is not
	// guaranteed, so this only forgets the ledger copy; history still decides.
	for _, id := range v.msgOrder {
		if _, ok := stored[id]; !ok && v.settled[id] && v.msgs[id].TimeCreated < newest {
			p.dropped[id] = v.rev["m:"+id]
		}
	}
	pending := map[string]bool{}
	for _, cp := range v.parts {
		mid := cp.part.MessageID
		_, gone := p.dropped[mid]
		_, held := v.msgs[mid]
		_, inRead := stored[mid]
		switch {
		case gone:
		case !held && !inRead && v.settled[mid]:
			// Nothing to attach to on either side.
			p.orphans[cp.part.ID] = v.rev["p:"+cp.part.ID]
		case !v.settled[mid]:
			pending[mid] = true
		default:
			// A completed stored message owns its parts even when it keeps less
			// text (for example signature-only thinking) or omits some.
			have, ok := storedParts[cp.part.ID]
			if v.confirmed["p:"+cp.part.ID] || done[mid] || ok && cp.covers(have.Data) {
				p.confirmed["p:"+cp.part.ID] = v.rev["p:"+cp.part.ID]
			} else {
				pending[mid] = true
			}
		}
	}
	for _, id := range v.msgOrder {
		have, ok := stored[id]
		if ok && v.settled[id] && !pending[id] && (!completed(v.msgs[id].Data) || completed(have.Data)) {
			p.confirmed["m:"+id] = v.rev["m:"+id]
		}
	}
}

func (v *ledgerView) overlay(d *platforms.SessionDetail, limit int, gone map[string]uint64) []string {
	index := map[string]int{}
	oldest := int64(-1 << 62)
	for i, m := range d.Messages {
		index[m.ID] = i
		if limit > 0 && len(d.Messages) >= limit && (i == 0 || m.TimeCreated < oldest) {
			oldest = m.TimeCreated
		}
	}
	var added []string
	for _, id := range v.msgOrder {
		if _, ok := gone[id]; ok {
			continue
		}
		m := v.msgs[id]
		i, ok := index[id]
		switch {
		case !ok && m.TimeCreated >= oldest:
			d.Messages = append(d.Messages, m)
			added = append(added, id)
		case ok && completed(m.Data) && !completed(d.Messages[i].Data):
			d.Messages[i] = m
		}
	}
	if len(added) > 0 {
		sort.SliceStable(d.Messages, func(a, b int) bool { return d.Messages[a].TimeCreated < d.Messages[b].TimeCreated })
		d.TotalMessages += len(added)
	}
	present := map[string]bool{}
	for _, m := range d.Messages {
		present[m.ID] = true
	}
	parts := map[string]int{}
	for i, sp := range d.Parts {
		parts[sp.ID] = i
	}
	for _, cp := range v.parts {
		live := cp.part
		i, ok := parts[live.ID]
		switch {
		case !ok && present[live.MessageID]:
			live.Data = withFields(live.Data, cp.values)
			d.Parts = append(d.Parts, live)
		case ok && cp.final && !cp.covers(d.Parts[i].Data):
			live.Data = withFields(live.Data, cp.values)
			d.Parts[i] = live
		case ok:
			d.Parts[i].Data = withFields(d.Parts[i].Data, cp.values)
		}
	}
	return added
}

// covers reports whether a stored part holds every streamed field at least
// as long as the captured one and, for a final part, the same tool status.
func (cp capturedPart) covers(stored []byte) bool {
	have := streamedFields(stored)
	for field, value := range cp.values {
		if convevents.UTF16Len(have[field]) < convevents.UTF16Len(value) {
			return false
		}
	}
	return !cp.final || readPart(stored).State.Status == readPart(cp.part.Data).State.Status
}
