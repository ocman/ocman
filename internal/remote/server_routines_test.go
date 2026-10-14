package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func TestSessionRoutineEnrichmentPropagatesOwnerFailures(t *testing.T) {
	for _, failure := range []string{"publication", "store"} {
		t.Run(failure, func(t *testing.T) {
			registry := platforms.NewRegistry()
			registry.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s", Platform: "opencode"}}})
			store := newStateDB(t)
			owner := NewServer(registry, nil, "owner", "test").UseInboxStore(store)
			ctx := t.Context()
			if failure == "publication" {
				release := owner.sessions.BeginRoutineCreation()
				defer release()
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Sessions(ctx, &pb.SessionsReq{Platform: "opencode"}); err == nil {
				t.Fatal("owner routine state failure returned an untagged session")
			} else if failure == "publication" && !errors.Is(err, context.Canceled) {
				t.Fatalf("publication wait error = %v", err)
			}
		})
	}
}
