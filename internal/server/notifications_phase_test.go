package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
)

func TestPushSnapshotIdentifiesFailedPhaseWithoutLosingCause(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{PlatformID: "fake", SessionList: []db.Session{{ID: "session", Status: db.StatusDone, TimeUpdated: time.Now().UnixMilli()}}}, lifeErr: context.DeadlineExceeded}
	s.registry.Register(p)
	_, err := s.pushSnapshot(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "phase=lifecycle") || !strings.Contains(err.Error(), "elapsed=") || !strings.Contains(err.Error(), "fake/session") {
		t.Fatalf("snapshot failure lacks phase, duration, source/session or original cause: %v", err)
	}
}
