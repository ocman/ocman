package prworkflow

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/ocman/ocman/internal/forge"
)

type deadlineForge struct{ forge.Forge }

func (f deadlineForge) LookupPR(ctx context.Context, _ string, _ int) (forge.PR, error) {
	<-ctx.Done()
	return forge.PR{}, ctx.Err()
}

func TestWatchDeadlineDoesNotBlockPendingDelivery(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.ready = false
	x.f.checks.State = forge.CIStateSuccess
	x.m.sweep(t.Context())
	if x.only(t).Pending == "" {
		t.Fatal("missing pending update")
	}
	x.ready = true
	x.m.d.Ready = func(ctx context.Context, _ Target) bool {
		_, err := x.m.d.DB.ArchivedSessions(ctx)
		return err == nil
	}
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) {
		return deadlineForge{x.f}, true
	}
	synctest.Test(t, func(t *testing.T) {
		x.m.sweep(t.Context())
		if l := x.only(t); l.Failures != 1 || l.NextAt <= x.m.now().UnixMilli() || l.LastError == "" {
			t.Fatalf("forge timeout prevented durable backoff: %+v", l)
		}
		if len(x.sent) != 1 || x.only(t).Pending != "" {
			t.Fatal("forge timeout prevented pending delivery")
		}
	})
}
