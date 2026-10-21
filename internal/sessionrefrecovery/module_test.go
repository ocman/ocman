package sessionrefrecovery

import (
	"context"
	"errors"
	"testing"
)

func TestRecoveryAggregatesProofsAndRetriesReloadWithoutResolvingAgain(t *testing.T) {
	calls, reloads, failures := 0, 0, 0
	ready := false
	m := New(Deps{
		Scopes: func() []string { return []string{"r-one:test", "r-two:test"} },
		Resolve: func(ctx context.Context, scope string) (int, error) {
			calls++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded owner lookup")
			}
			if scope == "r-one:test" {
				return 1, errors.New("partial batch failure")
			}
			return 2, nil
		},
		RequestReload: func() bool { reloads++; return ready },
		OnError:       func(string, error) { failures++ },
	})
	if m.check(t.Context()) || calls != 2 || reloads != 1 || failures != 1 {
		t.Fatalf("first recovery: calls=%d reloads=%d failures=%d", calls, reloads, failures)
	}
	ready = true
	if !m.check(t.Context()) || calls != 2 || reloads != 2 {
		t.Fatal("reload retry repeated owner lookups")
	}
}

func TestRecoveryRunnerJoinsOnCancellationAndStopsAfterReload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m := New(Deps{Scopes: func() []string { t.Fatal("cancelled runner touched state"); return nil }})
	m.Run(ctx)
	if m.Name() != "session-reference-recovery" {
		t.Fatal("unexpected module identity")
	}
	m = New(Deps{Scopes: func() []string { return []string{"r-owner:test"} }, Resolve: func(context.Context, string) (int, error) { return 1, nil }, RequestReload: func() bool { return true }})
	m.Run(t.Context())
	if !m.needsReload {
		t.Fatal("runner exited without requesting reload")
	}
}
