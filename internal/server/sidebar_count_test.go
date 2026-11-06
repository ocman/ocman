package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

type aggregateTestPlatform struct{ *compactTestPlatform }

func (*aggregateTestPlatform) RunningSessionCountFor(context.Context, []platforms.SidebarSource, platforms.SidebarQuery) (int, error) {
	return 17, nil
}

func TestRunningCountDoesNotLoadSidebarRows(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := &aggregateTestPlatform{&compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "test"}}}
	reg.Register(p)
	w := httptest.NewRecorder()
	s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
	if w.Code != 200 || w.Body.String() != "{\"count\":17}\n" || p.calls != 0 {
		t.Fatalf("count should use aggregate only: calls=%d status=%d body=%s", p.calls, w.Code, w.Body.String())
	}
}

type remoteAggregatePlatform struct {
	*fakePlatform
	query platforms.SidebarQuery
	err   error
}

func (p *remoteAggregatePlatform) RunningSessionCount(_ context.Context, q platforms.SidebarQuery) (int, error) {
	p.query = q
	return 4, p.err
}

func TestRunningCountCombinesOwnersAndPreservesPartialFailurePolicy(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	reg.Register(&aggregateTestPlatform{&compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "local"}}})
	remote := &remoteAggregatePlatform{fakePlatform: &fakePlatform{PlatformID: "r-box:test"}}
	reg.Register(remote)
	reg.Register(&fakePlatform{PlatformID: "r-older:test"})
	if err := s.stateDB.PinSession(t.Context(), "r-box:test", "pinned"); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.SnoozeSession(t.Context(), "r-box:test", "snoozed", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	q := platforms.SidebarQuery{Directory: "/repo", Origin: "routine", OriginID: "r"}
	if n, err := s.runningSessionCount(t.Context(), q); err != nil || n != 21 || len(remote.query.IncludeIDs) != 2 || remote.query.Directory != q.Directory || remote.query.OriginID != q.OriginID {
		t.Fatalf("count=%d query=%+v err=%v", n, remote.query, err)
	}
	remote.err = platforms.ErrRemoteOffline
	if n, err := s.runningSessionCount(t.Context(), q); err != nil || n != 17 {
		t.Fatalf("partial count=%d err=%v", n, err)
	}
	reg.Register(&fakePlatform{PlatformID: "unsupported-local"})
	if _, err := s.runningSessionCount(t.Context(), q); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported local adapter: %v", err)
	}
}

type failingAggregatePlatform struct {
	*compactTestPlatform
	sourceError bool
}

func (p *failingAggregatePlatform) SidebarSource(ctx context.Context) (platforms.SidebarSource, error) {
	if p.sourceError {
		return platforms.SidebarSource{}, platforms.ErrHistoryUnavailable
	}
	return p.compactTestPlatform.SidebarSource(ctx)
}

func (*failingAggregatePlatform) RunningSessionCountFor(context.Context, []platforms.SidebarSource, platforms.SidebarQuery) (int, error) {
	return 0, platforms.ErrHistoryUnavailable
}

func TestRunningCountLocalFailuresAreNotPartialCounts(t *testing.T) {
	for _, sourceError := range []bool{true, false} {
		s, reg := newSessionsTestServer(t)
		reg.Register(&failingAggregatePlatform{compactTestPlatform: &compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "local"}}, sourceError: sourceError})
		reg.Register(&remoteAggregatePlatform{fakePlatform: &fakePlatform{PlatformID: "r-box:test"}})
		w := httptest.NewRecorder()
		s.handleSessions(w, httptest.NewRequest(http.MethodGet, "/api/sessions?view=running-count", nil))
		if w.Code != 500 {
			t.Fatalf("local count failure returned partial result: %d %s", w.Code, w.Body.String())
		}
	}
}
