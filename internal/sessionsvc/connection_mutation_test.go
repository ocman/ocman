package sessionsvc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/testutil"
)

type blockedMutationPlatform struct {
	platforms.Platform
	started, release chan struct{}
	calls            atomic.Int32
	completed        atomic.Bool
}

func (*blockedMutationPlatform) CanonicalSessionIDs() bool { return true }
func (p *blockedMutationPlatform) mutation() error {
	if p.calls.Add(1) == 1 {
		close(p.started)
		<-p.release
	}
	p.completed.Store(true)
	return nil
}

func (*blockedMutationPlatform) Abort(context.Context, platforms.AbortRequest) error { return nil }
func (*blockedMutationPlatform) RespondPermission(context.Context, platforms.RespondPermissionRequest) error {
	return nil
}
func (p *blockedMutationPlatform) RenameSession(context.Context, platforms.RenameSessionRequest) error {
	return p.mutation()
}
func (p *blockedMutationPlatform) MoveSession(context.Context, platforms.MoveSessionRequest) error {
	return p.mutation()
}
func (p *blockedMutationPlatform) DisposeSession(context.Context, platforms.DisposeSessionRequest) error {
	return p.mutation()
}
func (p *blockedMutationPlatform) Revert(context.Context, platforms.RevertSessionRequest) error {
	return p.mutation()
}
func (p *blockedMutationPlatform) Unrevert(context.Context, platforms.UnrevertSessionRequest) error {
	return p.mutation()
}
func (p *blockedMutationPlatform) SetPermissionRules(context.Context, platforms.SetPermissionRulesRequest) error {
	return p.mutation()
}

func TestConnectionDetachDrainsNonTurnMutations(t *testing.T) {
	for name, mutate := range map[string]func(*Service, context.Context, string) error{
		"rename": func(s *Service, ctx context.Context, id string) error {
			return s.Rename(ctx, "", platforms.RenameSessionRequest{SessionID: id, Title: "title"})
		},
		"move": func(s *Service, ctx context.Context, id string) error {
			return s.Move(ctx, "", platforms.MoveSessionRequest{SessionID: id, Directory: "/new"})
		},
		"dispose": func(s *Service, ctx context.Context, id string) error {
			return s.Dispose(ctx, "", platforms.DisposeSessionRequest{SessionID: id})
		},
		"revert": func(s *Service, ctx context.Context, id string) error {
			return s.Revert(ctx, "", platforms.RevertSessionRequest{SessionID: id, MessageID: "m"})
		},
		"unrevert": func(s *Service, ctx context.Context, id string) error {
			return s.Unrevert(ctx, "", platforms.UnrevertSessionRequest{SessionID: id})
		},
		"rules": func(s *Service, ctx context.Context, id string) error {
			return s.SetPermissionRules(ctx, "", platforms.SetPermissionRulesRequest{SessionID: id})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newConnectionRig(t)
			created, err := r.svc.Create(t.Context(), r.connections[0].ID, identityRequest())
			if err != nil {
				t.Fatal(err)
			}
			route, err := r.router.ResolveSession(t.Context(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := &blockedMutationPlatform{Platform: route.Platform, started: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(p.release) }) }
			t.Cleanup(release)
			router := platformconnections.NewRouter(r.store)
			if err := router.Bind(t.Context(), r.connections[0].ID, p); err != nil {
				t.Fatal(err)
			}
			r.svc.connections.router = router
			finished := make(chan error, 1)
			go func() { finished <- mutate(r.svc, t.Context(), created.ID) }()
			<-p.started
			var early atomic.Bool
			detached := make(chan error, 1)
			go func() {
				detached <- router.Detach(t.Context(), r.connections[0].ID, func() error {
					early.Store(!p.completed.Load())
					return r.store.SetEnabled(t.Context(), r.connections[0].ID, false)
				})
			}()
			testutil.WaitFor(t, time.Second, "connection fenced", func() bool {
				done, err := router.Begin(t.Context(), r.connections[0].ID)
				if err == nil {
					done()
					return false
				}
				return errors.Is(err, platformconnections.ErrUnavailable)
			})
			if err := mutate(r.svc, t.Context(), created.ID); !errors.Is(err, platformconnections.ErrUnavailable) {
				t.Errorf("new mutation bypassed drain fence: %v", err)
			}
			if err := r.svc.Abort(t.Context(), "", platforms.AbortRequest{SessionID: created.ID}); err != nil {
				t.Errorf("abort cannot settle draining work: %v", err)
			}
			if err := r.svc.RespondPermission(t.Context(), "", platforms.RespondPermissionRequest{SessionID: created.ID, PermissionID: "permission", Reply: "once"}); err != nil {
				t.Errorf("permission answer cannot settle draining work: %v", err)
			}
			release()
			if err := <-finished; err != nil {
				t.Error(err)
			}
			if err := <-detached; err != nil {
				t.Error(err)
			}
			if early.Load() {
				t.Error("connection intent persisted before native mutation drained")
			}
		})
	}
}

func TestConnectionMutationLeaseIncludesHooks(t *testing.T) {
	for _, mutation := range []string{"move", "rules"} {
		t.Run(mutation, func(t *testing.T) {
			r := newConnectionRig(t)
			created, err := r.svc.Create(t.Context(), r.connections[0].ID, identityRequest())
			if err != nil {
				t.Fatal(err)
			}
			hooks := 0
			checkLease := func() {
				hooks++
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				defer cancel()
				persisted := false
				err := r.router.Detach(ctx, r.connections[0].ID, func() error { persisted = true; return nil })
				if !errors.Is(err, context.DeadlineExceeded) || persisted {
					t.Errorf("hook ran outside connection lifetime: persisted=%v err=%v", persisted, err)
				}
			}
			r.svc.hooks.BeginActivity = func(ctx context.Context, _ platforms.Platform, _ string) (context.Context, func(), error) {
				return ctx, checkLease, nil
			}
			if mutation == "move" {
				r.svc.hooks.SessionCreated = func(CreatedSession) { checkLease() }
				err = r.svc.Move(t.Context(), "", platforms.MoveSessionRequest{SessionID: created.ID, Directory: "/new"})
			} else {
				r.svc.hooks.PermissionRulesChanged = func(context.Context, platforms.Platform, string) { checkLease() }
				err = r.svc.SetPermissionRules(t.Context(), "", platforms.SetPermissionRulesRequest{SessionID: created.ID})
			}
			if err != nil {
				t.Fatal(err)
			}
			if hooks != 2 {
				t.Fatalf("ran %d hooks, want mutation and activity hooks", hooks)
			}
			if err := r.router.Detach(t.Context(), r.connections[0].ID, func() error { return nil }); err != nil {
				t.Fatal("lease leaked after hooks", err)
			}
		})
	}
}
