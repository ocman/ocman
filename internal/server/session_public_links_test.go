package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type ownedPushPlatform struct{ *pushTestPlatform }

func (*ownedPushPlatform) CanonicalSessionIDs() bool { return true }

func (p *ownedPushPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "source"}, nil
}

func TestPushNotificationLinksAndDedupKeysUseCanonicalIDs(t *testing.T) {
	s, base, ownedID := identityHTTPFixture(t)
	base.sessions = []db.Session{{ID: ownedID, Platform: "test", Status: db.StatusDone, TimeUpdated: time.Now().UnixMilli()}}
	s.registry.Register(&ownedPushPlatform{pushTestPlatform: &pushTestPlatform{fakePlatform: base,
		life: &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "message", LatestMessageRole: "assistant"},
	}})
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("push snapshot: %+v %v", rows, err)
	}
	if !strings.Contains(rows[0].Notification.URL, "/session/"+ownedID) {
		t.Fatalf("native notification link: %+v", rows[0])
	}
	if !strings.Contains(rows[0].Key, "test:"+ownedID) {
		t.Fatalf("notification deduplication key is not canonical: %+v", rows[0])
	}
}
