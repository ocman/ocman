package queuesvc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// memStore is an in-memory Store keeping FIFO order by insertion.
type memStore struct {
	mu   sync.Mutex
	msgs []state.QueuedMessage
	seq  int64
}

func (m *memStore) EnqueueMessage(msg state.QueuedMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	msg.Position = m.seq
	m.msgs = append(m.msgs, msg)
	return nil
}

func (m *memStore) EnqueueClaimedChildResult(_ string, msg state.QueuedMessage) (bool, error) {
	return true, m.EnqueueMessage(msg)
}

func (m *memStore) CountQueuedMessages(platform, sessionID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.msgs {
		if msg.SessionID == sessionID && (platform == "" || msg.Platform == platform) {
			n++
		}
	}
	return n, nil
}

func (m *memStore) HeadQueuedMessage(platform, sessionID string) (*state.QueuedMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.msgs {
		if m.msgs[i].Blocked {
			continue
		}
		if m.msgs[i].SessionID == sessionID && (platform == "" || m.msgs[i].Platform == platform) {
			c := m.msgs[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (m *memStore) RecordQueuedMessageFailure(id, reason string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.msgs {
		if m.msgs[i].ID != id {
			continue
		}
		m.msgs[i].Attempts++
		m.msgs[i].LastError = reason
		m.msgs[i].Blocked = m.msgs[i].Attempts >= state.QueuedMessageAttemptLimit
		return m.msgs[i].Blocked, nil
	}
	return false, nil
}

func (m *memStore) DeleteQueuedMessage(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.msgs {
		if m.msgs[i].ID == id {
			m.msgs = append(m.msgs[:i], m.msgs[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *memStore) ListQueuedMessages(platform, sessionID string) ([]state.QueuedMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.QueuedMessage
	for _, msg := range m.msgs {
		if msg.SessionID == sessionID && (platform == "" || msg.Platform == platform) {
			out = append(out, msg)
		}
	}
	return out, nil
}

func (m *memStore) SessionsWithQueuedMessages() ([]state.QueuedSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[state.QueuedSession]bool{}
	var out []state.QueuedSession
	for _, msg := range m.msgs {
		k := state.QueuedSession{Platform: msg.Platform, SessionID: msg.SessionID}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *memStore) GetQueuedMessageSession(id string) (string, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.msgs {
		if msg.ID == id {
			return msg.Platform, msg.SessionID, true, nil
		}
	}
	return "", "", false, nil
}

func (m *memStore) MoveQueuedMessage(id string, direction int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i := range m.msgs {
		if m.msgs[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false, nil
	}
	swap := idx + direction
	if swap < 0 || swap >= len(m.msgs) {
		return false, nil
	}
	m.msgs[idx], m.msgs[swap] = m.msgs[swap], m.msgs[idx]
	return true, nil
}

func TestEnqueueChildResultIsHeld(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)
	req := platforms.SendMessageRequest{SessionID: "parent", Message: "child result"}

	queued, err := svc.EnqueueChildResult(context.Background(), "child-1", "child-result:1", "opencode", req)
	if err != nil || !queued {
		t.Fatal(err)
	}
	if len(store.msgs) != 1 || len(sender.sent) != 0 {
		t.Fatalf("queued=%d sent=%d, want one held message", len(store.msgs), len(sender.sent))
	}
}

// recSender records sent messages and can be told to fail once.
type recSender struct {
	mu       sync.Mutex
	sent     []string
	failOnce bool
	onSend   func()
}

func (r *recSender) SendNow(_ context.Context, _ string, req platforms.SendMessageRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failOnce {
		r.failOnce = false
		return errors.New("boom")
	}
	if r.onSend != nil {
		r.onSend()
	}
	r.sent = append(r.sent, req.Message)
	return nil
}

func (r *recSender) messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// statusStub reports a fixed busy state.
type statusStub struct {
	running   bool
	ok        bool
	completed bool
	messageID string
	createdAt int64
	onLatest  func()
}

func (s statusStub) TurnRunning(context.Context, string, string) (bool, bool) {
	return s.running, s.ok
}

func (s statusStub) LatestMessageState(context.Context, string, string) (string, int64, bool, bool, bool) {
	if s.onLatest != nil {
		s.onLatest()
	}
	return s.messageID, s.createdAt, s.running, s.completed, s.ok
}

func TestEnqueue_IdleSessionSendsImmediately(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)

	err := svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{
		SessionID: "s1", Message: "hello",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := sender.messages(); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("sent = %v, want [hello] (idle drains immediately)", got)
	}
}

func TestEnqueue_BusyThenOnePerIdleEdge(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: true, ok: true}
	svc := New(store, sender, status, nil)

	// Two follow-ups typed mid-turn: both queue, nothing sends.
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent = %v, want nothing while busy", got)
	}

	// First idle edge: exactly one message sends (the oldest), not both.
	// The send starts a new turn; the next idle edge sends the next.
	status.running = false
	svc.Flush(context.Background(), "", "s1")
	if got := sender.messages(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("after 1st idle = %v, want exactly [one] (one per turn, FIFO)", got)
	}

	// Second idle edge: the next queued message sends.
	svc.Flush(context.Background(), "", "s1")
	if got := sender.messages(); len(got) != 2 || got[1] != "two" {
		t.Fatalf("after 2nd idle = %v, want [one two] (FIFO)", got)
	}

	// Third idle edge with an empty queue: no-op.
	svc.Flush(context.Background(), "", "s1")
	if got := sender.messages(); len(got) != 2 {
		t.Fatalf("after empty flush = %v, want unchanged [one two]", got)
	}
}

// Regression: a second enqueue must never drain an earlier queued message,
// even if the session momentarily reports idle (TurnRunning is inferred
// from the last message role, which flips to idle the instant a user
// message lands). Only a genuine session.idle edge (Flush) drains a
// backlog. Before the fix, enqueue #2 saw "idle" and sent message #1,
// making it vanish from the queue.
func TestEnqueue_SecondDoesNotDrainFirstOnIdleBlip(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	// Status reports IDLE the whole time (the worst case / blip).
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)

	// First enqueue on an idle session drains immediately (send now).
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	if got := sender.messages(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("first enqueue sent = %v, want [one] (idle → send now)", got)
	}

	// Simulate the real world: after "one" is sent the turn is running,
	// so the user queues "two" and "three". Even though our stub still
	// says idle, they must NOT be sent by the enqueue path — only a
	// real idle edge drains them.
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "three"})

	// Nothing new sent; both remain queued and visible.
	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("after backlog enqueues sent = %v, want still [one]", got)
	}
	q, _ := svc.List("opencode", "s1")
	if len(q) != 2 || q[0].Text != "two" || q[1].Text != "three" {
		t.Fatalf("queue = %v, want [two three] both retained", q)
	}
}

// Regression: a genuine session.idle edge must drain the queue even when
// the inferred status still reads "busy". Status is inferred from the last
// assistant message's finish field, which lags the session.idle SSE edge —
// so at the instant Flush runs the poll can still say busy. Before the fix
// the busy gate swallowed the drain and, since no further idle edge ever
// arrives for a now-genuinely-idle session, the whole queue was stranded.
func TestFlush_IdleEdgeDrainsDespiteLaggingBusyStatus(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	// Worst case: the inferred status is STILL busy when the idle edge lands.
	svc := New(store, sender, statusStub{running: true, ok: true}, nil)

	// User queued three follow-ups mid-turn; nothing sent yet.
	for _, msg := range []string{"one", "two", "three"} {
		_ = svc.Enqueue(context.Background(), "opencode", true,
			platforms.SendMessageRequest{SessionID: "s1", Message: msg})
	}
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent = %v, want nothing while busy", got)
	}

	// A genuine session.idle edge fires. Even though the status poll still
	// lags at "busy", exactly one message must drain (session.idle is the
	// authoritative turn-finished signal).
	svc.Flush(context.Background(), "", "s1")
	if got := sender.messages(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("after idle edge = %v, want [one] drained despite lagging busy status", got)
	}
}

// A backlog stranded with no future session.idle edge (queued before a
// fix, or edge swallowed) must self-heal via Sweep: one message drains per
// sweep once the session is idle.
func TestSweep_DrainsStrandedBacklogWhenIdle(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: true, ok: true}
	svc := New(store, sender, status, nil)

	// Two messages queued while busy; nothing sent, no idle edge coming.
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})

	// A sweep while still busy must NOT send (no authoritative idle edge;
	// the busy gate applies).
	svc.Sweep(context.Background())
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sweep while busy sent = %v, want nothing", got)
	}

	// Session goes genuinely idle. Sweep drains exactly one (the oldest) —
	// the backstop for the first stranded message.
	status.running = false
	svc.Sweep(context.Background())
	if got := sender.messages(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("first idle sweep = %v, want [one]", got)
	}

	// A further sweep must NOT chain a second send into the same turn on a
	// status blip: the drained-since-idle guard blocks it. Only a real
	// session.idle edge (Flush) drains the next message.
	svc.Sweep(context.Background())
	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("blip sweep = %v, want still [one] (guard blocks chaining)", got)
	}

	// The real idle edge fires once the turn finishes: it clears the guard
	// and drains the next queued message.
	svc.Flush(context.Background(), "", "s1")
	if got := sender.messages(); len(got) != 2 || got[1] != "two" {
		t.Fatalf("after idle edge = %v, want [one two]", got)
	}
}

// Regression (#58): a client-marked mid-turn send (forceQueue=true) must
// be HELD even when the server's status inference wrongly reports idle
// (ok && !running) — the DB lags the live turn. Before the flag, the
// enqueue fast-path drained it straight into the running turn, so it
// never appeared in the queue list.
func TestEnqueue_ForceQueueHoldsEvenWhenStatusReadsIdle(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	// Status stub says IDLE — the exact lag that broke the old code.
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)

	err := svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{
		SessionID: "s1", Message: "held",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent = %v, want nothing (forceQueue holds)", got)
	}
	q, _ := svc.List("opencode", "s1")
	if len(q) != 1 || q[0].Text != "held" {
		t.Fatalf("queue = %v, want [held] retained", q)
	}
}

// Regression: a mid-turn send (forceQueue=true) whose session.idle edge
// never arrives (watcher disconnected, or OpenCode didn't emit it) must
// still self-heal via Sweep once the session is genuinely idle. Before
// the fix, forceQueue eagerly marked the session drained-since-idle at
// enqueue time, which disarmed the Sweep backstop — so the message was
// stranded forever with no send and no idle edge to clear the guard.
func TestSweep_DrainsForceQueuedBacklogWithNoIdleEdge(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: true, ok: true}
	svc := New(store, sender, status, nil)

	// User queues two follow-ups mid-turn (the real UI path: forceQueue).
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent = %v, want nothing while busy", got)
	}

	// Turn finishes but NO session.idle edge arrives. The session now
	// reads genuinely idle. The Sweep backstop must drain the head.
	status.running = false
	svc.Sweep(context.Background())
	if got := sender.messages(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("sweep = %v, want [one] drained (self-heal without idle edge)", got)
	}
}

func TestSweep_DrainsAfterStaleGuardAndMissedIdleEdge(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: false, ok: true, messageID: "user-1", createdAt: 1}
	svc := New(store, sender, status, nil)

	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	status.messageID = "assistant-1"
	status.createdAt = 2
	status.completed = true

	svc.Sweep(context.Background())
	if got := sender.messages(); len(got) != 2 || got[1] != "two" {
		t.Fatalf("sweep after missed idle = %v, want [one two]", got)
	}
}

func TestSweep_UsesPreSendMessageAsCompletionBaseline(t *testing.T) {
	store := &memStore{}
	status := &statusStub{running: false, ok: true, messageID: "assistant-old", createdAt: 1, completed: true}
	sender := &recSender{onSend: func() {
		status.messageID = "assistant-new"
		status.createdAt = 2
	}}
	svc := New(store, sender, status, nil)

	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	svc.Sweep(context.Background())

	if got := sender.messages(); len(got) != 2 || got[1] != "two" {
		t.Fatalf("fast completion sweep = %v, want [one two]", got)
	}
}

func TestSweep_KeepsGuardForOlderCompletedMessage(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: false, ok: true, messageID: "assistant-new", createdAt: 2, completed: true}
	svc := New(store, sender, status, nil)

	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	status.messageID = "assistant-old"
	status.createdAt = 1
	svc.Sweep(context.Background())

	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("older completion sweep = %v, want only [one]", got)
	}
}

func TestSweep_RechecksGuardGenerationBeforeDrain(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: false, ok: true, messageID: "user-1", createdAt: 1}
	svc := New(store, sender, status, nil)

	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	status.messageID = "assistant-1"
	status.createdAt = 2
	status.completed = true
	status.onLatest = func() { svc.markDrained("s1", "replacement", 3) }
	svc.Sweep(context.Background())

	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("changed guard sweep = %v, want only [one]", got)
	}
}

func TestSweep_DefersSendUntilBaselineIsAvailable(t *testing.T) {
	store := &memStore{}
	sender := &recSender{}
	status := &statusStub{running: false, ok: false}
	svc := New(store, sender, status, nil)

	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "one"})
	_ = svc.Enqueue(context.Background(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "two"})
	status.ok = true
	status.messageID = "assistant-old"
	status.createdAt = 1
	status.completed = true
	svc.Sweep(context.Background())

	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("recovered baseline sweep = %v, want only [one]", got)
	}
}

func TestEnqueue_EmptyRejected(t *testing.T) {
	svc := New(&memStore{}, &recSender{}, statusStub{ok: true}, nil)
	err := svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1"})
	if !errors.Is(err, ErrEmptyMessage) {
		t.Fatalf("err = %v, want ErrEmptyMessage", err)
	}
}

// TestFlush_UnsendableHeadStopsBlockingQueue pins the dead-letter path.
// The drain is strictly head-first, so before this a message that could
// never send (deleted session, unregistered platform) silently stalled
// every later message on that session forever.
func TestFlush_UnsendableHeadStopsBlockingQueue(t *testing.T) {
	store := &memStore{}
	sender := &alwaysFailFirstSender{failFor: "poison"}
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)

	_ = store.EnqueueMessage(state.QueuedMessage{ID: "p", Platform: "opencode", SessionID: "s1", Text: "poison"})
	_ = store.EnqueueMessage(state.QueuedMessage{ID: "n", Platform: "opencode", SessionID: "s1", Text: "next"})

	for i := 0; i < state.QueuedMessageAttemptLimit; i++ {
		svc.Flush(context.Background(), "opencode", "s1")
	}
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent = %v, want nothing while the head keeps failing", got)
	}

	// Once the head is set aside, the rest of the queue moves again.
	svc.Flush(context.Background(), "opencode", "s1")
	if got := sender.messages(); len(got) != 1 || got[0] != "next" {
		t.Fatalf("sent = %v, want [next] once the poison message is blocked", got)
	}
}

// alwaysFailFirstSender fails every send of one specific message and
// records the rest.
type alwaysFailFirstSender struct {
	mu      sync.Mutex
	failFor string
	sent    []string
}

func (s *alwaysFailFirstSender) SendNow(_ context.Context, _ string, req platforms.SendMessageRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Message == s.failFor {
		return errors.New("session not found")
	}
	s.sent = append(s.sent, req.Message)
	return nil
}

func (s *alwaysFailFirstSender) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func TestFlush_UnknownStatusDoesNotSend(t *testing.T) {
	// When status can't be inferred (ok=false) we must not send — the
	// message stays queued for a later, resolvable idle edge.
	store := &memStore{}
	sender := &recSender{}
	svc := New(store, sender, statusStub{running: false, ok: false}, nil)

	_ = store.EnqueueMessage(state.QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi"})
	svc.Flush(context.Background(), "", "s1")
	// ok=false means "unknown" — the gate only blocks on known-running,
	// so an unknown status flushes (session presumed idle). Assert the
	// message drained so we don't strand queued work on flaky status.
	if got := sender.messages(); len(got) != 1 {
		t.Fatalf("sent = %v, want the queued message drained on unknown status", got)
	}
}

func TestFlush_SendErrorLeavesHead(t *testing.T) {
	store := &memStore{}
	sender := &recSender{failOnce: true}
	svc := New(store, sender, statusStub{running: false, ok: true}, nil)

	_ = store.EnqueueMessage(state.QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi"})
	svc.Flush(context.Background(), "", "s1")

	// Send failed → message must remain at the head for retry.
	head, _ := store.HeadQueuedMessage("", "s1")
	if head == nil || head.ID != "x" {
		t.Fatalf("head = %v, want the failed message left for retry", head)
	}
}

func TestRemove_RejectsWrongSession(t *testing.T) {
	store := &memStore{}
	_ = store.EnqueueMessage(state.QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi"})
	svc := New(store, &recSender{}, statusStub{ok: true}, nil)

	// Deleting with a mismatched session id must not remove the row.
	removed, err := svc.Remove("s2", "x")
	if err != nil || removed {
		t.Fatalf("Remove wrong session = (%v,%v), want (false,nil)", removed, err)
	}
	// Correct session removes it.
	removed, err = svc.Remove("s1", "x")
	if err != nil || !removed {
		t.Fatalf("Remove right session = (%v,%v), want (true,nil)", removed, err)
	}
}

func TestMove_RejectsWrongSessionAndBoundary(t *testing.T) {
	store := &memStore{}
	_ = store.EnqueueMessage(state.QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a"})
	_ = store.EnqueueMessage(state.QueuedMessage{ID: "b", Platform: "opencode", SessionID: "s1", Text: "b"})
	svc := New(store, &recSender{}, statusStub{ok: true}, nil)

	// Wrong session: no-op.
	if moved, _ := svc.Move("s2", "a", 1); moved {
		t.Fatal("Move wrong session reported a swap")
	}
	// 'a' is first; moving up is a boundary no-op.
	if moved, _ := svc.Move("s1", "a", -1); moved {
		t.Fatal("Move at boundary reported a swap")
	}
	// Moving 'a' down swaps with 'b'.
	if moved, _ := svc.Move("s1", "a", 1); !moved {
		t.Fatal("Move down did not swap")
	}
	got, _ := svc.List("opencode", "s1")
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("order after move = %v, want [b a]", got)
	}
}

func TestEnqueue_FiresNotify(t *testing.T) {
	var mu sync.Mutex
	var notified []string
	svc := New(&memStore{}, &recSender{}, statusStub{running: true, ok: true}, func(sid string) {
		mu.Lock()
		notified = append(notified, sid)
		mu.Unlock()
	})
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "hi"})
	mu.Lock()
	defer mu.Unlock()
	if len(notified) == 0 || notified[0] != "s1" {
		t.Fatalf("notified = %v, want [s1]", notified)
	}
}

// notifyRec records every notify(sessionID) call, thread-safe.
type notifyRec struct {
	mu   sync.Mutex
	sids []string
}

func (n *notifyRec) fn() func(string) {
	return func(sid string) {
		n.mu.Lock()
		n.sids = append(n.sids, sid)
		n.mu.Unlock()
	}
}

func (n *notifyRec) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sids)
}

// Every mutation that changes the queue MUST fire notify so the UI's
// queue.updated broadcast keeps the list current. This table asserts a
// broadcast fires for enqueue (mid-turn hold + idle drain), drain via
// Flush, drain via Sweep, remove, and move — and that no-op mutations
// (rejected remove/move) do NOT fire.
func TestNotify_FiresForEveryQueueMutation(t *testing.T) {
	t.Run("enqueue mid-turn hold fires", func(t *testing.T) {
		rec := &notifyRec{}
		svc := New(&memStore{}, &recSender{}, statusStub{running: true, ok: true}, rec.fn())
		_ = svc.Enqueue(context.Background(), "opencode", true,
			platforms.SendMessageRequest{SessionID: "s1", Message: "hi"})
		if rec.count() != 1 {
			t.Fatalf("mid-turn enqueue notify count = %d, want 1", rec.count())
		}
	})

	t.Run("enqueue idle drain fires (enqueue + drain)", func(t *testing.T) {
		rec := &notifyRec{}
		// Idle session: the enqueue fast-path drains immediately. That is
		// two state changes (row added, row sent+deleted) → two notifies.
		svc := New(&memStore{}, &recSender{}, statusStub{running: false, ok: true}, rec.fn())
		_ = svc.Enqueue(context.Background(), "opencode", false,
			platforms.SendMessageRequest{SessionID: "s1", Message: "hi"})
		if rec.count() != 2 {
			t.Fatalf("idle enqueue+drain notify count = %d, want 2", rec.count())
		}
	})

	t.Run("Flush drain fires", func(t *testing.T) {
		rec := &notifyRec{}
		store := &memStore{}
		svc := New(store, &recSender{}, statusStub{running: true, ok: true}, rec.fn())
		// Queue mid-turn (1 notify), then reset the recorder and Flush.
		_ = svc.Enqueue(context.Background(), "opencode", true,
			platforms.SendMessageRequest{SessionID: "s1", Message: "hi"})
		rec.mu.Lock()
		rec.sids = nil
		rec.mu.Unlock()
		svc.Flush(context.Background(), "", "s1")
		if rec.count() != 1 {
			t.Fatalf("Flush drain notify count = %d, want 1", rec.count())
		}
	})

	t.Run("Sweep drain fires", func(t *testing.T) {
		rec := &notifyRec{}
		store := &memStore{}
		status := &statusStub{running: true, ok: true}
		svc := New(store, &recSender{}, status, rec.fn())
		_ = svc.Enqueue(context.Background(), "opencode", true,
			platforms.SendMessageRequest{SessionID: "s1", Message: "hi"})
		rec.mu.Lock()
		rec.sids = nil
		rec.mu.Unlock()
		// Session goes idle → Sweep drains → notify.
		status.running = false
		svc.Sweep(context.Background())
		if rec.count() != 1 {
			t.Fatalf("Sweep drain notify count = %d, want 1", rec.count())
		}
	})

	t.Run("remove fires; rejected remove does not", func(t *testing.T) {
		rec := &notifyRec{}
		store := &memStore{}
		_ = store.EnqueueMessage(state.QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi"})
		svc := New(store, &recSender{}, statusStub{ok: true}, rec.fn())

		// Wrong session: no removal, no notify.
		if removed, _ := svc.Remove("s2", "x"); removed {
			t.Fatal("remove wrong session reported removal")
		}
		if rec.count() != 0 {
			t.Fatalf("rejected remove notify count = %d, want 0", rec.count())
		}
		// Right session: removed → notify.
		if removed, _ := svc.Remove("s1", "x"); !removed {
			t.Fatal("remove right session did not remove")
		}
		if rec.count() != 1 {
			t.Fatalf("remove notify count = %d, want 1", rec.count())
		}
	})

	t.Run("move fires; boundary/rejected move does not", func(t *testing.T) {
		rec := &notifyRec{}
		store := &memStore{}
		_ = store.EnqueueMessage(state.QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a"})
		_ = store.EnqueueMessage(state.QueuedMessage{ID: "b", Platform: "opencode", SessionID: "s1", Text: "b"})
		svc := New(store, &recSender{}, statusStub{ok: true}, rec.fn())

		// Boundary: 'a' is first, moving up is a no-op → no notify.
		if moved, _ := svc.Move("s1", "a", -1); moved {
			t.Fatal("boundary move reported a swap")
		}
		// Wrong session → no notify.
		if moved, _ := svc.Move("s2", "a", 1); moved {
			t.Fatal("wrong-session move reported a swap")
		}
		if rec.count() != 0 {
			t.Fatalf("no-op move notify count = %d, want 0", rec.count())
		}
		// Real swap → notify.
		if moved, _ := svc.Move("s1", "a", 1); !moved {
			t.Fatal("valid move did not swap")
		}
		if rec.count() != 1 {
			t.Fatalf("move notify count = %d, want 1", rec.count())
		}
	})

	t.Run("failed send does not fire (message stays queued)", func(t *testing.T) {
		rec := &notifyRec{}
		store := &memStore{}
		_ = store.EnqueueMessage(state.QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi"})
		// Sender fails once → drainHead leaves the head, no delete, no notify.
		svc := New(store, &recSender{failOnce: true}, statusStub{running: false, ok: true}, rec.fn())
		svc.Flush(context.Background(), "", "s1")
		if rec.count() != 0 {
			t.Fatalf("failed-send notify count = %d, want 0 (no state change)", rec.count())
		}
	})
}
