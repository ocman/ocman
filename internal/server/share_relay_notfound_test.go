package server

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestPublishCompletedTurnSkipsFetchWithoutShareLinks pins the early
// return: the idle-edge publish must not fetch a full transcript before
// knowing the session has no share links. Short-lived helper sessions are
// deleted by the time the edge lands, and the wasted fetch surfaced as a
// "platforms: not found" warning per session on the dev instance.
func TestPublishCompletedTurnSkipsFetchWithoutShareLinks(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fetched := make(chan string, 1)
	fake := &fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "ses-gone", Platform: "fake"}},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			fetched <- id
			return nil, errors.New("platforms: session not found")
		},
	}
	reg.Register(fake)

	if err := srv.publishCompletedTurn(context.Background(), fake, "ses-gone"); err != nil {
		t.Fatalf("publishCompletedTurn: %v", err)
	}
	select {
	case id := <-fetched:
		t.Fatalf("fetched the transcript of %q although it has no share links", id)
	default:
	}
}
