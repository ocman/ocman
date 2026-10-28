package owned

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type gatedSource struct {
	sourceReader
	once             sync.Once
	entered, release chan struct{}
}

func (s *gatedSource) HistoryIdentity(ctx context.Context) (platforms.HistoryIdentity, error) {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.sourceReader.HistoryIdentity(ctx)
}

type eventPlatform struct {
	*nativePlatform
	sink platforms.SessionEventSink
}

func (p *eventPlatform) SetSessionEventSink(sink platforms.SessionEventSink) { p.sink = sink }

func TestLifecycleCallbacksUseCanonicalOwnerIdentityAndKeepQuotaMetadata(t *testing.T) {
	a, p, _ := fixture(t)
	native := &eventPlatform{nativePlatform: p}
	a.native = native
	var events []string
	a.SetSessionEventSink(platforms.SessionEventSink{
		OnSessionStatus: func(platform, id string, status db.SessionStatus, resetsAt int64) {
			if platform != "test" || !sessioncatalog.IsID(id) {
				t.Fatalf("native lifecycle metadata escaped: %s %s", platform, id)
			}
			if len(events) == 0 && (status != db.StatusLimited || resetsAt != 123) {
				t.Fatalf("quota lifecycle metadata changed: %s %d", status, resetsAt)
			}
			events = append(events, id)
		},
		OnSessionIdle: func(platform, id string) {
			if platform != "test" || len(events) != 2 || events[0] != id || events[1] != id {
				t.Fatalf("idle identity differs from status: %s %s %+v", platform, id, events)
			}
			events = append(events, id)
		},
	})
	native.sink.OnSessionStatus("test", "native", db.StatusLimited, 123)
	native.sink.OnSessionStatus("test", "native", db.StatusWaiting, 0)
	native.sink.OnSessionIdle("test", "native")
	if len(events) != 3 {
		t.Fatalf("lost lifecycle callbacks: %+v", events)
	}
	native.sink.OnSessionStatus("unrelated", "native", db.StatusBusy, 0)
	if len(events) != 3 {
		t.Fatal("unrelated source published lifecycle")
	}
}

func TestLifecycleEventsDuringAllocationWaitForCanonicalPublicationCommit(t *testing.T) {
	a, p, _ := fixture(t)
	native := &eventPlatform{nativePlatform: p}
	a.native = native
	var statusID, idleID string
	a.SetSessionEventSink(platforms.SessionEventSink{OnSessionStatus: func(_ string, id string, status db.SessionStatus, _ int64) {
		statusID = id
		if status != db.StatusWaiting {
			t.Fatalf("deferred status changed: %s", status)
		}
	}, OnSessionIdle: func(_ string, id string) { idleID = id }})
	p.beforeCreate = func(platforms.CreateSessionRequest) {
		native.sink.OnSessionIdle("test", "created-native")
		native.sink.OnSessionStatus("test", "created-native", db.StatusWaiting, 0)
		if statusID != "" || idleID != "" {
			t.Fatal("native creation event published before binding")
		}
	}
	created, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if statusID != "" || idleID != "" {
		t.Fatal("configuring identity acquired lifecycle authority")
	}
	if err := a.CommitCreation(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if statusID != created.ID || idleID != created.ID {
		t.Fatalf("deferred canonical lifecycle lost: %s %s %+v", statusID, idleID, created)
	}
}

func TestLifecycleCallbacksDrainNewEventsWithoutNativeContentReads(t *testing.T) {
	a, p, _ := fixture(t)
	native := &eventPlatform{nativePlatform: p}
	a.native = native
	count := 0
	a.SetSessionEventSink(platforms.SessionEventSink{OnSessionStatus: func(_ string, _ string, status db.SessionStatus, _ int64) {
		count++
		if status == db.StatusBusy {
			native.sink.OnSessionStatus("test", "native", db.StatusWaiting, 0)
		}
	}})
	native.sink.OnSessionStatus("test", "native", db.StatusBusy, 0)
	if count != 2 {
		t.Fatalf("event arriving during dispatch was left unflushed: %d", count)
	}
}

func TestDeferredIdleSurvivesConcurrentTerminalStatus(t *testing.T) {
	a, p, store := fixture(t)
	native := &eventPlatform{nativePlatform: p}
	a.native = native
	row, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	idle := false
	a.SetSessionEventSink(platforms.SessionEventSink{OnSessionStatus: func(string, string, db.SessionStatus, int64) {}, OnSessionIdle: func(_ string, id string) { idle = id == row.ID }})
	gate := &gatedSource{sourceReader: a.reader, entered: make(chan struct{}), release: make(chan struct{})}
	a.reader = gate
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate.release) }) })
	done := make(chan struct{})
	go func() { defer close(done); native.sink.OnSessionIdle("test", "native") }()
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		t.Fatal("lifecycle flush did not start")
	}
	native.sink.OnSessionStatus("test", "native", db.StatusWaiting, 0)
	release.Do(func() { close(gate.release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle flush did not complete")
	}
	if _, err := store.Attach(t.Context(), row.ID, a.binding("native")); err != nil {
		t.Fatal(err)
	}
	if err := a.CommitCreation(t.Context(), row.ID); err != nil {
		t.Fatal(err)
	}
	if !idle {
		t.Fatal("concurrent terminal status erased deferred idle edge")
	}
}
