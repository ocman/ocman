package providersupervisor

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func ackedTurn(t *testing.T, s *Supervisor, id string) {
	t.Helper()
	publish(t, s, "session.status", fmt.Sprintf(`{"sessionID":%q,"status":{"type":"busy"}}`, id))
	publish(t, s, "session.idle", fmt.Sprintf(`{"sessionID":%q}`, id))
	s.mu.Lock()
	s.session(id).ack = s.seq
	s.mu.Unlock()
}

// One spool that cannot be rewritten never keeps the others from being
// pruned, and its failure is reported.
func TestPruneContinuesPastAFailingSession(t *testing.T) {
	s := supervisor(t)
	for i := range 20 {
		ackedTurn(t, s, fmt.Sprint("good", i))
	}
	ackedTurn(t, s, "bad")
	if err := os.Mkdir(s.path("bad")+".tmp", 0700); err != nil { // the rewrite cannot be created
		t.Fatal(err)
	}
	s.mu.Lock()
	err := s.prune()
	s.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("prune error = %v, want the failing session", err)
	}
	for i := range 20 {
		if n := len(s.sessions[fmt.Sprint("good", i)].records); n != 0 {
			t.Fatalf("good%d kept %d records behind a failing session", i, n)
		}
	}
	if len(s.sessions["bad"].records) != 2 {
		t.Fatal("the failing session lost records")
	}
}

// The rewrite runs without the lock: an event published meanwhile is kept,
// in memory and on disk.
func TestPruneRewritesWithoutBlockingPublish(t *testing.T) {
	s := supervisor(t)
	ackedTurn(t, s, "s")
	old := pruneTestHook
	t.Cleanup(func() { pruneTestHook = old })
	pruneTestHook = func() {
		pruneTestHook = func() {}
		publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
	}
	s.mu.Lock()
	err := s.prune()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.sessions["s"].records); n != 1 {
		t.Fatalf("records = %d, want the one published during the rewrite", n)
	}
	data, _ := os.ReadFile(s.path("s"))
	if strings.Count(string(data), "\n") != 1 || !strings.Contains(string(data), `"sequence":3`) {
		t.Fatalf("spool = %s", data)
	}
}

// A consumer that attached below the cut while the spool was rewritten keeps
// what it needs.
func TestPruneYieldsToAConsumerAttachedMeanwhile(t *testing.T) {
	s := supervisor(t)
	ackedTurn(t, s, "s")
	old := pruneTestHook
	t.Cleanup(func() { pruneTestHook = old })
	pruneTestHook = func() {
		s.mu.Lock()
		_, _ = s.newLease(0, time.Time{})
		s.mu.Unlock()
	}
	s.mu.Lock()
	err := s.prune()
	s.mu.Unlock()
	if err != nil || len(s.sessions["s"].records) != 2 {
		t.Fatalf("pruned below an attached consumer: %v", err)
	}
	if _, err := os.Stat(s.path("s") + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("abandoned rewrite left behind")
	}
}
