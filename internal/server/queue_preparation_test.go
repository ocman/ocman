package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type coldQueueOwner struct {
	*fakePlatform
	blockAt, reads              int
	entered, release, cancelled chan struct{}
	t                           *testing.T
}

func (p *coldQueueOwner) Session(ctx context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.t.Error("sender preparation read has no deadline")
	}
	p.reads++
	// A successful model lookup now supplies first-prompt metadata too.
	// Exercise the separate fallback read only when that lookup fails.
	if p.blockAt == 2 && p.reads == 1 {
		return nil, errors.New("project metadata unavailable")
	}
	if p.reads == p.blockAt {
		close(p.entered)
		select {
		case <-ctx.Done():
			close(p.cancelled)
			return nil, ctx.Err()
		case <-p.release:
		}
	}
	return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo", Status: db.StatusDone}}, nil
}

func (*coldQueueOwner) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return &platforms.SessionLifecycle{Status: db.StatusDone}, nil
}

func TestQueueShutdownCancelsRealSenderPreparation(t *testing.T) {
	for _, blockAt := range []int{1, 2} {
		t.Run(map[int]string{1: "model", 2: "first-prompt"}[blockAt], func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			sent := make(chan struct{}, 1)
			p := &coldQueueOwner{
				fakePlatform: &fakePlatform{id: "r-cold:opencode", sendMessageFn: func(platforms.SendMessageRequest) error { sent <- struct{}{}; return nil }},
				blockAt:      blockAt, entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{}), t: t,
			}
			reg.Register(p)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m := s.queueModule()
			if err := m.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.Service().Enqueue(t.Context(), "r-cold:opencode", true, platforms.SendMessageRequest{SessionID: "cold", Message: "held", Model: "p/m", Agent: "plan"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { m.Run(ctx); close(done) }()
			select {
			case <-p.entered:
			case <-time.After(time.Second):
				t.Fatal("sender preparation did not reach the owner read")
			}
			cancel()
			defer func() { close(p.release); <-done }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("real sender preparation blocked queue shutdown")
			}
			select {
			case <-p.cancelled:
			default:
				t.Error("real sender owner read was not cancelled")
			}
			select {
			case <-sent:
				t.Error("cancelled preparation sent a prompt")
			default:
			}
			if rows, err := m.Service().List(t.Context(), "r-cold:opencode", "cold"); err != nil || len(rows) != 1 || rows[0].Text != "held" || rows[0].Model != "p/m" || rows[0].Agent != "plan" || rows[0].Attempts != 0 {
				t.Fatalf("held payload changed: %+v %v", rows, err)
			}
		})
	}
}
