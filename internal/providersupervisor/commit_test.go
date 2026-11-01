package providersupervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/providerprotocol"
)

// slowDisk blocks fsyncs of session id's spool until release is closed and
// counts them; other spools sync normally.
func slowDisk(t *testing.T, s *Supervisor, id string) (release func(), syncs *atomic.Int32, entered chan struct{}) {
	t.Helper()
	gate := make(chan struct{})
	entered = make(chan struct{}, 64)
	syncs = new(atomic.Int32)
	path := s.path(id)
	old := syncSpool
	t.Cleanup(func() { syncSpool = old })
	syncSpool = func(f *os.File) error {
		if f.Name() == path {
			syncs.Add(1)
			entered <- struct{}{}
			<-gate
		}
		return f.Sync()
	}
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release, syncs, entered
}

func event(kind, props string) providerprotocol.Event {
	return providerprotocol.Event{Type: kind, Properties: json.RawMessage(props)}
}

func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked behind another session's fsync", what)
	}
}

// One session's slow fsync holds neither the supervisor's routes nor other
// sessions' publishers, and its own event stays unexposed until durable.
func TestSlowSpoolDoesNotBlockOtherSessions(t *testing.T) {
	s := supervisor(t)
	release, _, entered := slowDisk(t, s, "slow")
	var applied atomic.Bool
	slowDone := make(chan error, 1)
	go func() {
		slowDone <- s.Publish(event("message.part.updated", `{"part":{"sessionID":"slow","id":"p","text":"x"}}`), func() { applied.Store(true) })
	}()
	<-entered

	within(t, "concurrent publishers", func() {
		var wg sync.WaitGroup
		for i := range 4 {
			wg.Go(func() {
				for j := range 25 {
					if err := s.Publish(event("message.part.updated", fmt.Sprintf(`{"part":{"sessionID":"fast%d","id":"p%d","text":"x"}}`, i, j)), nil); err != nil {
						t.Error(err)
					}
				}
			})
		}
		wg.Wait()
	})
	within(t, "/hello", func() { request(s, "GET", "/hello", "") })
	within(t, "/drain-idle", func() {
		if w := request(s, "POST", "/drain-idle?token=t", ""); strings.Contains(w.Body.String(), `"idle":true`) {
			t.Error("idle while a record is on its way to its spool")
		}
	})
	var snapshot providerprotocol.Snapshot
	within(t, "snapshot", func() {
		_ = json.Unmarshal(request(s, "GET", "/sessions/fast0/snapshot", "").Body.Bytes(), &snapshot)
	})
	s.mu.Lock()
	fastLast := s.sessions["fast0"].last
	slowExposed := len(s.sessions["slow"].records)
	s.mu.Unlock()
	if snapshot.Cursor != fastLast || snapshot.Lease == "" {
		t.Fatalf("snapshot cursor %d, want the session's last applied %d", snapshot.Cursor, fastLast)
	}
	if applied.Load() || slowExposed != 0 {
		t.Fatal("a record was exposed before its fsync returned")
	}
	select {
	case err := <-slowDone:
		t.Fatalf("publish returned before its fsync: %v", err)
	default:
	}
	release()
	if err := <-slowDone; err != nil || !applied.Load() {
		t.Fatalf("slow publish = %v, applied %v", err, applied.Load())
	}
}

// Publishers queued behind a slow fsync share the next one (group commit),
// and are exposed and applied in sequence order.
func TestGroupCommitAppliesInSequenceOrder(t *testing.T) {
	s := supervisor(t)
	release, syncs, entered := slowDisk(t, s, "s")
	var order []uint64
	apply := func() {
		// Applied under s.mu, after its record is durable.
		order = append(order, s.sessions["s"].last)
	}
	errs := make(chan error, 8)
	go func() { errs <- s.Publish(event("message.part.updated", `{"part":{"sessionID":"s","id":"p0"}}`), apply) }()
	<-entered
	for i := 1; i < 8; i++ {
		go func() {
			errs <- s.Publish(event("message.part.updated", fmt.Sprintf(`{"part":{"sessionID":"s","id":"p%d"}}`, i)), apply)
		}()
	}
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.sessions["s"].queue) == 8 })
	release()
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := syncs.Load(); n != 2 {
		t.Fatalf("fsyncs = %d, want the queued publishers in one group commit", n)
	}
	for i, seq := range order {
		if seq != uint64(i+1) {
			t.Fatalf("applied out of order: %v", order)
		}
	}
	data, _ := os.ReadFile(s.path("s"))
	if strings.Count(string(data), "\n") != 8 {
		t.Fatalf("spool = %s", data)
	}
}

// An unscoped stream never delivers a sequence ahead of an unsettled one:
// its exclusive cursor would skip the earlier record once exposed.
func TestUnscopedStreamWaitsForEarlierSequences(t *testing.T) {
	s := supervisor(t)
	release, _, entered := slowDisk(t, s, "slow")
	go func() { _ = s.Publish(event("session.status", `{"sessionID":"slow","status":{"type":"busy"}}`), nil) }()
	<-entered
	publish(t, s, "session.status", `{"sessionID":"fast","status":{"type":"busy"}}`)

	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events?after=0", nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	ids := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if id, ok := strings.CutPrefix(scanner.Text(), "id: "); ok {
				ids <- id
			}
		}
	}()
	select {
	case id := <-ids:
		t.Fatalf("delivered %s ahead of an unsettled sequence", id)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	for _, want := range []string{"1", "2"} {
		if got := <-ids; got != want {
			t.Fatalf("delivered %s, want %s", got, want)
		}
	}
}

// A barrier's condition observes every earlier apply: PublishIf waits for an
// in-flight publish to settle before checking ok.
func TestBarrierObservesEarlierApplies(t *testing.T) {
	s := supervisor(t)
	release, _, entered := slowDisk(t, s, "s")
	applied := false // guarded by s.mu: apply and ok run under it
	go func() {
		_ = s.Publish(event("session.status", `{"sessionID":"s","status":{"type":"busy"}}`), func() { applied = true })
	}()
	<-entered
	result := make(chan bool, 1)
	go func() {
		var seen bool
		_, _ = s.PublishIf(func() bool { seen = applied; return true }, func(providerprotocol.Event) {}, event("session.idle", `{"sessionID":"s"}`))
		result <- seen
	}()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.barriers == 1 })
	// A publisher arriving after the barrier waits for it too.
	later := make(chan error, 1)
	go func() { later <- s.Publish(event("session.status", `{"sessionID":"other","status":{"type":"busy"}}`), nil) }()
	release()
	if !<-result {
		t.Fatal("PublishIf checked its condition before an earlier apply")
	}
	if err := <-later; err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions["s"].records[1].Sequence != 2 || !s.sessions["s"].records[1].Completed || s.sessions["other"].records[0].Sequence != 3 {
		t.Fatal("barrier and later publisher out of order")
	}
}

// A drain that starts while BeginTurn waits for earlier records still fences
// the turn, and a turn being committed keeps the supervisor from idling.
func TestBeginTurnRechecksDrainAfterWaiting(t *testing.T) {
	s := supervisor(t)
	release, _, entered := slowDisk(t, s, "s")
	go func() { _ = s.Publish(event("message.part.updated", `{"part":{"sessionID":"s","id":"p"}}`), nil) }()
	<-entered
	began := make(chan error, 1)
	go func() { began <- s.BeginTurn("next", nil) }()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.barriers == 1 })
	if !s.Active("next") {
		t.Fatal("a turn being admitted reads inactive")
	}
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal(w.Body.String())
	}
	release()
	if err := <-began; err == nil {
		t.Fatal("turn admitted after drain")
	}
	if s.Active("next") {
		t.Fatal("refused turn left active")
	}
}

// A failed group commit fails every publisher in it; none is exposed.
func TestFailedGroupCommitExposesNothing(t *testing.T) {
	s := supervisor(t)
	old := syncSpool
	t.Cleanup(func() { syncSpool = old })
	syncSpool = func(*os.File) error { return fmt.Errorf("disk full") }
	applied := false
	if err := s.Publish(event("session.status", `{"sessionID":"s","status":{"type":"busy"}}`), func() { applied = true }); err == nil || applied {
		t.Fatal("failed fsync exposed", err)
	}
	if _, err := s.PublishIf(func() bool { return true }, func(providerprotocol.Event) { applied = true }, event("session.idle", `{"sessionID":"s"}`)); err == nil || applied {
		t.Fatal("failed fsync exposed by a barrier", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions["s"].records) != 0 || len(s.unresolved) != 0 || s.seq != 0 {
		t.Fatalf("records %d unresolved %d seq %d", len(s.sessions["s"].records), len(s.unresolved), s.seq)
	}
}

// A prune swapping a spool keeps records that became durable meanwhile but
// were not yet exposed.
func TestPruneKeepsDurableUnexposedRecords(t *testing.T) {
	s := supervisor(t)
	ackedTurn(t, s, "s")
	e := &entry{id: "s", spool: s.sessions["s"], state: entryDurable, rec: record{Sequence: 3, Event: event("session.status", `{"sessionID":"s","status":{"type":"busy"}}`)}}
	s.mu.Lock()
	s.sessions["s"].queue = append(s.sessions["s"].queue, e)
	err := s.prune()
	s.sessions["s"].queue = nil
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.path("s"))
	if strings.Count(string(data), "\n") != 1 || !strings.Contains(string(data), `"sequence":3`) {
		t.Fatalf("spool = %s", data)
	}
}

// A headless replay collects pending prompts only after earlier writes have
// settled: a reply still syncing when the replay starts resolves its ask, so
// the replay must not re-publish it.
func TestHeadlessReplaySkipsPromptResolvedMeanwhile(t *testing.T) {
	s := supervisor(t)
	publish(t, s, "permission.asked", `{"sessionID":"s","id":"p"}`)
	release, _, entered := slowDisk(t, s, "s")
	replied := make(chan error, 1)
	go func() {
		replied <- s.Publish(event("permission.replied", `{"sessionID":"s","requestID":"p","reply":"once"}`), nil)
	}()
	<-entered

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan struct{})
	go func() {
		defer close(served)
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events?consumer=headless&after=1", nil).WithContext(ctx))
	}()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.barriers == 1 })
	release()
	if err := <-replied; err != nil {
		t.Fatal(err)
	}
	// The replay has committed once no barrier waits or commits.
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.barriers == 0 && !s.exclusive })
	cancel()
	<-served
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.sessions["s"].records[2:] {
		t.Errorf("replayed a resolved prompt: %s %s", rec.Event.Type, rec.Event.Properties)
	}
	if len(s.sessions["s"].pending) != 0 {
		t.Fatal("resolved prompt is pending again")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
