package local

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

type failedUpsertStore struct{ *fakeStore }

func TestReplacementConfirmationUsesRemainingLaunchBudget(t *testing.T) {
	h, rt, _, _, _ := v2Host(t)
	req := hostsvc.EnsureProjectOpencodeRequest{}
	if _, err := h.EnsureProjectOpencode(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	h.deps.AfterStop = func(ctx context.Context, _ string) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < sfLaunchTimeout/2 {
			return errors.New("confirmation shortened the bounded launch budget")
		}
		return nil
	}
	if _, err := h.RestartProjectOpencode(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if rt.launchCount() != 2 || rt.stopCount() != 1 {
		t.Fatalf("restart did not complete: launches=%d stops=%d", rt.launchCount(), rt.stopCount())
	}
}

func (s failedUpsertStore) Upsert(context.Context, string, ManagedInstance) error {
	return errors.New("upsert unavailable")
}

func TestStoppedRecoveryBlocksReplacementUntilConfirmed(t *testing.T) {
	for _, source := range []string{"no store", "adopted", "failed upsert", "durable"} {
		for _, restart := range []bool{false, true} {
			t.Run(source+"/"+map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
				h, rt, store, rec, root := v2Host(t)
				switch source {
				case "no store":
					h.store = nil
				case "failed upsert":
					h.store = failedUpsertStore{store}
				}
				if source == "adopted" {
					h.deps.DiscoverPort = func(string) string { return "7777" }
				}
				req := hostsvc.EnsureProjectOpencodeRequest{}
				initial, err := h.EnsureProjectOpencode(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				launches := rt.launchCount()
				if source != "durable" && store.has(root) {
					t.Fatal("test unexpectedly has a durable recovery row")
				}
				confirmErr := errors.New("confirmation unavailable")
				confirmations := 0
				h.deps.AfterStop = func(_ context.Context, got string) error {
					confirmations++
					if got != root || h.currentInstance(root) != nil || h.authorizedEndpoint(root) != "" || rec.last() != "" {
						t.Error("stopped recovery remains routable")
						return errors.New("stopped recovery remains routable")
					}
					return confirmErr
				}
				if _, err := h.RestartProjectOpencode(t.Context(), req); !errors.Is(err, confirmErr) {
					t.Fatalf("first confirmation = %v", err)
				}
				// A stopped endpoint could already have been recycled. Never probe it.
				oldProbes := 0
				rt.probe = func(inst *ocruntime.Instance) bool {
					if inst.Endpoint == initial.Endpoint {
						oldProbes++
					}
					return true
				}
				replace := h.EnsureProjectOpencode
				if restart {
					replace = h.RestartProjectOpencode
				}
				for attempt := 0; attempt < 2; attempt++ {
					if _, err := replace(t.Context(), req); !errors.Is(err, confirmErr) {
						t.Errorf("retry %d bypassed failed confirmation: %v", attempt, err)
					}
				}
				if confirmations != 3 || oldProbes != 0 || rt.stopCount() != 1 || rt.launchCount() != launches || rec.last() != "" {
					t.Fatalf("recovery bypassed: confirmations=%d old probes=%d stops=%d launches=%d port=%s", confirmations, oldProbes, rt.stopCount(), rt.launchCount(), rec.last())
				}
				h.deps.AfterStop = func(context.Context, string) error { confirmations++; return nil }
				rt.launchEndpoint = func() string { return "http://127.0.0.1:8888" }
				res, err := replace(t.Context(), req)
				if err != nil || res == nil || !res.Launched || rec.last() != "8888" || confirmations != 4 || oldProbes != 0 || rt.stopCount() != 1 || rt.launchCount() != launches+1 {
					t.Fatalf("recovery failed: result=%+v err=%v confirmations=%d old probes=%d stops=%d launches=%d port=%s", res, err, confirmations, oldProbes, rt.stopCount(), rt.launchCount(), rec.last())
				}
				if _, err := h.EnsureProjectOpencode(t.Context(), req); err != nil || confirmations != 4 || rt.launchCount() != launches+1 {
					t.Fatalf("successful reconciliation was not cleared: %v confirmations=%d", err, confirmations)
				}
			})
		}
	}
}

func TestBeforeStopFailurePreservesInstance(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool { return inst.Endpoint != "old" }}
			boundaryErr := errors.New("stop boundary unavailable")
			prepared, boundaries, confirmed := 0, 0, 0
			h := New(Deps{Runtime: rt,
				BeforeReplace: func(context.Context, string, string) error { prepared++; return nil },
				BeforeStop: func(_ context.Context, root string, inst *ocruntime.Instance) error {
					boundaries++
					if root != "repo" || prepared != boundaries || rt.stopCount() != 0 || inst.ID != "owned" {
						t.Error("stop boundary out of order")
						return errors.New("stop boundary out of order")
					}
					return boundaryErr
				},
				AfterStop: func(context.Context, string) error { confirmed++; return nil },
			})
			inst := &ocruntime.Instance{ID: "owned", Endpoint: "old"}
			h.authorizeInstance("repo", inst)
			replace := h.ensureLocked
			if restart {
				replace = h.restartLocked
			}
			for attempt := 0; attempt < 2; attempt++ {
				_, err := replace(t.Context(), "repo")
				if !errors.Is(err, boundaryErr) || rt.stopCount() != 0 || rt.launchCount() != 0 || confirmed != 0 || h.currentInstance("repo") != inst || h.authorizedEndpoint("repo") != "old" {
					t.Fatalf("failed boundary stopped or lost instance: err=%v stops=%d launches=%d confirmed=%d", err, rt.stopCount(), rt.launchCount(), confirmed)
				}
			}
			if boundaries != 2 {
				t.Fatalf("boundary calls = %d", boundaries)
			}
		})
	}
}

func TestStopBoundaryOrdering(t *testing.T) {
	for _, stopFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "stop failure"}[stopFails], func(t *testing.T) {
			rt := &fakeRuntime{}
			if stopFails {
				rt.stopErr = errors.New("stop failed")
			}
			inst := &ocruntime.Instance{Endpoint: "old", ID: "owned"}
			prepared, boundary, confirmed := false, false, false
			h := New(Deps{Runtime: rt})
			h.authorizeInstance("repo", inst)
			h.deps.BeforeReplace = func(context.Context, string, string) error { prepared = true; return nil }
			h.deps.BeforeStop = func(ctx context.Context, root string, got *ocruntime.Instance) error {
				if ctx.Err() != nil || root != "repo" || !prepared || rt.stopCount() != 0 || h.currentInstance(root) != inst || got != inst {
					t.Error("boundary must run after preparation and before Stop")
					return errors.New("stop boundary out of order")
				}
				boundary = true
				return nil
			}
			h.deps.AfterStop = func(context.Context, string) error {
				if !boundary || rt.stopCount() != 1 || h.currentInstance("repo") != nil || h.authorizedEndpoint("repo") != "" {
					t.Error("confirmation must follow successful Stop and routing withdrawal")
					return errors.New("confirmation out of order")
				}
				confirmed = true
				return nil
			}
			_, err := h.restartLocked(t.Context(), "repo")
			if !boundary || rt.stopCount() != 1 {
				t.Fatal("stop boundary skipped")
			}
			if stopFails {
				if !errors.Is(err, rt.stopErr) || confirmed || h.currentInstance("repo") != inst || rt.launchCount() != 0 {
					t.Fatalf("failed Stop was reconciled: %v", err)
				}
			} else if err != nil || !confirmed || rt.launchCount() != 1 {
				t.Fatalf("successful stop was not reconciled: %v", err)
			}
		})
	}
}

type failedDeleteStore struct {
	*fakeStore
	err error
}

func (s *failedDeleteStore) Delete(ctx context.Context, root string) error {
	if s.err != nil {
		return s.err
	}
	return s.fakeStore.Delete(ctx, root)
}

func TestStoppedRecoveryRetainedOnDeleteFailure(t *testing.T) {
	store := &failedDeleteStore{fakeStore: newFakeStore(), err: errors.New("delete unavailable")}
	rt := &fakeRuntime{}
	h := New(Deps{Runtime: rt, ManagedStore: store})
	if _, err := h.ensureLocked(t.Context(), "repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.restartLocked(t.Context(), "repo"); !errors.Is(err, store.err) {
		t.Fatalf("delete error = %v", err)
	}
	rt.probe = func(*ocruntime.Instance) bool { t.Error("probed stopped instance"); return false }
	if _, err := h.ensureLocked(t.Context(), "repo"); !errors.Is(err, store.err) || rt.stopCount() != 1 || rt.launchCount() != 1 || h.authorizedEndpoint("repo") != "" {
		t.Fatalf("failed deletion bypassed recovery: %v", err)
	}
	store.err = nil
	rt.probe = nil
	if res, err := h.ensureLocked(t.Context(), "repo"); err != nil || res == nil || !res.Launched || rt.stopCount() != 1 || rt.launchCount() != 2 {
		t.Fatalf("delete recovery failed: result=%+v err=%v", res, err)
	}
}

func TestFailedProbeReplacementRetriesOnlyConfirmation(t *testing.T) {
	rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool { return inst.Endpoint != "old" }}
	confirmErr := errors.New("confirmation failed")
	h := New(Deps{Runtime: rt, AfterStop: func(context.Context, string) error { return confirmErr }})
	h.authorizeInstance("repo", &ocruntime.Instance{Endpoint: "old", ID: "owned"})
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := h.ensureLocked(t.Context(), "repo"); !errors.Is(err, confirmErr) || rt.stopCount() != 1 || rt.launchCount() != 0 || h.authorizedEndpoint("repo") != "" {
			t.Fatalf("failed probe bypassed recovery: %v stops=%d launches=%d", err, rt.stopCount(), rt.launchCount())
		}
		rt.probe = func(*ocruntime.Instance) bool { t.Error("probed stopped instance"); return false }
	}
	h.deps.AfterStop = nil
	rt.probe = nil
	if _, err := h.ensureLocked(t.Context(), "repo"); err != nil || rt.stopCount() != 1 || rt.launchCount() != 1 {
		t.Fatalf("recovery failed: %v", err)
	}
}

func TestExplicitStopRetriesPendingConfirmation(t *testing.T) {
	h, rt, _, _, root := v2Host(t)
	req := hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}
	if _, err := h.EnsureProjectOpencode(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	confirmErr := errors.New("confirmation failed")
	h.deps.AfterStop = func(context.Context, string) error { return confirmErr }
	if _, err := h.RestartProjectOpencode(t.Context(), req); !errors.Is(err, confirmErr) {
		t.Fatal(err)
	}
	if err := h.StopProjectOpencode(t.Context(), req); !errors.Is(err, confirmErr) || rt.stopCount() != 1 {
		t.Fatalf("explicit stop repeated Stop or lost recovery: %v", err)
	}
	h.deps.AfterStop = nil
	if err := h.StopProjectOpencode(t.Context(), req); err != nil || rt.stopCount() != 1 || rt.launchCount() != 1 {
		t.Fatalf("explicit stop did not reconcile: %v", err)
	}
}

func TestConfirmedFailedProbeSkipsStoppedDiscovery(t *testing.T) {
	rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool { return inst.Endpoint != "old" }}
	h := New(Deps{Runtime: rt, DiscoverPort: func(string) string {
		t.Error("discovery can return the already stopped endpoint")
		return "7777"
	}})
	h.authorizeInstance("repo", &ocruntime.Instance{Endpoint: "old", ID: "owned"})
	if res, err := h.ensureLocked(t.Context(), "repo"); err != nil || res == nil || !res.Launched || rt.stopCount() != 1 || rt.launchCount() != 1 {
		t.Fatalf("replacement failed: result=%+v err=%v", res, err)
	}
}

func TestNewHostRecoversDurableStoppedWithoutManagedRow(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, candidate := range []bool{false, true} {
			t.Run(map[bool]string{false: "ensure", true: "restart"}[restart]+"/"+map[bool]string{false: "no candidate", true: "authorized candidate"}[candidate], func(t *testing.T) {
				h, rt, store, rec, root := v2Host(t)
				if store.has(root) {
					t.Fatal("fresh host unexpectedly has managed row")
				}
				if candidate {
					h.authorizeInstance(root, &ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "old"})
					rec.set("7777")
				}
				stopped := true
				reads, confirmations := 0, 0
				h.deps.ReplacementStopped = func(_ context.Context, got string) (bool, error) {
					reads++
					if got != root {
						t.Errorf("durable lookup root = %q, want %q", got, root)
					}
					return stopped, nil
				}
				confirmErr := errors.New("confirmation unavailable")
				h.deps.AfterStop = func(context.Context, string) error { confirmations++; return confirmErr }
				h.deps.DiscoverPort = func(string) string { t.Error("discovered stopped instance"); return "7777" }
				rt.probe = func(*ocruntime.Instance) bool { t.Error("probed stopped instance"); return true }
				replace := h.EnsureProjectOpencode
				if restart {
					replace = h.RestartProjectOpencode
				}
				req := hostsvc.EnsureProjectOpencodeRequest{}
				for attempt := 0; attempt < 3; attempt++ {
					if _, err := replace(t.Context(), req); !errors.Is(err, confirmErr) {
						t.Errorf("attempt %d bypassed durable stopped phase: %v", attempt, err)
					}
				}
				if reads != 1 || confirmations != 3 || rt.stopCount() != 0 || rt.launchCount() != 0 || h.currentInstance(root) != nil || h.authorizedEndpoint(root) != "" || rec.last() != "" {
					t.Fatalf("durable recovery lost: reads=%d confirmations=%d stops=%d launches=%d port=%s", reads, confirmations, rt.stopCount(), rt.launchCount(), rec.last())
				}
				h.deps.AfterStop = func(context.Context, string) error { confirmations++; stopped = false; return nil }
				rt.probe = nil
				rt.launchEndpoint = func() string { return "http://127.0.0.1:8888" }
				if res, err := replace(t.Context(), req); err != nil || res == nil || !res.Launched || confirmations != 4 || rt.stopCount() != 0 || rt.launchCount() != 1 || rec.last() != "8888" {
					t.Fatalf("durable recovery failed: result=%+v err=%v confirmations=%d port=%s", res, err, confirmations, rec.last())
				}
				if _, err := h.EnsureProjectOpencode(t.Context(), req); err != nil || reads != 2 || confirmations != 4 || rt.launchCount() != 1 {
					t.Fatalf("recovery not cleared: err=%v reads=%d confirmations=%d", err, reads, confirmations)
				}
			})
		}
	}
}

func TestDurableStoppedLookupErrorFailsClosed(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			h, rt, _, rec, root := v2Host(t)
			lookupErr := errors.New("stopped phase unavailable")
			h.deps.ReplacementStopped = func(_ context.Context, got string) (bool, error) {
				if got != root {
					t.Errorf("lookup root = %q, want %q", got, root)
				}
				return false, lookupErr
			}
			h.deps.AfterStop = func(context.Context, string) error { t.Error("confirmed after failed lookup"); return nil }
			h.deps.DiscoverPort = func(string) string { t.Error("discovered after failed lookup"); return "7777" }
			rt.probe = func(*ocruntime.Instance) bool { t.Error("probed after failed lookup"); return true }
			replace := h.EnsureProjectOpencode
			if restart {
				replace = h.RestartProjectOpencode
			}
			if _, err := replace(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, lookupErr) || rt.stopCount() != 0 || rt.launchCount() != 0 || rec.last() != "" {
				t.Fatalf("failed lookup bypassed: err=%v stops=%d launches=%d port=%s", err, rt.stopCount(), rt.launchCount(), rec.last())
			}
		})
	}
}
