package plugins

import (
	"crypto/subtle"
	"slices"
)

type Direction uint8

const (
	FromHost Direction = iota
	FromPlugin
)

// Stream validates the ordered combination of both directions of one process
// connection. The owner serializes Accept calls with writes and received frames.
// It owns no goroutines, clocks, processes, or capability implementations.
type Stream struct {
	mode       Mode
	token      string
	protocol   Version
	supported  []Capability
	negotiated Negotiated
	offered    bool
	ready      bool
	closed     bool
	limit      int
	lastID     uint64
	active     map[uint64]uint64 // request ID -> last chunk sequence; bounded by limit
}

func NewStream(mode Mode, token string, protocol Version, supported []Capability) (*Stream, error) {
	if !validMode(mode) || !validToken(token) || !validVersion(protocol) || validateCapabilities(supported) != nil {
		return nil, ErrInvalidMessage
	}
	return &Stream{mode: mode, token: token, protocol: protocol, supported: append([]Capability(nil), supported...), active: make(map[uint64]uint64)}, nil
}

// Negotiation returns the selection after the plugin's hello, plus readiness.
// In serve mode the host must acknowledge this selection before the stream is
// ready. In describe mode readiness means the one response has been received.
func (s *Stream) Negotiation() (Negotiated, bool) {
	n := s.negotiated
	n.Capabilities = append([]Capability(nil), n.Capabilities...)
	return n, s.ready
}

// Accept fails closed. Cancellation is advisory and repeatable while a call is
// active: in-flight chunks and a racing successful result remain valid. Only
// result releases the concurrency slot. Shutdown abandons all outstanding calls;
// the supervisor must settle them locally and await process exit.
func (s *Stream) Accept(direction Direction, e Envelope) error {
	if s.closed {
		return ErrStreamOrder
	}
	err := s.accept(direction, e)
	if err != nil {
		s.closed = true
		s.token = ""
		clear(s.active)
	}
	return err
}

func (s *Stream) accept(direction Direction, e Envelope) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if direction != FromHost && direction != FromPlugin {
		return ErrStreamOrder
	}
	if !s.offered {
		if direction != FromPlugin || e.Hello == nil || e.Hello.Description == nil || e.Hello.Mode != s.mode || subtle.ConstantTimeCompare([]byte(e.Hello.Token), []byte(s.token)) != 1 {
			return ErrHandshake
		}
		n, err := Negotiate(s.protocol, s.supported, *e.Hello.Description)
		if err != nil {
			return err
		}
		s.negotiated, s.limit, s.offered, s.token = n, e.Hello.Description.MaxConcurrency, true, ""
		if s.mode == ModeDescribe {
			s.ready, s.closed = true, true
		}
		return nil
	}
	if !s.ready {
		if direction != FromHost || e.Hello == nil || e.Hello.Accepted == nil ||
			e.Hello.Accepted.Protocol != s.negotiated.Protocol || !slices.Equal(e.Hello.Accepted.Capabilities, s.negotiated.Capabilities) {
			return ErrHandshake
		}
		s.ready = true
		return nil
	}
	switch e.Type {
	case TypeCall:
		c := e.Call
		if direction != FromHost || c.ID <= s.lastID || len(s.active) >= s.limit || !s.hasCapabilityVersion(c.Capability, c.Version) {
			return ErrStreamOrder
		}
		s.lastID = c.ID
		s.active[c.ID] = 0
	case TypeChunk:
		previous, ok := s.active[e.Chunk.ID]
		if direction != FromPlugin || !ok || e.Chunk.Sequence != previous+1 {
			return ErrStreamOrder
		}
		s.active[e.Chunk.ID] = e.Chunk.Sequence
	case TypeResult:
		if _, ok := s.active[e.Result.ID]; direction != FromPlugin || !ok {
			return ErrStreamOrder
		}
		delete(s.active, e.Result.ID)
	case TypeEvent:
		if direction != FromPlugin || !s.hasCapability(e.Event.Capability) {
			return ErrStreamOrder
		}
	case TypeCancel:
		if _, ok := s.active[e.Cancel.ID]; direction != FromHost || !ok {
			return ErrStreamOrder
		}
	case TypeShutdown:
		if direction != FromHost {
			return ErrStreamOrder
		}
		s.closed = true
		clear(s.active)
	default:
		return ErrStreamOrder
	}
	return nil
}

func (s *Stream) hasCapability(name string) bool {
	for _, c := range s.negotiated.Capabilities {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (s *Stream) hasCapabilityVersion(name string, version Version) bool {
	for _, c := range s.negotiated.Capabilities {
		if c.Name == name && c.Version == version {
			return true
		}
	}
	return false
}
