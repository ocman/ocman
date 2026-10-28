package codextransport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type ownerRuntimeFake struct {
	instances                  []*ocruntime.Instance
	calls                      []string
	drain, seal                bool
	listErr, drainErr, sealErr error
	stopErr                    error
	onStop                     func()
	resumeContextOK            bool
}

func (f *ownerRuntimeFake) Instances(string) ([]*ocruntime.Instance, error) {
	return f.instances, f.listErr
}
func (f *ownerRuntimeFake) DrainIdle(context.Context, *ocruntime.Instance, string) (bool, error) {
	f.calls = append(f.calls, "drain")
	return f.drain, f.drainErr
}
func (f *ownerRuntimeFake) SealIdle(context.Context, *ocruntime.Instance, string) (bool, error) {
	f.calls = append(f.calls, "seal")
	return f.seal, f.sealErr
}
func (f *ownerRuntimeFake) Resume(ctx context.Context, _ *ocruntime.Instance, _ string) error {
	f.calls = append(f.calls, "resume")
	f.resumeContextOK = ctx.Err() == nil
	return nil
}
func (f *ownerRuntimeFake) Stop(context.Context, *ocruntime.Instance) error {
	f.calls = append(f.calls, "stop")
	if f.onStop != nil {
		f.onStop()
	}
	return f.stopErr
}

func ownerSocket(t *testing.T, handler http.Handler) *ocruntime.Instance {
	t.Helper()
	dir, err := os.MkdirTemp("", "co-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: handler}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return &ocruntime.Instance{Endpoint: "unix://" + path, ID: "old-codex", Kind: ocruntime.KindProviderTmux}
}

func TestRecoverManagedOwnerSafety(t *testing.T) {
	for _, scenario := range []string{"idle", "current", "wrong home", "not loaded", "active", "unsupported", "other platform",
		"protocol skew", "probe fails", "ownership changed", "busy drain", "drain fails", "resume raced", "seal unsupported", "stop fails"} {
		t.Run(scenario, func(t *testing.T) {
			var probes atomic.Int32
			inst := ownerSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hello" {
					hello := providerprotocol.Hello{PlatformID: "codex", CLIVersion: "test", ProtocolVersion: providerprotocol.CurrentVersion,
						Capabilities: providerprotocol.Capabilities{Ownership: scenario != "unsupported"}}
					if scenario == "other platform" {
						hello.PlatformID = "claudecode"
					}
					if scenario == "protocol skew" {
						hello.ProtocolVersion++
					}
					_ = json.NewEncoder(w).Encode(hello)
					return
				}
				if r.URL.Path != "/sessions/thr/ownership" {
					t.Errorf("unexpected probe %s", r.URL)
				}
				if scenario == "probe fails" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				owned := providerprotocol.SessionOwnership{SourceKey: "/codex", Loaded: true, Idle: true}
				switch scenario {
				case "wrong home":
					owned.SourceKey = "/other"
				case "not loaded":
					owned.Loaded = false
				case "active":
					owned.Idle = false
				case "ownership changed":
					owned.Loaded = probes.Load() == 0
				}
				probes.Add(1)
				_ = json.NewEncoder(w).Encode(owned)
			}))
			f := &ownerRuntimeFake{instances: []*ocruntime.Instance{inst}, drain: true, seal: true}
			current := "unix:///current"
			var want []string
			switch scenario {
			case "idle":
				want = []string{"drain", "seal", "stop"}
			case "current":
				current = inst.Endpoint
			case "busy drain":
				f.drain = false
				want = []string{"drain", "resume"}
			case "drain fails":
				f.drainErr = errors.New("lost response")
				want = []string{"drain", "resume"}
			case "ownership changed":
				want = []string{"drain", "resume"}
			case "resume raced":
				f.seal = false
				want = []string{"drain", "seal", "resume"}
			case "seal unsupported":
				f.sealErr = errors.New("unsupported")
				want = []string{"drain", "seal", "resume"}
			case "stop fails":
				f.stopErr = errors.New("stop failed")
				want = []string{"drain", "seal", "stop"}
			}
			recovered, err := recoverManagedOwner(t.Context(), f, "/codex", current, "/root", "thr")
			if recovered != (scenario == "idle") || (err != nil) != (scenario == "stop fails") || !reflect.DeepEqual(f.calls, want) {
				t.Fatalf("recovery = %v, %v, calls %v; want %v", recovered, err, f.calls, want)
			}
		})
	}
}

func TestOwnershipRecoveryRetriesOnlyRejectedDelivery(t *testing.T) {
	var stopped atomic.Bool
	var starts atomic.Int32
	inst := ownerSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			_ = json.NewEncoder(w).Encode(providerprotocol.Hello{PlatformID: "codex", CLIVersion: "test", ProtocolVersion: providerprotocol.CurrentVersion,
				Capabilities: providerprotocol.Capabilities{Ownership: true, Capabilities: platforms.Capabilities{Composer: true}}})
		case "/sessions/thr/ownership":
			_ = json.NewEncoder(w).Encode(providerprotocol.SessionOwnership{SourceKey: "/codex", Loaded: true, Idle: true})
		case "/sessions/thr/send":
			if !stopped.Load() {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(providerprotocol.Error{Code: providerprotocol.CodeSessionOwnedElsewhere, Message: "owned elsewhere"})
				return
			}
			starts.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	f := &ownerRuntimeFake{instances: []*ocruntime.Instance{inst}, drain: true, seal: true, onStop: func() { stopped.Store(true) }}
	a, err := provider.New(t.Context(), inst.Endpoint[len("unix://"):])
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	err = withOwnershipRecovery(t.Context(), func() error {
		return a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "thr", Message: "hi"})
	}, func(ctx context.Context) (bool, error) {
		return recoverManagedOwner(ctx, f, "/codex", "unix:///current", "/root", "thr")
	})
	if err != nil || starts.Load() != 1 || !stopped.Load() {
		t.Fatalf("send = %v, starts = %d, stopped = %v", err, starts.Load(), stopped.Load())
	}
}

func TestOwnershipRecoveryRetryBounds(t *testing.T) {
	owned := &platforms.BusyError{Message: "owned", Cause: platforms.ErrSessionOwnedElsewhere}
	for _, scenario := range []string{"success", "busy", "ambiguous", "unknown owner", "recovery failed", "cancelled", "still owned", "retry ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls, recoveries := 0, 0
			err := withOwnershipRecovery(ctx, func() error {
				calls++
				switch scenario {
				case "success":
					return nil
				case "busy":
					return platforms.ErrBusy
				case "ambiguous":
					return platforms.ErrPlatformUnreachable
				case "retry ambiguous":
					if calls > 1 {
						return platforms.ErrPlatformUnreachable
					}
				}
				return owned
			}, func(context.Context) (bool, error) {
				recoveries++
				if scenario == "cancelled" {
					cancel()
				}
				if scenario == "recovery failed" {
					return false, errors.New("stop failed")
				}
				return scenario != "unknown owner", nil
			})
			wantCalls, wantRecoveries := 1, 1
			switch scenario {
			case "success", "busy", "ambiguous":
				wantRecoveries = 0
			case "still owned":
				wantCalls = 6
			case "retry ambiguous":
				wantCalls = 2
			}
			if calls != wantCalls || recoveries != wantRecoveries || (err == nil) != (scenario == "success") {
				t.Fatalf("result = %v, calls %d recoveries %d; want calls %d recoveries %d", err, calls, recoveries, wantCalls, wantRecoveries)
			}
		})
	}
}

func TestPlatformKeepsUnknownWriterUntouched(t *testing.T) {
	r := newRig(t)
	t.Setenv("FAKE_CODEX_OWNERSHIP_CONFLICT", "1")
	p := r.platform(t.Context())
	err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "thr", Message: "hi"})
	if !errors.Is(err, platforms.ErrSessionOwnedElsewhere) {
		t.Fatalf("ownership rejection lost its identity: %v", err)
	}
	if _, _, err := p.ensure(t.Context()); err != nil {
		t.Fatalf("recovery disrupted the current supervisor: %v", err)
	}
}

func TestOwnerDiscoveryFailures(t *testing.T) {
	for _, scenario := range []string{"list error", "cancelled", "unreachable"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f := &ownerRuntimeFake{instances: []*ocruntime.Instance{{Endpoint: "unix://" + filepath.Join(t.TempDir(), "missing")}}}
			if scenario == "list error" {
				f.listErr = errors.New("unreadable inventory")
			}
			if scenario == "cancelled" {
				cancel()
			}
			recovered, err := recoverManagedOwner(ctx, f, "/codex", "unix:///current", "/root", "thr")
			if recovered || (err != nil) != (scenario != "unreachable") || len(f.calls) != 0 {
				t.Fatalf("unsafe recovery = %v, %v, %v", recovered, err, f.calls)
			}
		})
	}
}
