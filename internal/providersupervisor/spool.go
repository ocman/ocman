// Package providersupervisor hosts a provider independently of the dashboard.
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

type sessionSpool struct {
	records []record
	ack     uint64
	active  bool
	limited bool
	pending map[string]record
	pruning bool // a rewrite of this spool is in flight
}

type cursorLease struct {
	cursor  uint64
	expires time.Time // zero means an attached stream
}

// Supervisor serializes snapshot reads with publishing. Provider state must be
// updated only through Publish's apply callback, so snapshots cannot race deltas.
type Supervisor struct {
	mu             sync.Mutex
	dir            string
	seq            uint64
	sessions       map[string]*sessionSpool
	leases         map[string]cursorLease
	changed        chan struct{}
	draining       bool
	idleDrainToken string
	admitted       int
	backend        Backend
}

func New(dir string, backend Backend) (*Supervisor, error) {
	if dir == "" {
		return nil, fmt.Errorf("supervisor spool directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Supervisor{dir: dir, backend: backend, sessions: make(map[string]*sessionSpool), leases: make(map[string]cursorLease), changed: make(chan struct{})}
	return s, nil
}

// Active reports whether session id has an open turn or a pending prompt:
// what keeps /drain from reporting idle and its spool from truncating.
func (s *Supervisor) Active(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.sessions[id]
	return p != nil && (p.active || len(p.pending) != 0)
}

func (s *Supervisor) session(id string) *sessionSpool {
	p := s.sessions[id]
	if p == nil {
		p = &sessionSpool{pending: make(map[string]record)}
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
	return s.publish(event, apply)
}

// PublishIf publishes events in order only while ok holds, atomically with
// BeginTurn: a turn accepted after ok was checked cannot be followed by these
// events (a stale idle edge). ok and apply must not call back into Supervisor.
func (s *Supervisor) PublishIf(ok func() bool, apply func(providerprotocol.Event), events ...providerprotocol.Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok() {
		return false, nil
	}
	for _, event := range events {
		if err := s.publish(event, func() { apply(event) }); err != nil {
			return true, err
		}
	}
	return true, nil
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
	props, _ := json.Marshal(map[string]any{"sessionID": id, "status": map[string]string{"type": "busy"}})
	return s.publish(providerprotocol.Event{Type: "session.status", Properties: props}, apply)
}

func (s *Supervisor) publish(event providerprotocol.Event, apply func()) error {
	return s.append(event, apply, false)
}

// append records event. A replay re-delivers a pending prompt under a new
// sequence: it does not open a turn, so a prompt that outlived its turn's
// idle edge cannot hold the session active once it is resolved.
func (s *Supervisor) append(event providerprotocol.Event, apply func(), replay bool) error {
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
		return err
	}
	id := props.SessionID
	if id == "" {
		id = props.Info.SessionID
	}
	if id == "" {
		id = props.Part.SessionID
	}
	if id == "" {
		return fmt.Errorf("event missing sessionID")
	}
	if (event.Type == "permission.asked" || event.Type == "question.asked") && props.ID == "" {
		return fmt.Errorf("prompt missing id")
	}
	if s.seq == ^uint64(0) {
		return fmt.Errorf("event sequence exhausted")
	}
	p := s.session(id)
	rec := record{Sequence: s.seq + 1, Event: event, Completed: event.Type == "session.idle" && !p.limited}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(data) > providerprotocol.MaxEventBytes-128 {
		return fmt.Errorf("event exceeds size limit")
	}
	f, err := os.OpenFile(s.path(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	s.seq = rec.Sequence
	// Own the payload; callers often reuse a stream-json read buffer.
	rec.Event.Properties = append(json.RawMessage(nil), event.Properties...)
	p.records = append(p.records, rec)
	switch event.Type {
	case "permission.asked", "question.asked":
		p.pending[props.ID] = rec
		p.active = p.active || !replay
	case "permission.replied", "question.replied", "question.rejected":
		delete(p.pending, props.RequestID)
	case "session.status":
		p.limited = props.Status.Type == "limited"
		if props.Status.Type == "busy" || props.Status.Type == "retry" || props.Status.Type == "waiting" || props.Status.Type == "limited" {
			p.active = true
		}
	case "session.idle":
		if !p.limited {
			p.active = false
		}
	}
	if apply != nil {
		apply()
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}

func (s *Supervisor) newLease(cursor uint64, expires time.Time) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	s.leases[id] = cursorLease{cursor: cursor, expires: expires}
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

// floor is the lowest sequence a consumer may still need.
func (s *Supervisor) floor() uint64 {
	minimum := s.seq
	for id, lease := range s.leases {
		if !lease.expires.IsZero() && time.Now().After(lease.expires) {
			delete(s.leases, id)
			continue
		}
		if lease.cursor < minimum {
			minimum = lease.cursor
		}
	}
	return minimum
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

func (s *Supervisor) pruneSession(id string, p *sessionSpool) error {
	cut := pruneCut(p, s.floor())
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

	// The bulk rewrite runs unlocked: publishing goes on meanwhile.
	s.mu.Unlock()
	f, err := writeRecords(tmp, keep)
	pruneTestHook()
	s.mu.Lock()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// A consumer that attached meanwhile may still need what was cut.
	if s.floor() < dropped {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil
	}
	// Records published meanwhile follow, under the lock like any append.
	extra := p.records[seen:]
	if err := appendRecords(f, extra); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	p.records = append(keep, extra...)
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

// appendRecords writes recs to f and syncs it, like append does per event.
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
	return f.Sync()
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
