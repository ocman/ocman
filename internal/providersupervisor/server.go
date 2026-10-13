package providersupervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// Backend supplies provider-specific operations and current state as v1
// upserts. Snapshot is called under the publishing lock and must not publish.
// Turn-start handlers must synchronously call BeginTurn before scheduling child
// work or returning success. Handler return alone cannot establish turn state.
type Backend struct {
	Hello    providerprotocol.Hello
	Handler  http.Handler
	Snapshot func(context.Context, string) ([]providerprotocol.Event, error)
}

func wireError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(providerprotocol.Error{Message: err.Error()})
}

func (s *Supervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/hello":
		hello := s.backend.Hello
		hello.Capabilities.Replay = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hello)
	case r.Method == http.MethodGet && r.URL.Path == "/events":
		s.events(w, r)
	case r.Method == http.MethodPost && (r.URL.Path == "/drain" || r.URL.Path == "/drain-idle"):
		s.mu.Lock()
		idle := s.admitted == 0
		for _, p := range s.sessions {
			if p.active || len(p.pending) != 0 {
				idle = false
			}
		}
		if idle || r.URL.Path == "/drain" {
			s.draining = true
		}
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(providerprotocol.DrainState{Idle: idle})
	case r.Method == http.MethodPost && r.URL.Path == "/resume":
		s.mu.Lock()
		s.draining = false
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(providerprotocol.DrainState{})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/snapshot"):
		s.snapshot(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ack"):
		s.ack(w, r)
	default:
		s.mu.Lock()
		draining := s.draining
		startsTurn := r.Method != http.MethodGet && !strings.HasPrefix(r.URL.Path, "/permissions/") && !strings.HasPrefix(r.URL.Path, "/questions/") && !strings.HasSuffix(r.URL.Path, "/abort")
		if startsTurn && !draining {
			s.admitted++
		}
		s.mu.Unlock()
		// Replies and abort remain usable while draining; no new turn can start.
		if draining && startsTurn {
			wireError(w, 409, fmt.Errorf("supervisor is draining"))
			return
		}
		if startsTurn {
			defer func() { s.mu.Lock(); s.admitted--; s.mu.Unlock() }()
		}
		if s.backend.Handler == nil {
			wireError(w, 501, fmt.Errorf("provider operation unavailable"))
			return
		}
		s.backend.Handler.ServeHTTP(w, r)
	}
}

func sessionRoute(path, suffix string) string {
	if !strings.HasPrefix(path, "/sessions/") || !strings.HasSuffix(path, suffix) {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, "/sessions/"), suffix)
	if strings.Contains(id, "/") {
		return ""
	}
	return id
}

func (s *Supervisor) snapshot(w http.ResponseWriter, r *http.Request) {
	id := sessionRoute(r.URL.Path, "/snapshot")
	if id == "" {
		wireError(w, 400, fmt.Errorf("invalid session"))
		return
	}
	s.mu.Lock()
	if s.backend.Snapshot == nil {
		s.mu.Unlock()
		wireError(w, 501, fmt.Errorf("snapshot unavailable"))
		return
	}
	events, err := s.backend.Snapshot(r.Context(), id)
	if err != nil {
		s.mu.Unlock()
		wireError(w, 500, err)
		return
	}
	// Pending prompts are state, and are present even when before the cursor.
	owned := make([]providerprotocol.Event, 0, len(events)+len(s.session(id).pending))
	for _, event := range events {
		event.Properties = append(json.RawMessage(nil), event.Properties...)
		owned = append(owned, event)
	}
	for _, rec := range s.session(id).pending {
		event := rec.Event
		event.Properties = append(json.RawMessage(nil), event.Properties...)
		owned = append(owned, event)
	}
	lease, err := s.newLease(s.seq, time.Now().Add(time.Minute))
	cursor := s.seq
	s.mu.Unlock()
	if err != nil {
		wireError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(providerprotocol.Snapshot{Cursor: cursor, Lease: lease, Events: owned})
}

func (s *Supervisor) ack(w http.ResponseWriter, r *http.Request) {
	id := sessionRoute(r.URL.Path, "/ack")
	var ack providerprotocol.TurnAck
	if id == "" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&ack) != nil {
		wireError(w, 400, fmt.Errorf("invalid acknowledgment"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.session(id)
	valid := ack.Sequence != 0 && ack.Sequence == p.ack
	for _, rec := range p.records {
		if rec.Sequence == ack.Sequence && rec.Completed {
			valid = true
		}
	}
	if !valid {
		wireError(w, 409, fmt.Errorf("acknowledgment is not a completed turn"))
		return
	}
	if ack.Sequence > p.ack {
		p.ack = ack.Sequence
	}
	if err := s.prune(); err != nil {
		wireError(w, 500, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Supervisor) events(w http.ResponseWriter, r *http.Request) {
	after := uint64(0)
	var err error
	if value := r.URL.Query().Get("after"); value != "" {
		after, err = strconv.ParseUint(value, 10, 64)
	}
	if err != nil {
		wireError(w, 400, fmt.Errorf("invalid cursor"))
		return
	}
	s.mu.Lock()
	lease := r.URL.Query().Get("lease")
	if lease != "" {
		pin, ok := s.leases[lease]
		if !ok || pin.cursor != after || pin.expires.IsZero() || time.Now().After(pin.expires) {
			s.mu.Unlock()
			wireError(w, 409, fmt.Errorf("snapshot lease expired; refresh snapshot"))
			return
		}
		s.leases[lease] = cursorLease{cursor: after}
	} else {
		if after > s.seq {
			s.mu.Unlock()
			wireError(w, 409, fmt.Errorf("cursor ahead of supervisor"))
			return
		}
		lease, err = s.newLease(after, time.Time{})
		if err != nil {
			s.mu.Unlock()
			wireError(w, 500, err)
			return
		}
	}
	// Headless reconnects must see unresolved requests even when after their
	// original event. New sequences preserve strict exclusive-cursor semantics.
	headless := r.URL.Query().Get("consumer") == "headless"
	if headless {
		var pending []providerprotocol.Event
		for _, p := range s.sessions {
			for _, rec := range p.pending {
				if rec.Sequence <= after {
					pending = append(pending, rec.Event)
				}
			}
		}
		for _, event := range pending {
			if err = s.append(event, nil, true); err != nil {
				break
			}
		}
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.leases, lease); _ = s.prune(); s.mu.Unlock() }()
	if err != nil {
		wireError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		wireError(w, 500, fmt.Errorf("streaming unsupported"))
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		s.mu.Lock()
		var batch []record
		for _, p := range s.sessions {
			for _, rec := range p.records {
				if rec.Sequence > after {
					if headless && rec.Sequence <= p.ack && strings.HasPrefix(rec.Event.Type, "session.") {
						rec.Event.Acknowledged = true // history kept for a late resolution
					}
					batch = append(batch, rec)
				}
			}
		}
		notify := s.changed
		s.mu.Unlock()
		sort.Slice(batch, func(i, j int) bool { return batch[i].Sequence < batch[j].Sequence })
		for _, rec := range batch {
			data, encodeErr := json.Marshal(rec.Event)
			if encodeErr != nil {
				return
			}
			// A slow browser cannot hold a spool forever or prevent draining.
			controller := http.NewResponseController(w)
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", rec.Sequence, data); err != nil {
				return
			}
			flusher.Flush()
			after = rec.Sequence
			s.mu.Lock()
			s.leases[lease] = cursorLease{cursor: after}
			err = s.prune()
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-notify:
		case <-heartbeat.C:
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Serve owns only a private unix socket. A dashboard restart does not cancel
// this process's context or its provider children.
func (s *Supervisor) Serve(ctx context.Context, socket string) error {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
