package convstream

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func message(id string, created int64, data string) db.Message {
	return db.Message{ID: id, SessionID: testKey.SessionID, TimeCreated: created, Data: json.RawMessage(data)}
}

func storedPart(id, messageID, text string) db.Part {
	data, _ := json.Marshal(map[string]any{"type": "text", "text": text})
	return db.Part{ID: id, MessageID: messageID, SessionID: testKey.SessionID, Data: data}
}

// composeOnce runs one whole read of l.
func composeOnce(l *ledger, d *platforms.SessionDetail, limit, offset int, now time.Time) []string {
	ticket := l.begin()
	plan, added := l.capture(ticket).compose(d, limit, offset)
	l.commit(plan)
	l.end(ticket, now)
	return added
}

func texts(d *platforms.SessionDetail) map[string]string {
	out := map[string]string{}
	for _, p := range d.Parts {
		out[p.ID] = readPart(p.Data).Text
	}
	return out
}

func ids(d *platforms.SessionDetail) []string {
	out := []string{}
	for _, m := range d.Messages {
		out = append(out, m.ID)
	}
	return out
}

// streamReply streams one assistant reply and ends its turn.
func streamReply(t *testing.T, sink Sink, sub *Subscription) {
	t.Helper()
	sink.Emit(status(db.StatusBusy), Meta{})
	sink.Emit(convevents.MessageUpserted{Message: message("msg_1", 20, `{"role":"assistant"}`)}, Meta{})
	sink.Emit(textPart("p1", "Final", false), Meta{})
	sink.Emit(delta("p1", 5, " answer"), Meta{})
	sink.Emit(idle(), Meta{})
	await(t, sub, convevents.TypeTurnEnded)
}

func TestComposeKeepsAnEndedTurnUntilHistoryStoresIt(t *testing.T) {
	h := newTestHub(t, Options{Linger: time.Millisecond})
	src := newFakeSource()
	src.lc = &platforms.SessionLifecycle{Status: db.StatusDone}
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	streamReply(t, sink, sub)

	// History has only the prompt: the read shows the reply anyway.
	lagging := &platforms.SessionDetail{Messages: []db.Message{message("prompt", 10, `{"role":"user"}`)}, TotalMessages: 1}
	composed := h.Compose(testKey, lagging, 30, 0)
	if got := texts(lagging)["p1"]; got != "Final answer" || len(lagging.Messages) != 2 || lagging.TotalMessages != 2 {
		t.Fatalf("composed = %v %+v", got, lagging)
	}
	if len(composed.MessageIDs) != 1 || composed.MessageIDs[0] != "msg_1" {
		t.Fatalf("composed message ids = %v", composed.MessageIDs)
	}
	cursor := composed.Cursor
	epoch, seq, ok := convevents.ParseCursor(cursor)
	if !ok || epoch != sub.Epoch || seq == 0 {
		t.Fatalf("cursor = %q", cursor)
	}

	// The source restarts: a new connection's hello still restates the reply.
	src.end <- errBoom
	sink = nextSink(t, src)
	hello := decode(t, subscribe(t, h, src, "").Hello.frame).Event.(convevents.StreamHello)
	if parts := hello.Overlay.Inflight.Parts; len(parts) != 1 || readPart(parts[0].Data).Text != "Final answer" {
		t.Fatalf("restarted hello = %+v", hello.Overlay.Inflight)
	}

	// Detached sessions keep it for a revisit's read.
	_ = sink
	sub.Close()
	h.mu.Lock()
	for _, s := range h.sessions {
		h.detachLocked(s)
	}
	h.mu.Unlock()
	revisit := &platforms.SessionDetail{}
	if cursor := h.Compose(testKey, revisit, 30, 0).Cursor; cursor != "" || texts(revisit)["p1"] != "Final answer" {
		t.Fatalf("detached compose = %q %v", cursor, texts(revisit))
	}

	// A read that stores it confirms it. A slower read that began before it
	// still gets the reply; once it ends, the reply is history's alone.
	slow := h.BeginRead(testKey)
	stored := &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 20, `{"role":"assistant"}`)},
		Parts: []db.Part{storedPart("p1", "msg_1", "Final answer")}}
	h.Compose(testKey, stored, 30, 0)
	older := &platforms.SessionDetail{}
	if slow.Compose(older, 30, 0); texts(older)["p1"] != "Final answer" {
		t.Fatalf("concurrent older read lost the reply: %+v", older)
	}
	empty := &platforms.SessionDetail{}
	h.Compose(testKey, empty, 30, 0)
	if len(empty.Messages) != 0 || len(empty.Parts) != 0 {
		t.Fatalf("released content returned: %+v", empty)
	}
}

func TestComposeEventsAfterTheCursorAreNotInTheRead(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.Emit(textPart("p1", "a", false), Meta{})
	cursor := h.Compose(testKey, &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 1, `{}`)}}, 30, 0).Cursor
	sink.Emit(delta("p1", 1, "b"), Meta{})
	frames := decodeAll(t, drain(sub))
	last := frames[len(frames)-1]
	_, at, _ := convevents.ParseCursor(cursor)
	if last.Event.EventType() != convevents.TypePartDelta || last.Seq <= at {
		t.Fatalf("delta seq %d not after cursor %s", last.Seq, cursor)
	}
	for _, f := range frames[:len(frames)-1] {
		if f.Seq > at {
			t.Fatalf("event %s at %d is after the cursor %s but in the read", f.Event.EventType(), f.Seq, cursor)
		}
	}
}

func TestComposeReleasesOnlyWhatHistoryStores(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	for _, ev := range []convevents.Event{
		convevents.MessageUpserted{Message: message("msg_1", 20, `{"role":"assistant","time":{"created":20,"completed":30}}`)},
		textPart("p1", "long streamed thinking", false),
		convevents.PartUpserted{Final: true, Part: convevents.Part{Part: db.Part{ID: "tool", MessageID: "msg_1",
			Data: json.RawMessage(`{"type":"tool","state":{"status":"completed","output":"ok"}}`)}}},
	} {
		l.apply(ev, now)
	}
	l.settle()
	running := textPart("running", "still streaming", false)
	running.Part.MessageID = "msg_2"
	l.apply(convevents.MessageUpserted{Message: message("msg_2", 40, `{"role":"assistant"}`)}, now)
	l.apply(running, now)

	// A partial read: incomplete message, shorter text, running tool.
	partial := &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 20, `{"role":"assistant"}`), message("msg_2", 40, `{"role":"assistant"}`)},
		Parts: []db.Part{storedPart("p1", "msg_1", "long"),
			{ID: "tool", MessageID: "msg_1", Data: json.RawMessage(`{"type":"tool","state":{"status":"running","output":"ok"}}`)},
			storedPart("running", "msg_2", "still streaming")}}
	composeOnce(l, partial, 30, 0, now)
	if got := texts(partial); got["p1"] != "long streamed thinking" || readPart(partial.Parts[1].Data).State.Status != "completed" {
		t.Fatalf("partial read not overlaid: %v %s", got, partial.Parts[1].Data)
	}
	if !completed(partial.Messages[0].Data) {
		t.Fatal("stored incomplete message hid the completed live copy")
	}
	if len(l.content.parts) != 3 {
		t.Fatalf("released unconfirmed content: %v", l.content.partOrder)
	}

	// The completed stored message owns its parts, even with less text. The
	// running turn's part stays until its turn ends.
	done := &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 20, `{"role":"assistant","finish":"stop"}`)},
		Parts: []db.Part{storedPart("p1", "msg_1", "")}}
	slow := l.begin()
	composeOnce(l, done, 30, 0, now)
	if len(l.content.parts) != 3 || len(l.confirmed) != 3 {
		t.Fatalf("confirmed = %v", l.content.partOrder)
	}
	// A read that began before the confirmation still overlays it.
	older := &platforms.SessionDetail{Messages: done.Messages, Parts: []db.Part{storedPart("p1", "msg_1", "")}}
	if plan, _ := l.capture(slow).compose(older, 30, 0); texts(older)["p1"] != "long streamed thinking" {
		t.Fatal("older read lost the streamed text")
	} else {
		l.commit(plan)
	}
	l.end(slow, now)
	done = &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 20, `{"role":"assistant","finish":"stop"}`)},
		Parts: []db.Part{storedPart("p1", "msg_1", "")}}
	composeOnce(l, done, 30, 0, now)
	if len(l.content.parts) != 1 || l.content.parts["running"] == nil || len(l.content.msgs) != 1 || l.content.msgs["msg_2"].ID == "" {
		t.Fatalf("after completed read: parts=%v msgs=%v", l.content.partOrder, l.content.msgOrder)
	}
	if texts(done)["p1"] != "" {
		t.Fatal("completed stored part was overwritten")
	}
}

func TestComposeDropsEndedContentHistoryMovedPast(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	l.apply(convevents.MessageUpserted{Message: message("msg_1", 20, `{}`)}, now)
	l.apply(textPart("p1", "reverted", false), now)
	l.settle()
	later := &platforms.SessionDetail{Messages: []db.Message{message("later", 30, `{"role":"user"}`)}}
	composeOnce(l, later, 30, 0, now)
	if len(later.Messages) != 1 || len(l.content.parts) != 0 || len(l.content.msgs) != 0 {
		t.Fatalf("removed content survived: %+v parts=%v", later, l.content.partOrder)
	}
}

func TestComposeOverlaysOnlyTheNewestPage(t *testing.T) {
	l := &ledger{}
	l.apply(convevents.MessageUpserted{Message: message("old", 5, `{}`)}, time.Now())
	older := &platforms.SessionDetail{}
	composeOnce(l, older, 30, 30, time.Now())
	full := &platforms.SessionDetail{Messages: []db.Message{message("a", 10, `{}`), message("b", 11, `{}`)}}
	composeOnce(l, full, 2, 0, time.Now())
	if len(older.Messages) != 0 || len(full.Messages) != 2 {
		t.Fatalf("overlay outside its page: older=%v full=%v", ids(older), ids(full))
	}
	composeOnce(l, nil, 30, 0, time.Now())
}

func TestLedgerRewriteDropsEndedTurnsAndEvictionKeepsTheRunningTurn(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	l.apply(textPart("ended", "x", false), now)
	l.settle()
	running := textPart("running", "y", false)
	running.Part.MessageID = "msg_2"
	l.apply(running, now)
	l.apply(convevents.HistoryRewritten{Reason: convevents.RewriteRevert}, now)
	if len(l.content.parts) != 1 || l.content.parts["running"] == nil {
		t.Fatalf("rewrite kept %v", l.content.partOrder)
	}
	l.apply(convevents.PartRemoved{MessageID: "msg_2", PartID: "running"}, now)
	l.apply(convevents.MessageRemoved{MessageID: "msg_2"}, now)
	l.apply(convevents.TurnStarted{}, now)
	// Its removal tombstones keep it until it expires.
	if l.expired(now) || !l.expired(now.Add(ledgerTTL+time.Second)) {
		t.Fatal("tombstone-only ledger expiry")
	}
	for i := range maxLedgerParts + 1 {
		p := textPart("p"+time.Duration(i).String(), "x", false)
		if i == 0 {
			p.Part.MessageID = "ended"
			l.apply(p, now)
			l.settle()
			continue
		}
		l.apply(p, now)
	}
	if len(l.content.parts) != maxLedgerParts || l.content.parts["p0s"] != nil {
		t.Fatalf("ledger holds %d parts", len(l.content.parts))
	}
}

func TestHubSweepsExpiredDetachedLedgers(t *testing.T) {
	now := time.Now()
	h := newTestHub(t, Options{Now: func() time.Time { return now }})
	h.mu.Lock()
	l := h.ledgerLocked(testKey)
	h.mu.Unlock()
	l.apply(textPart("p1", "x", false), now)
	now = now.Add(ledgerTTL + time.Second)
	if h.Compose(Key{Platform: "opencode", SessionID: "other"}, &platforms.SessionDetail{}, 30, 0); h.ledgers[testKey] != nil {
		t.Fatalf("expired ledger kept: %v", h.ledgers)
	}
	// The read's own empty ledger goes at the next sweep.
	h.mu.Lock()
	h.sweepLocked()
	h.mu.Unlock()
	if len(h.ledgers) != 0 {
		t.Fatalf("empty ledger kept: %v", h.ledgers)
	}
}

func TestComposeKeepsStreamRemovalsOutOfOlderReads(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	l.apply(convevents.MessageRemoved{MessageID: "gone"}, now)
	l.apply(convevents.PartRemoved{MessageID: "kept", PartID: "gone-part"}, now)
	read := &platforms.SessionDetail{
		Messages: []db.Message{message("gone", 1, `{}`), message("kept", 2, `{}`)},
		Parts:    []db.Part{storedPart("a", "gone", "x"), storedPart("gone-part", "kept", "y"), storedPart("b", "kept", "z")},
	}
	composeOnce(l, read, 30, 0, now)
	if got := ids(read); len(got) != 1 || got[0] != "kept" || len(read.Parts) != 1 || read.Parts[0].ID != "b" {
		t.Fatalf("removed rows returned: %v %+v", got, read.Parts)
	}
	// The stream adding it back wins over the tombstone.
	l.apply(convevents.MessageUpserted{Message: message("gone", 1, `{}`)}, now)
	again := &platforms.SessionDetail{Messages: []db.Message{message("gone", 1, `{}`)}}
	if composeOnce(l, again, 30, 0, now); len(again.Messages) != 1 {
		t.Fatalf("re-added message filtered: %v", ids(again))
	}
	composeOnce(l, &platforms.SessionDetail{}, 30, 0, now.Add(ledgerTTL+time.Second))
	if len(l.removed) != 0 {
		t.Fatalf("tombstones = %v", l.removed)
	}
}

func TestLedgerSettlesWhenATurnEndsWithoutAnIdleEdge(t *testing.T) {
	settled := func(s *session) bool {
		s.ledger.mu.Lock()
		defer s.ledger.mu.Unlock()
		return s.ledger.settled["msg_1"]
	}
	busy := &convevents.SessionStatusEvent{Status: db.StatusBusy}
	for name, end := range map[string]func(*session){
		"terminal status": func(s *session) { s.onStatusLocked(status(db.StatusError), false) },
		"idle reconnect":  func(s *session) { s.connectLocked(Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusDone}}) },
		"settled read": func(s *session) {
			s.settledLocked(s.st.gen, &platforms.SessionLifecycle{Status: db.StatusDone}, nil, false)
		},
		"detach": func(s *session) {
			s.mu.Unlock()
			s.hub.mu.Lock()
			s.hub.detachLocked(s)
			s.hub.mu.Unlock()
			s.mu.Lock()
		},
	} {
		s := newSession(newTestHub(t, Options{}), testKey)
		s.mu.Lock()
		s.connectLocked(Seed{Status: busy})
		s.handleLocked(textPart("p1", "x", false), false)
		if settled(s) {
			t.Fatalf("%s: running turn settled early", name)
		}
		end(s)
		if !settled(s) {
			t.Fatalf("%s: ended turn not settled", name)
		}
		s.mu.Unlock()
	}
}

func TestHubKeepsTombstonesOfDetachedSessions(t *testing.T) {
	now := time.Now()
	h := newTestHub(t, Options{Now: func() time.Time { return now }})
	h.mu.Lock()
	l := h.ledgerLocked(testKey)
	h.mu.Unlock()
	l.apply(convevents.MessageRemoved{MessageID: "gone"}, now)
	h.Compose(Key{Platform: "opencode", SessionID: "other"}, &platforms.SessionDetail{}, 30, 0)
	lagging := &platforms.SessionDetail{Messages: []db.Message{message("gone", 1, `{}`)}}
	if h.Compose(testKey, lagging, 30, 0); len(lagging.Messages) != 0 {
		t.Fatalf("a sweep dropped the tombstone: %v", ids(lagging))
	}
}

func TestLedgerStoresStreamedTextOnceAndAppendsInPlace(t *testing.T) {
	var f inflight
	f.upsert(textPart("p1", "abc", false).Part, false)
	lp := f.parts["p1"]
	if readPart(lp.part.Data).Text != "" || readPart(lp.data()).Text != "abc" {
		t.Fatalf("stored %s, rendered %s", lp.part.Data, lp.data())
	}
	for _, text := range []string{"d", "😀", "e"} {
		f.delta(convevents.PartDelta{PartID: "p1", Field: convevents.FieldText, Offset: lp.fields[convevents.FieldText].units, Text: text})
	}
	// An overlapping replay appends only its unseen tail; a gap is refused.
	f.delta(convevents.PartDelta{PartID: "p1", Field: convevents.FieldText, Offset: 6, Text: "ef"})
	f.delta(convevents.PartDelta{PartID: "p1", Field: convevents.FieldText, Offset: 99, Text: "gap"})
	if got := readPart(f.snapshot().Parts[0].Data).Text; got != "abcd😀ef" || lp.fields[convevents.FieldText].units != 8 {
		t.Fatalf("text = %q (%d units)", got, lp.fields[convevents.FieldText].units)
	}
	// A shorter non-final upsert keeps the longer value; a final one replaces it.
	f.upsert(textPart("p1", "ab", false).Part, false)
	if got := readPart(lp.data()).Text; got != "abcd😀ef" {
		t.Fatalf("non-final shortened: %q", got)
	}
	f.upsert(textPart("p1", "trimmed", true).Part, true)
	if got := readPart(f.parts["p1"].data()).Text; got != "trimmed" {
		t.Fatalf("final = %q", got)
	}
	tool := convevents.Part{Part: db.Part{ID: "t", MessageID: "msg_1", Data: json.RawMessage(`{"type":"tool","state":{"status":"running","output":"out","raw":"in"}}`)}}
	f.upsert(tool, false)
	if st := readPart(f.parts["t"].part.Data).State; st.Output != "" || st.Raw != "" || st.Status != "running" {
		t.Fatalf("tool fields kept twice: %s", f.parts["t"].part.Data)
	}
	if st := readPart(f.parts["t"].data()).State; st.Output != "out" || st.Raw != "in" {
		t.Fatalf("tool rendered %s", f.parts["t"].data())
	}
}

func TestComposeNeverReleasesContentChangedAfterItsCapture(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	l.apply(convevents.MessageUpserted{Message: message("msg_1", 20, `{}`)}, now)
	l.apply(textPart("p1", "ab", false), now)
	l.settle()
	ticket := l.begin()
	view := l.capture(ticket)
	l.apply(delta("p1", 2, "c"), now)
	stored := &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 20, `{}`)}, Parts: []db.Part{storedPart("p1", "msg_1", "ab")}}
	plan, _ := view.compose(stored, 30, 0)
	l.commit(plan)
	if _, ok := l.confirmed["p:p1"]; ok {
		t.Fatal("a read captured before newer text confirmed it")
	}
	current := l.begin()
	plan, _ = l.capture(current).compose(&platforms.SessionDetail{Messages: stored.Messages, Parts: []db.Part{storedPart("p1", "msg_1", "abc")}}, 30, 0)
	l.commit(plan)
	if _, ok := l.confirmed["p:p1"]; !ok {
		t.Fatal("a current read did not confirm")
	}
	// The stale read is still open, so the entry stays until it ends.
	l.end(current, now)
	if l.content.parts["p1"] == nil {
		t.Fatal("released while an earlier read was open")
	}
	l.end(ticket, now)
	if len(l.rev) != 0 || len(l.content.parts) != 0 {
		t.Fatalf("released entries kept bookkeeping: rev=%v parts=%v", l.rev, l.content.partOrder)
	}
}

func TestHubBudgetEvictsEndedTurnsOfUnwatchedSessionsFirst(t *testing.T) {
	now := time.Now()
	h := newTestHub(t, Options{Now: func() time.Time { return now }})
	big := strings.Repeat("x", 1000)
	fill := func(key Key, id string, settle bool, at time.Time) *ledger {
		h.mu.Lock()
		l := h.ledgerLocked(key)
		h.mu.Unlock()
		p := textPart(id, big, false)
		p.Part.MessageID = id
		l.apply(p, at)
		if settle {
			l.settle()
		}
		return l
	}
	watched := Key{Platform: "opencode", SessionID: "watched"}
	ended := fill(watched, "ended", true, now.Add(-time.Minute))
	running := fill(watched, "running", false, now)
	old := fill(Key{Platform: "opencode", SessionID: "old"}, "old", true, now.Add(-2*time.Minute))
	recent := fill(Key{Platform: "opencode", SessionID: "recent"}, "recent", true, now)
	h.mu.Lock()
	h.sessions[watched] = newSession(h, watched)
	h.sessions[watched].ledger = running
	h.maxLedger = 3500
	h.budgetLocked()
	h.mu.Unlock()
	held := func(l *ledger, id string) bool { return l.content.parts[id] != nil }
	if held(old, "old") || !held(recent, "recent") || !held(ended, "ended") || !held(running, "running") {
		t.Fatalf("first eviction: old=%v recent=%v ended=%v running=%v", held(old, "old"), held(recent, "recent"), held(ended, "ended"), held(running, "running"))
	}
	h.mu.Lock()
	h.maxLedger = 1100
	h.budgetLocked()
	h.mu.Unlock()
	if held(recent, "recent") || held(ended, "ended") || !held(running, "running") {
		t.Fatal("budget evicted a running turn or kept ended ones")
	}
	h.mu.Lock()
	delete(h.sessions, watched)
	h.mu.Unlock()
}

func TestComposeRacesWithStreaming(t *testing.T) {
	h := newTestHub(t, Options{})
	src := newFakeSource()
	subscribe(t, h, src, "")
	sink := nextSink(t, src)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.Emit(textPart("p1", "", false), Meta{})
		for i := range 500 {
			sink.Emit(delta("p1", i, "x"), Meta{})
		}
		sink.Emit(idle(), Meta{})
	}()
	for range 50 {
		h.Compose(testKey, &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 1, `{}`)}}, 30, 0)
	}
	<-done
	read := &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 1, `{}`)}}
	h.Compose(testKey, read, 30, 0)
	if got := texts(read)["p1"]; len(got) != 500 {
		t.Fatalf("composed %d chars", len(got))
	}
}

func TestLedgerEvictionCountsMessagesAndKeepsRunningContent(t *testing.T) {
	now := time.Now()
	big := `{"role":"assistant","note":"` + strings.Repeat("m", 1000) + `"}`
	l := &ledger{}
	// A message-only ended turn, a mixed ended turn, then a running turn.
	l.apply(convevents.MessageUpserted{Message: message("only", 1, big)}, now)
	l.apply(convevents.MessageUpserted{Message: message("mixed", 2, big)}, now)
	p := textPart("mixed-part", strings.Repeat("x", 1000), false)
	p.Part.MessageID = "mixed"
	l.apply(p, now)
	l.settle()
	l.apply(convevents.MessageUpserted{Message: message("running", 3, big)}, now)
	total, _ := l.size()
	freed := l.evict(total-500, false)
	if _, ok := l.content.msgs["only"]; ok || freed < 1000 {
		t.Fatalf("message-only turn kept (freed %d)", freed)
	}
	if _, ok := l.content.msgs["mixed"]; !ok {
		t.Fatal("evicted more than needed")
	}
	l.evict(0, false)
	if _, ok := l.content.msgs["mixed"]; ok || l.content.parts["mixed-part"] != nil {
		t.Fatal("mixed turn not evicted as a group")
	}
	if _, ok := l.content.msgs["running"]; !ok {
		t.Fatal("running content evicted")
	}
	if after, _ := l.size(); after != len(big) {
		t.Fatalf("size after eviction = %d", after)
	}
}

func TestComposeInfersRemovalsWithoutHidingHistory(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	l.apply(convevents.MessageUpserted{Message: message("msg_1", 20, `{}`)}, now)
	l.apply(textPart("p1", "reply", false), now)
	l.settle()
	// History stored a later message first: the ledger copy is forgotten...
	composeOnce(l, &platforms.SessionDetail{Messages: []db.Message{message("later", 30, `{}`)}}, 30, 0, now)
	if len(l.content.msgs) != 0 || len(l.content.parts) != 0 || len(l.removed) != 0 {
		t.Fatalf("inferred removal kept content or a tombstone: %v %v", l.content.msgOrder, l.removed)
	}
	// ...but history storing it afterwards is shown.
	read := &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 20, `{}`), message("later", 30, `{}`)},
		Parts: []db.Part{storedPart("p1", "msg_1", "reply")}}
	composeOnce(l, read, 30, 0, now)
	if len(read.Messages) != 2 || len(read.Parts) != 1 {
		t.Fatalf("history row hidden: %v", ids(read))
	}
}

func TestStreamRemovalTombstoneClearsOnceHistoryCatchesUp(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	l.apply(convevents.PartRemoved{MessageID: "msg_1", PartID: "p1"}, now)
	holding := func() *platforms.SessionDetail {
		return &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 1, `{}`)}, Parts: []db.Part{storedPart("p1", "msg_1", "x")}}
	}
	slow := l.begin()
	stale := holding()
	if composeOnce(l, stale, 30, 0, now); len(stale.Parts) != 0 {
		t.Fatal("history that still holds a removed part restored it")
	}
	// History catches up with the removal; a later re-add is shown.
	composeOnce(l, &platforms.SessionDetail{Messages: []db.Message{message("msg_1", 1, `{}`)}}, 30, 0, now)
	readded := holding()
	if composeOnce(l, readded, 30, 0, now); len(readded.Parts) != 1 {
		t.Fatal("a part history re-added stayed hidden")
	}
	// A read that began before history caught up still filters it.
	older := holding()
	plan, _ := l.capture(slow).compose(older, 30, 0)
	l.commit(plan)
	if len(older.Parts) != 0 {
		t.Fatal("an earlier read lost the tombstone")
	}
	l.end(slow, now)
	if len(l.removed) != 0 {
		t.Fatalf("cleared tombstone kept: %v", l.removed)
	}
}

func TestLedgerBoundsSettledMessageCount(t *testing.T) {
	l := &ledger{}
	now := time.Now()
	for i := range maxLedgerMessages + 10 {
		l.apply(convevents.MessageUpserted{Message: message(fmt.Sprintf("m%d", i), int64(i), `{}`)}, now)
		l.settle()
	}
	if len(l.content.msgOrder) > maxLedgerMessages {
		t.Fatalf("ledger holds %d messages", len(l.content.msgOrder))
	}
}

func TestHubBudgetHasAHardCeilingForRunningTurns(t *testing.T) {
	now := time.Now()
	h := newTestHub(t, Options{Now: func() time.Time { return now }})
	h.mu.Lock()
	l := h.ledgerLocked(testKey)
	h.mu.Unlock()
	for i := range 5 {
		p := textPart(fmt.Sprintf("p%d", i), strings.Repeat("x", 1000), false)
		p.Part.MessageID = fmt.Sprintf("m%d", i)
		l.apply(p, now)
	}
	h.mu.Lock()
	h.sessions[testKey] = newSession(h, testKey)
	h.sessions[testKey].ledger = l
	h.maxLedger = 1500
	h.budgetLocked()
	delete(h.sessions, testKey)
	h.mu.Unlock()
	if n, _ := l.size(); n > 3000 || n < 2000 {
		t.Fatalf("running content held %d bytes, want just under the 3000 ceiling", n)
	}
}
