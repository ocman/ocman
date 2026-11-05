package sessionsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func TestCreateAdmitsActivityBeforeTakingCreationLock(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{})
	stop := errors.New("stop after activity admission")
	var lockErr error
	svc.hooks.BeginDirectory = func(ctx context.Context, _ platforms.Platform, _ string) (context.Context, func(), error) {
		probe, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		unlock, err := svc.lockCreation(probe, p.ID(), "shared-request")
		lockErr = err
		if err == nil {
			unlock()
		}
		return ctx, nil, stop
	}
	_, err := svc.Create(t.Context(), "opencode", platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "shared-request"}})
	if !errors.Is(err, stop) {
		t.Fatalf("unexpected create result: %v", err)
	}
	if lockErr != nil {
		t.Fatalf("creation lock held before activity admission, opposite to Fork: %v", lockErr)
	}
}
