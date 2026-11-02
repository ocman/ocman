package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
)

func TestRemotePRWatchCountsSurviveHubOverlay(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	rows := []db.Session{{ID: "s1", Platform: "r-box:fake", BackgroundTaskCount: 3, ApplicationBackgroundTaskCount: 1}}
	for range 2 {
		if err := s.overlayPRWatchTasks(t.Context(), rows); err != nil {
			t.Fatal(err)
		}
		if rows[0].BackgroundTaskCount != 3 || rows[0].ApplicationBackgroundTaskCount != 1 {
			t.Fatalf("hub lost owner watch state: %+v", rows[0])
		}
	}
}

type ownerWatchPlatform struct {
	*fakePlatform
	counts map[string]int
	err    error
	reads  int
}

func (p *ownerWatchPlatform) PullRequestOperation(_ context.Context, req platforms.PullRequestRequest) (platforms.PullRequestResponse, error) {
	p.reads++
	if req.Operation != "counts" {
		return platforms.PullRequestResponse{}, platforms.ErrUnsupported
	}
	body, err := json.Marshal(p.counts)
	if p.err != nil {
		err = p.err
	}
	return platforms.PullRequestResponse{Status: 200, Body: body}, err
}

func TestHubReadsOwnerWatchCountsOncePerPlatformAndClearsSettledWatches(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := &ownerWatchPlatform{fakePlatform: &fakePlatform{id: "r-box:fake"}, counts: map[string]int{"s1": 1, "s2": 2}}
	reg.Register(p)
	rows := []db.Session{{ID: "s1", Platform: string(p.ID()), BackgroundTaskCount: 2}, {ID: "s2", Platform: string(p.ID())}}
	for range 2 {
		if err := s.overlayPRWatchTasks(t.Context(), rows); err != nil {
			t.Fatal(err)
		}
		if rows[0].BackgroundTaskCount != 3 || rows[1].BackgroundTaskCount != 2 {
			t.Fatalf("counts=%+v", rows)
		}
	}
	if p.reads != 2 {
		t.Fatalf("reads=%d; should batch by platform", p.reads)
	}
	p.err = platforms.ErrRemoteOffline
	if err := s.overlayPRWatchTasks(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].BackgroundTaskCount != 3 {
		t.Fatal("failed read cleared owner state")
	}
	p.err, p.counts = nil, map[string]int{}
	if err := s.overlayPRWatchTasks(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].BackgroundTaskCount != 2 || rows[1].BackgroundTaskCount != 0 {
		t.Fatalf("settled counts=%+v", rows)
	}
}

func TestPRWorkflowOwnerResolutionIsStrict(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	if owner, err := s.remotePRWorkflow(prworkflow.Target{Platform: "fake"}); err != nil || owner != nil {
		t.Fatalf("local=%v %v", owner, err)
	}
	if _, err := s.remotePRWorkflow(prworkflow.Target{Platform: "r-box:fake"}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	reg.Register(&fakePlatform{id: "r-box:fake"})
	if _, err := s.remotePRWorkflow(prworkflow.Target{Platform: "r-box:fake"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}
