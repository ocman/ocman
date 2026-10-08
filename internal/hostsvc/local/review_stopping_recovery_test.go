package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

type stoppingRuntime struct {
	fakeRuntime
	original ocruntime.Instance
	oldProbe error
	oldStops int
}

func TestExplicitRestartRecoversAuthenticationMismatchAfterFailedStop(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(fmt.Sprint("cold=", cold), func(t *testing.T) {
			h, _, store, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", RepoRoot: root, Kind: ocruntime.KindNativeTmux}
			rt := &stoppingRuntime{original: original, oldProbe: ocapi.ErrAuthentication}
			rt.stopErr = errors.New("transient stop failure")
			h.runtime = rt
			h.authorizeInstance(root, &original)
			if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: original.Endpoint, RuntimeID: original.ID, Kind: original.Kind}); err != nil {
				t.Fatal(err)
			}
			var pending *ocruntime.Instance
			prepared, boundaries, confirmed := 0, 0, 0
			h.deps.BeforeReplace = func(context.Context, string, string) error { prepared++; return nil }
			h.deps.BeforeStop = func(_ context.Context, _ string, inst *ocruntime.Instance) error {
				boundaries++
				copy := *inst
				pending = &copy
				return nil
			}
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return pending, nil }
			h.deps.AfterStop = func(context.Context, string) error { confirmed++; pending = nil; return nil }
			req := hostsvc.EnsureProjectOpencodeRequest{}
			if _, err := h.RestartProjectOpencode(t.Context(), req); !errors.Is(err, rt.stopErr) {
				t.Fatalf("first Stop failure lost: %v", err)
			}
			rt.stopErr = nil
			rt.endpoint = "http://127.0.0.1:8888"
			if cold {
				deps := h.deps
				deps.Runtime = rt
				h = New(deps)
			}
			if _, err := h.EnsureProjectOpencode(t.Context(), req); !errors.Is(err, ocapi.ErrAuthentication) || rt.oldStops != 1 || confirmed != 0 {
				t.Fatalf("automatic recovery stopped unauthenticated instance: %v stops=%d confirmed=%d", err, rt.oldStops, confirmed)
			}
			if res, err := h.RestartProjectOpencode(t.Context(), req); err != nil || res == nil || !res.Launched {
				t.Fatalf("explicit recovery blocked: result=%+v err=%v", res, err)
			}
			if prepared != 1 || boundaries != 1 || confirmed != 1 || rt.oldStops != 2 || rt.launchCount() != 1 || rec.last() != "8888" {
				t.Fatalf("recovery resampled evidence or failed: prepared=%d boundaries=%d confirmed=%d stops=%d launches=%d port=%s", prepared, boundaries, confirmed, rt.oldStops, rt.launchCount(), rec.last())
			}
		})
	}
}

func TestAuthenticationStopRecoveryRequiresOwnedHandle(t *testing.T) {
	for _, scenario := range []string{"warm", "different inventory", "missing inventory", "inventory error", "adopted", "missing scope", "stop failure"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, store, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "old", ID: "original", RepoRoot: root, Kind: ocruntime.KindNativeTmux}
			rt := &stoppingRuntime{oldProbe: ocapi.ErrAuthentication}
			h.runtime = rt
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return &original, nil }
			wantErr := ocapi.ErrAuthentication
			wantStops := 0
			switch scenario {
			case "warm", "stop failure":
				h.authorizeInstance(root, &original)
				wantErr, wantStops = nil, 1
				if scenario == "stop failure" {
					rt.stopErr = errors.New("stop still unavailable")
					wantErr = rt.stopErr
				}
			case "different inventory":
				if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "different", RuntimeID: original.ID, Kind: original.Kind}); err != nil {
					t.Fatal(err)
				}
			case "inventory error":
				store.getErr = errors.New("inventory unavailable")
				wantErr = store.getErr
			case "adopted":
				original.ID = ""
			case "missing scope":
				original.RepoRoot = ""
			}
			rt.original = original
			stopped, err := h.recoverStopping(t.Context(), root, true)
			if !errors.Is(err, wantErr) || stopped != (wantErr == nil) || rt.oldStops != wantStops || rt.launchCount() != 0 || h.authorizedEndpoint(root) != "" || rec.last() != "" {
				t.Fatalf("ownership guard failed: stopped=%t err=%v stops=%d launches=%d", stopped, err, rt.oldStops, rt.launchCount())
			}
		})
	}
}

func (r *stoppingRuntime) Probe(ctx context.Context, inst *ocruntime.Instance) error {
	if inst.Endpoint == r.original.Endpoint {
		if *inst != r.original {
			return errors.New("probe changed original handle")
		}
		return r.oldProbe
	}
	return r.fakeRuntime.Probe(ctx, inst)
}

func (r *stoppingRuntime) Stop(ctx context.Context, inst *ocruntime.Instance) error {
	if *inst != r.original {
		return errors.New("stop changed original handle")
	}
	r.oldStops++
	return r.fakeRuntime.Stop(ctx, inst)
}

func TestReopenedHostRecoversUnacknowledgedStop(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			_, _, store, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", Kind: ocruntime.KindNativeTmux, PID: 123}
			var pending *ocruntime.Instance
			rt := &stoppingRuntime{original: original}
			rt.stopErr = errors.New("crash before acknowledgment")
			deps := Deps{Runtime: rt, ManagedStore: store, SetMachineServer: rec.set,
				BeforeStop: func(_ context.Context, _ string, inst *ocruntime.Instance) error {
					saved := *inst
					pending = &saved
					return nil
				},
				ReplacementStopping: func(context.Context, string) (*ocruntime.Instance, error) { return pending, nil },
			}
			h := New(deps)
			h.authorizeInstance(root, &original)
			if _, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, rt.stopErr) || pending == nil {
				t.Fatalf("stop boundary not saved: %v", err)
			}
			// The runtime has closed, but acknowledgment never reached durable state.
			rt = &stoppingRuntime{original: original, oldProbe: fmt.Errorf("%w: %w", ocruntime.ErrProbeUnreachable, syscall.ECONNREFUSED)}
			deps.Runtime = rt
			deps.BeforeReplace = func(context.Context, string, string) error {
				t.Error("refreshed original stop evidence")
				return errors.New("unexpected preparation")
			}
			deps.BeforeStop = func(context.Context, string, *ocruntime.Instance) error {
				t.Error("resampled original stop boundary")
				return errors.New("unexpected boundary")
			}
			confirmErr := errors.New("confirmation unavailable")
			confirmations := 0
			deps.AfterStop = func(context.Context, string) error { confirmations++; return confirmErr }
			deps.DiscoverPort = func(string) string { t.Error("discovered instead of recovering original"); return "9999" }
			h = New(deps)
			replace := h.EnsureProjectOpencode
			if restart {
				replace = h.RestartProjectOpencode
			}
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := replace(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, confirmErr) {
					t.Errorf("recovery bypassed confirmation: %v", err)
				}
			}
			if rt.oldStops != 1 || rt.launchCount() != 0 || confirmations != 2 || h.authorizedEndpoint(root) != "" || rec.last() != "" || store.has(root) {
				t.Fatalf("original recovery lost: stops=%d launches=%d confirmations=%d port=%s", rt.oldStops, rt.launchCount(), confirmations, rec.last())
			}
			h.deps.AfterStop = func(context.Context, string) error { pending = nil; return nil }
			rt.endpoint = "http://127.0.0.1:8888"
			if res, err := replace(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil || res == nil || !res.Launched || rt.oldStops != 1 || rt.launchCount() != 1 || rec.last() != "8888" {
				t.Fatalf("reconciliation failed: result=%+v err=%v", res, err)
			}
		})
	}
}

func TestUnacknowledgedStopStillRunningCancelsBeforeFreshPreparation(t *testing.T) {
	h, _, _, rec, root := v2Host(t)
	original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", RepoRoot: root, Kind: ocruntime.KindNativeTmux}
	rt := &stoppingRuntime{original: original}
	h.runtime = rt
	cancelled, prepared, bounded := false, false, false
	h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) {
		if cancelled {
			return nil, nil
		}
		return &original, nil
	}
	h.deps.CancelReplacementStop = func(_ context.Context, got string) error {
		if got != root {
			return errors.New("wrong cancellation root")
		}
		cancelled = true
		return nil
	}
	h.deps.BeforeReplace = func(context.Context, string, string) error {
		if !cancelled {
			t.Error("refreshed evidence before cancelling")
			return errors.New("not cancelled")
		}
		prepared = true
		return nil
	}
	h.deps.BeforeStop = func(_ context.Context, _ string, inst *ocruntime.Instance) error {
		if !prepared || *inst != original {
			return errors.New("wrong fresh stop boundary")
		}
		bounded = true
		return nil
	}
	h.deps.AfterStop = func(context.Context, string) error {
		if !bounded || rt.oldStops != 1 {
			return errors.New("confirmed original incomplete stop")
		}
		return nil
	}
	rt.endpoint = "http://127.0.0.1:8888"
	if res, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil || res == nil || !res.Launched || !cancelled || !prepared || rt.oldStops != 1 || rec.last() != "8888" {
		t.Fatalf("running recovery failed: result=%+v err=%v cancelled=%v prepared=%v stops=%d", res, err, cancelled, prepared, rt.oldStops)
	}
}

func TestUnacknowledgedStopInconclusiveBlocksReplacement(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, syscall.ECONNRESET, io.EOF, io.ErrUnexpectedEOF, ocruntime.ErrProbeNotReady} {
		t.Run(cause.Error(), func(t *testing.T) {
			h, _, _, _, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "old", ID: "original", RepoRoot: root}
			errProbe := fmt.Errorf("%w: %w", ocruntime.ErrProbeUnreachable, cause)
			rt := &stoppingRuntime{original: original, oldProbe: errProbe}
			h.runtime = rt
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return &original, nil }
			h.deps.CancelReplacementStop = func(context.Context, string) error { t.Error("cancelled inconclusive stop"); return nil }
			h.deps.BeforeReplace = func(context.Context, string, string) error { t.Error("refreshed inconclusive evidence"); return nil }
			h.deps.AfterStop = func(context.Context, string) error { t.Error("confirmed inconclusive stop"); return nil }
			for _, fn := range []func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error){h.EnsureProjectOpencode, h.RestartProjectOpencode} {
				if _, err := fn(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, cause) || rt.oldStops != 0 || rt.launchCount() != 0 {
					t.Fatalf("inconclusive stop replaced: err=%v stops=%d launches=%d", err, rt.oldStops, rt.launchCount())
				}
			}
		})
	}
}

func TestUnacknowledgedStopMissingHandleFailsClosed(t *testing.T) {
	h, rt, _, _, _ := v2Host(t)
	legacyErr := errors.New("unresolved stop has no persisted runtime handle")
	h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return nil, legacyErr }
	for _, fn := range []func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error){h.EnsureProjectOpencode, h.RestartProjectOpencode} {
		if _, err := fn(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, legacyErr) || rt.stopCount() != 0 || rt.launchCount() != 0 {
			t.Fatalf("legacy stop bypassed: %v", err)
		}
	}
}

func TestUnacknowledgedStopRecoveryErrorsBlockLaunch(t *testing.T) {
	for _, scenario := range []string{"empty endpoint", "wrong scope", "missing cancel", "cancel failure", "closure failure", "authentication", "unknown probe"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, _, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "old", ID: "original", RepoRoot: root}
			rt := &stoppingRuntime{original: original}
			h.runtime = rt
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return &original, nil }
			h.deps.BeforeReplace = func(context.Context, string, string) error { t.Error("prepared unresolved stop"); return nil }
			h.deps.AfterStop = func(context.Context, string) error { t.Error("confirmed unresolved stop"); return nil }
			want := ""
			wantStops := 0
			switch scenario {
			case "empty endpoint":
				original.Endpoint = ""
				want = "invalid runtime handle"
			case "wrong scope":
				original.RepoRoot = "unrelated"
				want = "invalid runtime handle"
			case "missing cancel":
				want = "cancellation callback"
			case "cancel failure":
				h.deps.CancelReplacementStop = func(context.Context, string) error { return errors.New("cancel denied") }
				want = "cancel denied"
			case "closure failure":
				rt.oldProbe = ocruntime.ErrProbeUnreachable
				rt.stopErr = errors.New("closure denied")
				want, wantStops = "closure denied", 1
			case "authentication":
				rt.oldProbe = ocapi.ErrAuthentication
				want = rt.oldProbe.Error()
			case "unknown probe":
				rt.oldProbe = errors.New("probe unknown")
				want = rt.oldProbe.Error()
			}
			if _, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err == nil || !strings.Contains(err.Error(), want) || rt.oldStops != wantStops || rt.launchCount() != 0 || rec.last() != "" {
				t.Fatalf("recovery error bypassed: err=%v stops=%d launches=%d port=%s", err, rt.oldStops, rt.launchCount(), rec.last())
			}
		})
	}
}

func TestUnacknowledgedStopRunningEnsureReusesOriginal(t *testing.T) {
	h, _, _, rec, root := v2Host(t)
	original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", RepoRoot: root}
	rt := &stoppingRuntime{original: original}
	h.runtime = rt
	h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return &original, nil }
	cancelled := false
	h.deps.CancelReplacementStop = func(context.Context, string) error { cancelled = true; return nil }
	h.deps.BeforeReplace = func(context.Context, string, string) error {
		t.Error("replaced positively validated runtime")
		return nil
	}
	if res, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil || res == nil || res.Launched || !cancelled || rt.stopCount() != 0 || rt.launchCount() != 0 || rec.last() != "7777" {
		t.Fatalf("running original was not reused: result=%+v err=%v cancelled=%v", res, err, cancelled)
	}
}
