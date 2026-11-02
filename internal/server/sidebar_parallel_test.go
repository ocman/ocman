package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type parallelSidebarLocal struct {
	*compactTestPlatform
	read func(context.Context) ([]db.Session, error)
}

func (p *parallelSidebarLocal) SidebarSessionsFor(ctx context.Context, _ []platforms.SidebarSource, _ platforms.SidebarQuery) ([]db.Session, error) {
	return p.read(ctx)
}

type parallelSidebarRemote struct {
	*remoteCompactTestPlatform
	read func(context.Context) ([]db.Session, error)
}

func (p *parallelSidebarRemote) SidebarSessions(ctx context.Context, _ platforms.SidebarQuery) ([]db.Session, error) {
	return p.read(ctx)
}

func TestSidebarLocalAndRemoteReadsOverlap(t *testing.T) {
	for _, localFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "local failure"}[localFails], func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			localStarted := make(chan struct{})
			remoteStarted := make(chan struct{})
			localErr := errors.New("local read failed")
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			reg.Register(&parallelSidebarLocal{
				compactTestPlatform: &compactTestPlatform{fakePlatform: &fakePlatform{id: "local"}},
				read: func(ctx context.Context) ([]db.Session, error) {
					close(localStarted)
					select {
					case <-remoteStarted:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if localFails {
						return nil, localErr
					}
					return []db.Session{{ID: "local", Platform: "local", TimeUpdated: 1}}, nil
				},
			})
			reg.Register(&parallelSidebarRemote{
				remoteCompactTestPlatform: &remoteCompactTestPlatform{fakePlatform: &fakePlatform{id: "r-box:test"}},
				read: func(ctx context.Context) ([]db.Session, error) {
					close(remoteStarted)
					select {
					case <-localStarted:
						return []db.Session{{ID: "remote", Platform: "r-box:test", TimeUpdated: 2}}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			})
			rows, err := s.sidebarSessionRows(ctx, platforms.SidebarQuery{}, false)
			if ctx.Err() != nil {
				t.Fatal("local and remote queries did not overlap")
			}
			if localFails {
				if !errors.Is(err, localErr) {
					t.Fatalf("error = %v, want local failure", err)
				}
			} else if err != nil || len(rows) != 2 || rows[0].ID != "remote" || rows[1].ID != "local" {
				t.Fatalf("rows=%+v error=%v", rows, err)
			}
		})
	}
}

func TestSidebarCancelsAndJoinsRemoteOnLocalFailure(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	localErr := errors.New("local read failed")
	reg.Register(&parallelSidebarLocal{
		compactTestPlatform: &compactTestPlatform{fakePlatform: &fakePlatform{id: "local"}},
		read: func(ctx context.Context) ([]db.Session, error) {
			select {
			case <-started:
				return nil, localErr
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	reg.Register(&parallelSidebarRemote{
		remoteCompactTestPlatform: &remoteCompactTestPlatform{fakePlatform: &fakePlatform{id: "r-box:test"}},
		read: func(ctx context.Context) ([]db.Session, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.sidebarSessionRows(ctx, platforms.SidebarQuery{}, false); !errors.Is(err, localErr) {
		t.Fatalf("error = %v, want local failure", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("returned before remote worker stopped")
	}
}

func TestSidebarRemoteFailureHandling(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial results", true: "strict failure"}[strict], func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			remoteErr := errors.New("remote unavailable")
			reg.Register(&compactTestPlatform{fakePlatform: &fakePlatform{id: "local"}, rows: []db.Session{{ID: "local", Platform: "local"}}})
			reg.Register(&parallelSidebarRemote{
				remoteCompactTestPlatform: &remoteCompactTestPlatform{fakePlatform: &fakePlatform{id: "r-box:test"}},
				read: func(ctx context.Context) ([]db.Session, error) {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 2*time.Second {
						return nil, errors.New("missing remote timeout")
					}
					return nil, remoteErr
				},
			})
			rows, err := s.sidebarSessionRows(t.Context(), platforms.SidebarQuery{}, strict)
			if strict {
				if !errors.Is(err, remoteErr) {
					t.Fatalf("error = %v, want remote failure", err)
				}
			} else if err != nil || len(rows) != 1 || rows[0].ID != "local" {
				t.Fatalf("rows=%+v error=%v", rows, err)
			}
		})
	}
}
