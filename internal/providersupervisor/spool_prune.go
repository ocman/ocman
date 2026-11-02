package providersupervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

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
