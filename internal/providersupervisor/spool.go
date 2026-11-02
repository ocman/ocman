// Package providersupervisor hosts a provider independently of the dashboard.
//
// # Sequencing and durability
//
// Every event gets a supervisor-wide sequence under Supervisor.mu. That mutex
// guards in-memory state only: spool file IO runs without it, so HTTP routes,
// snapshots, /events batching and other sessions never wait on an fsync.
//
// Publish is durable before it returns: it assigns a sequence, queues the
// record on its session's spool and waits. Whichever waiting publisher finds
// the spool idle writes every queued record of that session with one append
// and one fsync (group commit), then hands the spool to the next writer. A
// record is exposed (retained for /events, folded into session state) and its
// apply callback runs only after its fsync succeeded, under Supervisor.mu and
// in sequence order within its session, on the publishing goroutine. A failed
// write is never exposed and never applied; Publish returns the error. The
// queue is bounded by the number of blocked publishers.
//
// Sessions commit independently, so a later sequence in one session can be
// exposed before an earlier one of another. Unscoped /events streams therefore
// deliver only up to the watermark below which every sequence is settled; a
// snapshot lease scopes its stream to the snapshot's session, whose records
// are always exposed in order.
//
// BeginTurn, PublishIf and headless prompt replays are barriers: they wait
// until no sequenced record is unsettled, hold off new publishers, check their
// condition and then commit their events one by one, each durable before the
// next is sequenced. A condition therefore observes every earlier apply, and a
// turn accepted after it was checked cannot be followed by its events. While
// a barrier commits, /drain and /drain-idle do not report idle.
//
// # Retention
//
// A session's records are kept until its turn is acknowledged and every
// lease that may need them has passed it. Retention is per session: a
// snapshot lease holds back only its own session, while unscoped streams
// (the headless watcher) hold back every session. Past retainLimit records
// in a session, leases that hold its acknowledged turns are invalidated
// rather than pinning memory: an unclaimed snapshot lease then answers 409
// (refresh the snapshot) and an attached stream ends, so its consumer takes
// a new snapshot. Unacknowledged turns are never truncated.
package providersupervisor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/providerprotocol"
)

type record struct {
	Sequence  uint64                 `json:"sequence"`
	Event     providerprotocol.Event `json:"event"`
	Completed bool                   `json:"completed,omitempty"`
}

type entryState int

const (
	entryQueued  entryState = iota // sequenced, waiting for a writer
	entryWriting                   // in a group commit
	entryDurable                   // synced, waiting for its turn to be exposed
	entryFailed                    // the write failed; never exposed
)

// entry is a sequenced record on its way to its spool.
type entry struct {
	id     string
	spool  *sessionSpool
	meta   eventMeta
	rec    record
	data   []byte // encoded record, without Completed
	replay bool
	apply  func()
	state  entryState
	err    error
}

type sessionSpool struct {
	records []record
	ack     uint64
	active  bool
	limited bool
	pending map[string]record
	pruning bool // a rewrite of this spool is in flight

	queue     []*entry   // sequenced, not yet exposed or discarded, in order
	writing   bool       // a publisher is writing queued entries
	last      uint64     // highest exposed sequence
	admitting int        // BeginTurn calls not yet settled
	cond      *sync.Cond // on Supervisor.mu: queue progress
	io        sync.Mutex // spool file appends and prune's swap; taken before Supervisor.mu
}

type cursorLease struct {
	cursor  uint64
	expires time.Time // zero means an attached stream
	session string    // a snapshot's session; empty streams every session
}

// Supervisor serializes snapshot reads with publishing. Provider state must be
// updated only through Publish's apply callback, so snapshots cannot race deltas.
type Supervisor struct {
	mu             sync.Mutex
	dir            string
	seq            uint64
	sessions       map[string]*sessionSpool
	leases         map[string]cursorLease
	evicted        map[string]bool // attached leases invalidated by the retention cap
	changed       chan struct{}
	draining       bool
	idleDrainToken string
	admitted       int
	backend        Backend

	unresolved map[uint64]struct{} // sequenced, neither exposed nor discarded
	exclusive  bool                // a barrier is committing
	barriers   int                 // barriers waiting to commit
	gate       *sync.Cond          // on mu: unresolved shrank or a barrier finished
}

func New(dir string, backend Backend) (*Supervisor, error) {
	if dir == "" {
		return nil, fmt.Errorf("supervisor spool directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Supervisor{dir: dir, backend: backend, sessions: make(map[string]*sessionSpool), leases: make(map[string]cursorLease), evicted: make(map[string]bool), changed: make(chan struct{}), unresolved: make(map[uint64]struct{})}
	s.gate = sync.NewCond(&s.mu)
	return s, nil
}

// Active reports whether session id has an open turn or a pending prompt:
// what keeps /drain from reporting idle and its spool from truncating.
func (s *Supervisor) Active(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.sessions[id]
	return p != nil && (p.active || len(p.pending) != 0 || p.admitting != 0)
}

// settled reports that no record is on its way to a spool.
func (s *Supervisor) settled() bool {
	return len(s.unresolved) == 0 && !s.exclusive
}

// watermark is the highest sequence below which every record is settled:
// what an unscoped stream may deliver without skipping a later exposure.
func (s *Supervisor) watermark() uint64 {
	mark := s.seq
	for seq := range s.unresolved {
		if seq-1 < mark {
			mark = seq - 1
		}
	}
	return mark
}

func (s *Supervisor) session(id string) *sessionSpool {
	p := s.sessions[id]
	if p == nil {
		p = &sessionSpool{pending: make(map[string]record)}
		p.cond = sync.NewCond(&s.mu)
		s.sessions[id] = p
	}
	return p
}

func (s *Supervisor) path(id string) string {
	hash := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(hash[:])+".jsonl")
}

// Publish appends and syncs before exposing an event or updating provider state.
// The callback must not call back into Supervisor.
func (s *Supervisor) Publish(event providerprotocol.Event, apply func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.exclusive || s.barriers != 0 {
		s.gate.Wait()
	}
	e, err := s.sequence(event, apply, false)
	if err != nil {
		return err
	}
	return s.await(e)
}

// PublishIf publishes events in order only while ok holds, atomically with
// BeginTurn: a turn accepted after ok was checked cannot be followed by these
// events (a stale idle edge). ok and apply must not call back into Supervisor.
func (s *Supervisor) PublishIf(ok func() bool, apply func(providerprotocol.Event), events ...providerprotocol.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.barrier(ok, func() []providerprotocol.Event { return events }, func(event providerprotocol.Event) func() { return func() { apply(event) } }, false)
}

// BeginTurn registers ownership before a backend schedules asynchronous work.
// Backends must call this before returning a successful turn-start response,
// never defer the first busy event to the child reader. A failed admission must
// not spawn a child. Publish session.idle when the registered turn finishes.
func (s *Supervisor) BeginTurn(id string, apply func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return fmt.Errorf("supervisor is draining")
	}
	p := s.session(id)
	p.admitting++
	defer func() { p.admitting-- }()
	props, _ := json.Marshal(map[string]any{"sessionID": id, "status": map[string]string{"type": "busy"}})
	// A drain that began while earlier records settled still refuses the turn.
	busy := []providerprotocol.Event{{Type: "session.status", Properties: props}}
	ok, err := s.barrier(func() bool { return !s.draining }, func() []providerprotocol.Event { return busy }, func(providerprotocol.Event) func() { return apply }, false)
	if err == nil && !ok {
		err = fmt.Errorf("supervisor is draining")
	}
	return err
}

// barrier commits events in order once every earlier record has settled,
// holding off new publishers, and only while ok (if any) holds. events is
// built after that, so it observes every earlier apply too. Called and
// returns with s.mu held.
func (s *Supervisor) barrier(ok func() bool, events func() []providerprotocol.Event, apply func(providerprotocol.Event) func(), replay bool) (bool, error) {
	s.barriers++
	for !s.settled() {
		s.gate.Wait()
	}
	s.barriers--
	s.exclusive = true
	defer func() {
		s.exclusive = false
		s.gate.Broadcast()
	}()
	if ok != nil && !ok() {
		return false, nil
	}
	for _, event := range events() {
		e, err := s.sequence(event, apply(event), replay)
		if err != nil {
			return true, err
		}
		if err := s.await(e); err != nil {
			return true, err
		}
	}
	return true, nil
}

// eventMeta is what the supervisor reads from an event's properties.
type eventMeta struct {
	session, id, requestID, status string
}

func parseEvent(event providerprotocol.Event) (eventMeta, error) {
	var props struct {
		SessionID string `json:"sessionID"`
		ID        string `json:"id"`
		RequestID string `json:"requestID"`
		Info      struct {
			SessionID string `json:"sessionID"`
		} `json:"info"`
		Part struct {
			SessionID string `json:"sessionID"`
		} `json:"part"`
		Status struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if err := json.Unmarshal(event.Properties, &props); err != nil {
		return eventMeta{}, err
	}
	meta := eventMeta{session: props.SessionID, id: props.ID, requestID: props.RequestID, status: props.Status.Type}
	if meta.session == "" {
		meta.session = props.Info.SessionID
	}
	if meta.session == "" {
		meta.session = props.Part.SessionID
	}
	if meta.session == "" {
		return eventMeta{}, fmt.Errorf("event missing sessionID")
	}
	if (event.Type == "permission.asked" || event.Type == "question.asked") && meta.id == "" {
		return eventMeta{}, fmt.Errorf("prompt missing id")
	}
	return meta, nil
}

// sequence validates event, assigns its sequence and queues it on its spool.
// A replay re-delivers a pending prompt under a new sequence: it does not open
// a turn, so a prompt that outlived its turn's idle edge cannot hold the
// session active once it is resolved.
func (s *Supervisor) sequence(event providerprotocol.Event, apply func(), replay bool) (*entry, error) {
	meta, err := parseEvent(event)
	if err != nil {
		return nil, err
	}
	if s.seq == ^uint64(0) {
		return nil, fmt.Errorf("event sequence exhausted")
	}
	// Own the payload; callers often reuse a stream-json read buffer.
	event.Properties = append(json.RawMessage(nil), event.Properties...)
	rec := record{Sequence: s.seq + 1, Event: event}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	if len(data) > providerprotocol.MaxEventBytes-128 {
		return nil, fmt.Errorf("event exceeds size limit")
	}
	s.seq = rec.Sequence
	p := s.session(meta.session)
	e := &entry{id: meta.session, spool: p, meta: meta, rec: rec, data: data, replay: replay, apply: apply}
	p.queue = append(p.queue, e)
	s.unresolved[rec.Sequence] = struct{}{}
	return e, nil
}

// syncSpool makes an append durable; tests substitute a slow disk.
var syncSpool = (*os.File).Sync

// await commits e, writing its spool's queue when no other publisher is, then
// exposes it once every earlier record of its session is settled. Called and
// returns with s.mu held.
func (s *Supervisor) await(e *entry) error {
	p := e.spool
	for {
		switch {
		case e.state == entryQueued && !p.writing:
			s.writeQueued(e.id, p)
			continue
		case (e.state == entryDurable || e.state == entryFailed) && p.queue[0] == e:
			return s.settle(e)
		}
		p.cond.Wait()
	}
}

// writeQueued group-commits every queued record of p: one append and one
// fsync, without s.mu. Called and returns with s.mu held.
func (s *Supervisor) writeQueued(id string, p *sessionSpool) {
	p.writing = true
	// Completed depends on the limited state before the record, which the
	// durable records still awaiting exposure precede.
	limited := p.limited
	var batch []*entry
	var data []byte
	for _, e := range p.queue {
		if e.state == entryFailed {
			continue
		}
		completed := e.rec.Event.Type == "session.idle" && !limited
		if e.rec.Event.Type == "session.status" {
			limited = e.meta.status == "limited"
		}
		if e.state != entryQueued {
			continue
		}
		e.state = entryWriting
		e.rec.Completed = completed
		line := e.data
		if completed {
			line, _ = json.Marshal(e.rec) // sequence marshalled it without the flag
		}
		batch = append(batch, e)
		data = append(append(data, line...), '\n')
	}
	path := s.path(id)
	s.mu.Unlock()
	p.io.Lock()
	err := appendSpool(path, data)
	s.mu.Lock()
	p.io.Unlock()
	for _, e := range batch {
		e.state, e.err = entryDurable, err
		if err != nil {
			e.state = entryFailed
		}
	}
	p.writing = false
	p.cond.Broadcast()
}

func appendSpool(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = syncSpool(f)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// settle removes the head entry e of its spool's queue, exposing it unless its
// write failed. Called with s.mu held.
func (s *Supervisor) settle(e *entry) error {
	p := e.spool
	p.queue = p.queue[1:]
	delete(s.unresolved, e.rec.Sequence)
	defer func() {
		p.cond.Broadcast()
		s.gate.Broadcast()
		close(s.changed)
		s.changed = make(chan struct{})
	}()
	if e.state == entryFailed {
		if s.seq == e.rec.Sequence {
			s.seq-- // never exposed: reusing it is invisible
		}
		return e.err
	}
	rec := e.rec
	p.records = append(p.records, rec)
	p.last = rec.Sequence
	switch rec.Event.Type {
	case "permission.asked", "question.asked":
		p.pending[e.meta.id] = rec
		p.active = p.active || !e.replay
	case "permission.replied", "question.replied", "question.rejected":
		delete(p.pending, e.meta.requestID)
	case "session.status":
		p.limited = e.meta.status == "limited"
		switch e.meta.status {
		case "busy", "retry", "waiting", "limited":
			p.active = true
		}
	case "session.idle":
		if !p.limited {
			p.active = false
		}
	}
	if e.apply != nil {
		e.apply()
	}
	return nil
}

func (s *Supervisor) newLease(cursor uint64, expires time.Time) (string, error) {
	return s.newScopedLease(cursor, expires, "")
}

func (s *Supervisor) newScopedLease(cursor uint64, expires time.Time, session string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	s.leases[id] = cursorLease{cursor: cursor, expires: expires, session: session}
	if !expires.IsZero() {
		time.AfterFunc(time.Until(expires), func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if lease, ok := s.leases[id]; ok && !lease.expires.IsZero() {
				delete(s.leases, id)
				s.logPrune()
			}
		})
	}
	return id, nil
}

// prune truncates acknowledged turns, session by session: one session's
// failure is reported (joined) and leaves only that spool for the next prune.
// Called with s.mu held; it releases s.mu while a spool file is rewritten.
func (s *Supervisor) prune() error {
	sessions := make(map[string]*sessionSpool, len(s.sessions))
	maps.Copy(sessions, s.sessions) // s.sessions may grow while s.mu is released
	var errs []error
	for id, p := range sessions {
		if err := s.pruneSession(id, p); err != nil {
			errs = append(errs, fmt.Errorf("pruning spool of %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// logPrune prunes where no caller can report a failure.
func (s *Supervisor) logPrune() {
	if err := s.prune(); err != nil {
		log.WithError(err).Warn("provider supervisor: spool pruning failed; retrying on the next acknowledgment")
	}
}

// floor is the lowest sequence a consumer of session id may still need:
// leases scoped to another session do not hold it back.
func (s *Supervisor) floor(id string) uint64 {
	minimum := s.seq
	for lid, lease := range s.leases {
		if !lease.expires.IsZero() && time.Now().After(lease.expires) {
			delete(s.leases, lid)
			continue
		}
		if (lease.session == "" || lease.session == id) && lease.cursor < minimum {
			minimum = lease.cursor
		}
	}
	return minimum
}

// retainLimit caps the records a session keeps for lagging leases. Past it,
// leases behind the session's acknowledged turns are invalidated instead.
var retainLimit = 2048

// evictBehind invalidates the leases that hold session id below cursor: an
// unclaimed snapshot lease then fails with 409, and an attached stream ends,
// so either consumer takes a new snapshot.
func (s *Supervisor) evictBehind(id string, cursor uint64) {
	for lid, lease := range s.leases {
		if (lease.session == "" || lease.session == id) && lease.cursor < cursor {
			delete(s.leases, lid)
			if lease.expires.IsZero() {
				s.evicted[lid] = true
			}
			log.WithFields(log.Fields{"session": id, "cursor": lease.cursor}).Warn("provider supervisor: lease fell behind the retention cap; consumer must refresh its snapshot")
		}
	}
}

// pruneCut is how many leading records of p are prunable.
func pruneCut(p *sessionSpool, minimum uint64) int {
	// Retain whole turns (including part initialization), never a suffix.
	if p.ack == 0 || p.ack > minimum || len(p.pending) != 0 || p.pruning {
		return 0
	}
	// A prompt resolved after its turn was acknowledged keeps its ask:
	// a consumer replaying the resolution needs the asked metadata.
	late := map[string]bool{}
	for _, rec := range p.records {
		if rec.Sequence > p.ack {
			if id := resolvedID(rec.Event); id != "" {
				late[id] = true
			}
		}
	}
	cut := 0
	for cut < len(p.records) && p.records[cut].Sequence <= p.ack && !late[askedID(p.records[cut].Event)] {
		cut++
	}
	return cut
}

// pruneTestHook runs while a spool is rewritten without s.mu.
var pruneTestHook = func() {}

// pruneSession rewrites p's spool without its prunable head. The bulk rewrite
// runs without any lock; the swap holds p.io, so no append lands in the old
// file after the records that follow the head were copied.
func (s *Supervisor) pruneSession(id string, p *sessionSpool) error {
	minimum := s.floor(id)
	cut := pruneCut(p, minimum)
	// Only leases hold acknowledged turns: past the cap, they yield.
	if len(p.records)-cut > retainLimit && p.ack > minimum && pruneCut(p, p.ack) > cut {
		s.evictBehind(id, p.ack)
		cut = pruneCut(p, s.floor(id))
	}
	if cut == 0 {
		return nil
	}
	dropped := p.records[cut-1].Sequence
	keep := append([]record(nil), p.records[cut:]...)
	seen := len(p.records)
	p.pruning = true // only this call truncates p until it returns
	defer func() { p.pruning = false }()
	path := s.path(id)
	tmp := path + ".tmp"
	abandon := func(f *os.File) {
		if f != nil {
			_ = f.Close()
		}
		_ = os.Remove(tmp)
	}

	// The bulk rewrite runs unlocked: publishing goes on meanwhile.
	s.mu.Unlock()
	f, err := writeRecords(tmp, keep)
	pruneTestHook()
	if err != nil {
		s.mu.Lock()
		abandon(nil)
		return err
	}
	p.io.Lock() // no append now until the swap
	defer p.io.Unlock()
	s.mu.Lock()
	// Records synced meanwhile follow: exposed ones, then those still queued
	// for exposure.
	extra := append([]record(nil), p.records[seen:]...)
	for _, e := range p.queue {
		if e.state == entryDurable {
			extra = append(extra, e.rec)
		}
	}
	s.mu.Unlock()
	err = appendRecords(f, extra)
	if err == nil {
		err = f.Close()
		f = nil
	}
	s.mu.Lock()
	if err != nil {
		abandon(f)
		return err
	}
	// A consumer that attached meanwhile may still need what was cut.
	if s.floor(id) < dropped {
		abandon(nil)
		return nil
	}
	if err := os.Rename(tmp, path); err != nil {
		abandon(nil)
		return err
	}
	p.records = append(keep, p.records[seen:]...)
	return nil
}

// writeRecords creates path with recs, synced; the caller closes the file.
func writeRecords(path string, recs []record) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := appendRecords(f, recs); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// appendRecords writes recs to f and syncs it, like a spool append.
func appendRecords(f *os.File, recs []record) error {
	var data []byte
	for _, rec := range recs {
		encoded, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		data = append(data, append(encoded, '\n')...)
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return syncSpool(f)
}

func askedID(e providerprotocol.Event) string {
	if e.Type != "permission.asked" && e.Type != "question.asked" {
		return ""
	}
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(e.Properties, &p)
	return p.ID
}

func resolvedID(e providerprotocol.Event) string {
	if e.Type != "permission.replied" && e.Type != "question.replied" && e.Type != "question.rejected" {
		return ""
	}
	var p struct {
		RequestID string `json:"requestID"`
	}
	_ = json.Unmarshal(e.Properties, &p)
	return p.RequestID
}
