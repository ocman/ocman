package server

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type eventPlatform struct {
	*fakePlatform
	sink platforms.SessionEventSink
}

func (p *eventPlatform) SetSessionEventSink(sink platforms.SessionEventSink) { p.sink = sink }

func TestPlatformIdleSinkDrainsQueue(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	sent := 0
	p := &eventPlatform{fakePlatform: &fakePlatform{id: "fake", sendMessageFn: func(platforms.SendMessageRequest) error { sent++; return nil }}}
	reg.Register(p)
	postQueuedMessage(t, srv, "fake", "s1", "held")
	p.sink.OnSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent %d, want 1", sent)
	}
}

func TestPlatformStatusSink(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := &eventPlatform{fakePlatform: &fakePlatform{id: "fake"}}
	reg.Register(p)
	// Installing a sink also wires sources registered before the server.
	reg.SetSessionEventSink(srv.sessionEventSink())
	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()
	for _, tc := range []struct {
		platform, id     string
		status           db.SessionStatus
		reset, wantReset int64
	}{
		{"fake", "s1", db.StatusLimited, 12345, 12345},
		{"fake", "s1", db.StatusBusy, 12345, 0},
	} {
		p.sink.OnSessionStatus(tc.platform, tc.id, tc.status, tc.reset)
		event := <-sub.ch
		var body struct {
			Platform  string `json:"platform"`
			SessionID string `json:"sessionID"`
			Patch     struct {
				Status   db.SessionStatus `json:"status"`
				ResetsAt int64            `json:"resetsAt"`
			} `json:"patch"`
		}
		if err := json.Unmarshal(event.data, &body); err != nil {
			t.Fatal(err)
		}
		if body.Platform != "fake" || body.SessionID != "s1" || body.Patch.Status != tc.status || body.Patch.ResetsAt != tc.wantReset {
			t.Fatalf("event = %+v", body)
		}
	}
	p.sink.OnSessionStatus("", "s1", db.StatusBusy, 0)
	p.sink.OnSessionStatus("fake", "", db.StatusBusy, 0)
	select {
	case e := <-sub.ch:
		t.Fatalf("unexpected event %v", e)
	default:
	}
}

func TestStatusEventsCoalesceByOwner(t *testing.T) {
	a := coalesceKey("ocman.session.changed", []byte(`{"sessionID":"same","platform":"fake"}`))
	b := coalesceKey("ocman.session.changed", []byte(`{"sessionID":"same","platform":"r-A:fake"}`))
	if a == b {
		t.Fatal("status patches from different owners coalesced")
	}
}

func TestLimitedSessionDoesNotDrainSweep(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "detail", true: "lifecycle"}[bounded], func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			sent := 0
			p := &fakePlatform{id: "fake", sendMessageFn: func(platforms.SendMessageRequest) error { sent++; return nil },
				sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
					return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusLimited}}, nil
				}}
			if bounded {
				reg.Register(&lifecycleFake{fakePlatform: p, lifecycle: &platforms.SessionLifecycle{Status: db.StatusLimited}})
			} else {
				reg.Register(p)
			}
			postQueuedMessage(t, srv, "fake", "s1", "held")
			srv.queueSvc().Sweep(t.Context())
			if sent != 0 {
				t.Fatalf("limited session received %d messages", sent)
			}
			msgs, err := srv.queueSvc().List(t.Context(), "fake", "s1")
			if err != nil || len(msgs) != 1 {
				t.Fatalf("queue = %v, %v", msgs, err)
			}
		})
	}
}
