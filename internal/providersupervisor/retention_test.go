package providersupervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/providerprotocol"
)

func takeSnapshot(t *testing.T, s *Supervisor, id string) providerprotocol.Snapshot {
	t.Helper()
	var snapshot providerprotocol.Snapshot
	w := request(s, "GET", "/sessions/"+id+"/snapshot", "")
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || snapshot.Lease == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	return snapshot
}

func ackTurn(t *testing.T, s *Supervisor, id string, parts int) {
	t.Helper()
	publish(t, s, "session.status", fmt.Sprintf(`{"sessionID":%q,"status":{"type":"busy"}}`, id))
	for i := range parts {
		publish(t, s, "message.part.updated", fmt.Sprintf(`{"part":{"sessionID":%q,"id":"p%d"}}`, id, i))
	}
	publish(t, s, "session.idle", fmt.Sprintf(`{"sessionID":%q}`, id))
	if w := request(s, "POST", "/sessions/"+id+"/ack", fmt.Sprintf(`{"sequence":%d}`, s.seq)); w.Code != http.StatusNoContent {
		t.Fatal(w.Code, w.Body.String())
	}
}

func retained(s *Supervisor, id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions[id].records)
}

// A browser lease on session a, unclaimed or stalled, pins only a's records:
// session b's acknowledged turns are pruned past it.
func TestStalledLeaseRetainsOnlyItsSession(t *testing.T) {
	s := supervisor(t)
	ackTurn(t, s, "a", 1)
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"a","id":"open"}}`)
	snapshot := takeSnapshot(t, s, "a") // never claimed: the browser stalls
	ackTurn(t, s, "b", 3)
	if n := retained(s, "b"); n != 0 {
		t.Fatalf("session b retained %d records behind a's lease", n)
	}
	// a's lease still pins a's later turn until claimed.
	publish(t, s, "session.idle", `{"sessionID":"a"}`)
	if w := request(s, "POST", "/sessions/a/ack", fmt.Sprintf(`{"sequence":%d}`, s.seq)); w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
	if n := retained(s, "a"); n != 2 {
		t.Fatalf("session a retained %d records, want its lease's 2", n)
	}
	s.mu.Lock()
	_, ok := s.leases[snapshot.Lease]
	s.mu.Unlock()
	if !ok {
		t.Fatal("lease within the cap was invalidated")
	}
}

// Past the per-session cap, a lease that pins acknowledged records is
// invalidated instead: an unclaimed snapshot must be retaken (409).
func TestRetentionCapInvalidatesLaggingLease(t *testing.T) {
	s := supervisor(t)
	old := retainLimit
	t.Cleanup(func() { retainLimit = old })
	retainLimit = 4
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"a","id":"open"}}`)
	snapshot := takeSnapshot(t, s, "a")
	ackTurn(t, s, "a", 4) // 1 + 6 records > cap
	if n := retained(s, "a"); n != 0 {
		t.Fatalf("session a retained %d records past the cap", n)
	}
	w := request(s, "GET", fmt.Sprintf("/events?after=%d&lease=%s", snapshot.Cursor, snapshot.Lease), "")
	if w.Code != http.StatusConflict {
		t.Fatalf("invalidated lease = %d, want 409", w.Code)
	}
}

// Within the cap a lagging lease keeps what it needs.
func TestRetentionWithinCapKeepsLease(t *testing.T) {
	s := supervisor(t)
	old := retainLimit
	t.Cleanup(func() { retainLimit = old })
	retainLimit = 4
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"a","id":"open"}}`)
	takeSnapshot(t, s, "a")
	ackTurn(t, s, "a", 1) // 1 + 3 records == cap
	if n := retained(s, "a"); n != 4 {
		t.Fatalf("retained %d records, want 4 within the cap", n)
	}
}

// An attached stream whose lease was invalidated ends, so its consumer
// takes a new snapshot instead of silently missing pruned records.
func TestInvalidatedStreamEnds(t *testing.T) {
	s := supervisor(t)
	old := retainLimit
	t.Cleanup(func() { retainLimit = old })
	retainLimit = 2
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"a","id":"open"}}`)
	snapshot := takeSnapshot(t, s, "a")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	w := &stalledWriter{header: http.Header{}, writing: make(chan struct{}, 16), gate: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/events?after=%d&lease=%s", snapshot.Cursor, snapshot.Lease), nil).WithContext(ctx))
	}()
	publish(t, s, "session.status", `{"sessionID":"a","status":{"type":"busy"}}`)
	<-w.writing // the browser stalls on its first event
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"a","id":"p"}}`)
	publish(t, s, "session.idle", `{"sessionID":"a"}`)
	if w := request(s, "POST", "/sessions/a/ack", fmt.Sprintf(`{"sequence":%d}`, s.seq)); w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
	if n := retained(s, "a"); n != 0 {
		t.Fatalf("retained %d records behind a stalled stream past the cap", n)
	}
	close(w.gate) // the write completes
	select {
	case <-done:
		if ctx.Err() != nil {
			t.Fatal("invalidated stream ran until cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invalidated stream kept running")
	}
}

// stalledWriter is an SSE client whose writes block until gate closes.
type stalledWriter struct {
	header  http.Header
	writing chan struct{}
	gate    chan struct{}
}

func (w *stalledWriter) Header() http.Header { return w.header }
func (w *stalledWriter) WriteHeader(int)     {}
func (w *stalledWriter) Flush()              {}
func (w *stalledWriter) Write(b []byte) (int, error) {
	w.writing <- struct{}{}
	<-w.gate
	return len(b), nil
}
