package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

func TestWarmFailedStopPreservesRoutingOnInconclusiveRecovery(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, syscall.ECONNRESET, io.EOF, io.ErrUnexpectedEOF, ocruntime.ErrProbeNotReady} {
		t.Run(cause.Error(), func(t *testing.T) {
			h, _, _, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", PID: 123, Kind: ocruntime.KindNativeTmux, RepoRoot: root}
			rt := &stoppingRuntime{original: original}
			rt.stopErr = errors.New("stop not confirmed")
			h.runtime = rt
			var pending *ocruntime.Instance
			h.deps.BeforeStop = func(_ context.Context, _ string, inst *ocruntime.Instance) error {
				saved := *inst
				pending = &saved
				return nil
			}
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return pending, nil }
			h.authorizeInstance(root, &original)
			rec.set("7777")
			req := hostsvc.EnsureProjectOpencodeRequest{}
			if _, err := h.RestartProjectOpencode(t.Context(), req); !errors.Is(err, rt.stopErr) || pending == nil || rec.last() != "7777" {
				t.Fatalf("failed Stop lost warm handle: err=%v port=%s", err, rec.last())
			}
			rt.oldProbe = fmt.Errorf("%w: %w", ocruntime.ErrProbeUnreachable, cause)
			h.deps.BeforeReplace = func(context.Context, string, string) error { t.Error("refreshed inconclusive stop"); return nil }
			h.deps.AfterStop = func(context.Context, string) error { t.Error("confirmed inconclusive stop"); return nil }
			for _, fn := range []func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error){h.EnsureProjectOpencode, h.RestartProjectOpencode} {
				if _, err := fn(t.Context(), req); !errors.Is(err, cause) || h.authorizedEndpoint(root) != original.Endpoint || rec.last() != "7777" || h.currentInstance(root) != &original || rt.oldStops != 1 || rt.launchCount() != 0 {
					t.Fatalf("warm inconclusive recovery lost routing: err=%v port=%s authorized=%s stops=%d launches=%d", err, rec.last(), h.authorizedEndpoint(root), rt.oldStops, rt.launchCount())
				}
			}
		})
	}
}

func TestInconclusiveRecoveryCannotAuthorizeDifferentOrColdHandle(t *testing.T) {
	for _, scenario := range []string{"cold", "unauthorized candidate", "rejected candidate", "different endpoint", "different ID", "different PID", "different kind", "different scope"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, _, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", PID: 123, Kind: ocruntime.KindNativeTmux, RepoRoot: root}
			warm := original
			switch scenario {
			case "different endpoint":
				warm.Endpoint = "http://127.0.0.1:8888"
			case "different ID":
				warm.ID = "other"
			case "different PID":
				warm.PID++
			case "different kind":
				warm.Kind = "other"
			case "different scope":
				warm.RepoRoot = ""
			}
			if scenario == "unauthorized candidate" {
				h.setInstance(root, &warm)
			} else if scenario != "cold" {
				h.authorizeInstance(root, &warm)
				if scenario == "rejected candidate" {
					h.revokeInstanceAuthorization(root)
				}
			}
			rec.set("7777")
			rt := &stoppingRuntime{original: original, oldProbe: context.DeadlineExceeded}
			h.runtime = rt
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return &original, nil }
			for _, fn := range []func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error){h.EnsureProjectOpencode, h.RestartProjectOpencode} {
				if _, err := fn(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, context.DeadlineExceeded) || h.authorizedEndpoint(root) != "" || rec.last() != "" || rt.oldStops != 0 || rt.launchCount() != 0 {
					t.Fatalf("unvalidated handle routed: err=%v port=%s authorized=%s", err, rec.last(), h.authorizedEndpoint(root))
				}
			}
		})
	}
}

func TestWarmRecoveryRevokesRoutingOnClosureOrIdentityRejection(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "closure", true: "identity rejection"}[rejected], func(t *testing.T) {
			h, _, _, rec, root := v2Host(t)
			original := ocruntime.Instance{Endpoint: "http://127.0.0.1:7777", ID: "original", RepoRoot: root}
			rt := &stoppingRuntime{original: original, oldProbe: fmt.Errorf("%w: %w", ocruntime.ErrProbeUnreachable, syscall.ECONNREFUSED)}
			h.runtime = rt
			h.authorizeInstance(root, &original)
			rec.set("7777")
			h.deps.ReplacementStopping = func(context.Context, string) (*ocruntime.Instance, error) { return &original, nil }
			wantErr := errors.New("confirmation failed")
			h.deps.AfterStop = func(context.Context, string) error { return wantErr }
			if rejected {
				rt.oldProbe = ocruntime.ErrProbeIdentityMismatch
				rt.stopErr = errors.New("cleanup failed")
				wantErr = rt.stopErr
			}
			if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, wantErr) || h.authorizedEndpoint(root) != "" || rec.last() != "" || rt.oldStops != 1 || rt.launchCount() != 0 {
				t.Fatalf("closed/rejected endpoint still routed: err=%v port=%s", err, rec.last())
			}
			rt.oldProbe = context.DeadlineExceeded
			if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err == nil || h.authorizedEndpoint(root) != "" || rec.last() != "" || rt.oldStops != 1 {
				t.Fatalf("closed/rejected endpoint republished: err=%v port=%s", err, rec.last())
			}
		})
	}
}
