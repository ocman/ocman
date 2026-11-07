package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/state"
)

type compactEventPlatform struct {
	*fakePlatform
	reads atomic.Int32
}

func (p *compactEventPlatform) SidebarSession(_ context.Context, id string) (*db.Session, error) {
	p.reads.Add(1)
	return &db.Session{ID: id, Platform: string(p.ID()), Title: "committed title", Status: db.StatusWaiting, LastTurnCompletedAt: 42}, nil
}

func TestSessionChangesCarryCompactRows(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := &compactEventPlatform{fakePlatform: &fakePlatform{PlatformID: "fake"}}
	reg.Register(p)
	seedPRWatch(t, srv, "fake", "s1", true)
	if err := srv.stateDB.RecordSessionCreator(t.Context(), state.SessionCreator{Platform: "fake", SessionID: "s1", CreatorPlatform: "fake", CreatorSessionID: "parent", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	updates := srv.sessionUpdatesModule()
	if err := updates.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); updates.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()
	sub2, unsub2 := srv.broadcastHub.subscribe()
	defer unsub2()
	for range 100 {
		srv.broadcastSidebarChanged("fake", "s1")
	}
	select {
	case event := <-sub.ch:
		var payload struct {
			Session  *db.Session
			Patch    *db.Session
			Revision uint64
		}
		if err := json.Unmarshal(event.data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Session == nil || payload.Patch == nil || payload.Session.Title != "committed title" || payload.Revision == 0 {
			t.Fatalf("missing compact snapshot: %s", event.data)
		}
		if payload.Session.Creator == nil || payload.Session.Creator.SessionID != "parent" || payload.Session.BackgroundTaskCount != 1 {
			t.Fatalf("lost row enrichment: %+v", payload.Session)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no compact event")
	}
	<-sub2.ch
	if n := p.reads.Load(); n != 1 {
		t.Fatalf("100 changes / two subscribers made %d compact reads", n)
	}
}

func TestParkedSnapshotCannotOverwriteNewerStatusRevision(t *testing.T) {
	h := newBroadcastHub()
	sub, stop := h.subscribe()
	defer stop()
	for range cap(sub.ch) {
		h.broadcast("filler", []byte(`{}`))
	}
	h.broadcast("ocman.session.changed", []byte(`{"platform":"p","sessionID":"s","revision":10,"patch":{"status":"busy"}}`))
	h.broadcast("ocman.session.changed", []byte(`{"platform":"p","sessionID":"s","revision":5,"patch":{"status":"waiting","title":"old"}}`))
	events := sub.takeBatch()
	var got struct {
		Revision int
		Patch    struct{ Status, Title string }
	}
	if err := json.Unmarshal(events[len(events)-1].data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Revision != 10 || got.Patch.Status != "busy" || got.Patch.Title != "" {
		t.Fatal(got)
	}
}
