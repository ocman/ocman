package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type ownedPushPlatform struct{ *pushTestPlatform }

func (p *ownedPushPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "source"}, nil
}

func TestPushNotificationLinksUseOwnedIDsWithoutChangingNativeDedupKeys(t *testing.T) {
	s, base, ownedID := identityHTTPFixture(t)
	base.sessions = []db.Session{{ID: "native", Platform: "test", Status: db.StatusDone, TimeUpdated: time.Now().UnixMilli()}}
	s.registry.Register(&ownedPushPlatform{pushTestPlatform: &pushTestPlatform{fakePlatform: base,
		life: &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "message", LatestMessageRole: "assistant"},
	}})
	rows, err := s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("push snapshot: %+v %v", rows, err)
	}
	if !strings.Contains(rows[0].Notification.URL, "/session/"+ownedID) {
		t.Fatalf("native notification link: %+v", rows[0])
	}
	if !strings.Contains(rows[0].Key, "test:native") {
		t.Fatalf("changed durable notification deduplication key: %+v", rows[0])
	}
}
