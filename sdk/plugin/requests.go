package plugin

import "time"

// Request asks the host to perform one unary operation. Replies must have space
// for its terminal result. IDs are assigned independently of host call IDs.
type Request struct {
	Call    Call
	Replies chan Result
}

type requestsState struct {
	next      uint64
	pending   map[uint64]Request
	cancelled map[uint64]bool
}

func newRequests() *requestsState {
	return &requestsState{pending: map[uint64]Request{}, cancelled: map[uint64]bool{}}
}

func (s *requestsState) send(r Request, stream *Stream, encoder *Encoder) error {
	if cap(r.Replies) < 1 {
		return ErrInvalidMessage
	}
	s.next++
	r.Call.ID = s.next
	e := Envelope{Type: TypeCall, Call: &r.Call}
	// Include the consumed request in shutdown settlement if admission fails.
	s.pending[r.Call.ID] = r
	if err := stream.Accept(FromPlugin, e); err != nil {
		return err
	}
	return encoder.Encode(e)
}

func (s *requestsState) result(result Result) error {
	r := s.pending[result.ID]
	if r.Replies != nil {
		select {
		case r.Replies <- result:
		default:
			return ErrStreamOrder
		}
	}
	delete(s.pending, result.ID)
	delete(s.cancelled, result.ID)
	return nil
}

func (s *requestsState) expire(stream *Stream, encoder *Encoder) error {
	for id, r := range s.pending {
		if s.cancelled[id] || time.Now().UnixMilli() < r.Call.DeadlineUnixMS {
			continue
		}
		e := Envelope{Type: TypeCancel, Cancel: &Cancel{ID: id}}
		if err := stream.Accept(FromPlugin, e); err != nil {
			return err
		}
		if err := encoder.Encode(e); err != nil {
			return err
		}
		s.cancelled[id] = true
		select {
		case r.Replies <- Result{ID: id, Error: &WireError{Category: ErrorDeadlineExceeded}}:
		default:
			return ErrStreamOrder
		}
		r.Replies = nil
		s.pending[id] = r
	}
	return nil
}

func (s *requestsState) close() {
	for id, r := range s.pending {
		if r.Replies != nil {
			select {
			case r.Replies <- Result{ID: id, Error: &WireError{Category: ErrorUnavailable}}:
			default:
			}
		}
	}
}
