package conversationread

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestFirstConversationReadWaitsForInitialImport(t *testing.T) {
	for _, missing := range []string{"instance", "conversation", "receipt"} {
		t.Run(missing, func(t *testing.T) {
			s, p, store, batch := readFixture(t)
			switch missing {
			case "instance":
				p.head.Identity.SourceKey = "/new-source"
				batch.Session.InstanceID = "new-source"
			case "conversation":
				p.head.Session.ID = "new"
				batch.Session.SessionID = "new"
				batch.Messages, batch.Parts = nil, nil
			case "receipt":
				if err := store.SetSourceState(t.Context(), "source", "read-ready/s", ""); err != nil {
					t.Fatal(err)
				}
			}
			waits := 0
			s.Await = func(ctx context.Context, platformID, sessionID, version string) bool {
				waits++
				if missing == "instance" {
					if err := store.RegisterInstance(ctx, statev2.Instance{ID: "new-source", OwnerID: "owner", Platform: "test", SourceKey: "/new-source"}); err != nil {
						t.Fatal(err)
					}
				}
				if missing != "receipt" {
					if _, err := store.Commit(ctx, batch); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.SetSourceState(ctx, batch.Session.InstanceID, "read-ready/"+sessionID, version); err != nil {
					t.Fatal(err)
				}
				return true
			}
			detail, err := p.Session(platforms.WithConversationReader(t.Context(), s), p.head.Session.ID, 30, 0)
			if err != nil || detail == nil || waits != 1 || p.nativeCalls != 0 {
				t.Fatalf("initial read = %+v, %v; waits=%d native=%d", detail, err, waits, p.nativeCalls)
			}
		})
	}
}

func TestInitialImportWaitStillFailsClosed(t *testing.T) {
	for _, outcome := range []string{"timeout", "incomplete", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			s, p, _, _ := readFixture(t)
			p.head.Session.ID = "new"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			waits, notified := 0, 0
			s.Notify = func(string, string) { notified++ }
			s.Await = func(waitCtx context.Context, _, _, _ string) bool {
				waits++
				if _, ok := waitCtx.Deadline(); !ok {
					t.Error("initial import wait has no deadline")
				}
				if outcome == "cancelled" {
					cancel()
				}
				return outcome == "incomplete"
			}
			_, err := p.Session(platforms.WithConversationReader(ctx, s), "new", 30, 0)
			want := platforms.ErrHistoryUnavailable
			if outcome == "cancelled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || waits != 1 || notified != 1 || p.nativeCalls != 0 {
				t.Fatalf("read = %v; waits=%d notified=%d native=%d", err, waits, notified, p.nativeCalls)
			}
		})
	}
}

func TestInitialImportWaitDefersToTurnThatStartedDuringRead(t *testing.T) {
	s, p, _, _ := readFixture(t)
	p.head.Session.ID = "new"
	s.Await = func(context.Context, string, string, string) bool {
		p.live = &platforms.LiveState{Status: "busy"}
		return false
	}
	detail, err := p.Session(platforms.WithConversationReader(t.Context(), s), "new", 30, 0)
	if err != nil || detail == nil || p.nativeCalls != 1 {
		t.Fatalf("live read = %+v, %v; native=%d", detail, err, p.nativeCalls)
	}
}
