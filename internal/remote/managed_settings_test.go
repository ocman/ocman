package remote

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/testutil"
)

// startManagedRemote serves a remote whose settings layer is store.
func startManagedRemote(t *testing.T, store ManagedSettingsStore) string {
	t.Helper()
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	srv := NewServer(reg, localStubHost{}, "managed-1", "v-test")
	if store != nil {
		srv.UseManagedSettings(store)
	}
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	return "grpc://" + ln.Addr()
}

// startHub connects a manager to addr and returns it once connected.
func startHub(t *testing.T, addr string, snapshot func(context.Context) (state.ManagedSettings, error)) (*Manager, int64) {
	t.Helper()
	hubStore := statetest.Open(t)
	localID, err := hubStore.AddRemote(t.Context(), addr, "tok", "Remote")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), hubStore, "opencode")
	mgr.UseManagedSettings(snapshot)
	t.Cleanup(mgr.Stop)
	mgr.Start(context.Background())
	return mgr, localID
}

func waitForSettingsStatus(t *testing.T, mgr *Manager, want string) {
	t.Helper()
	testutil.WaitFor(t, waitTimeout, "managed settings status "+want, func() bool {
		list, err := mgr.List(t.Context())
		return err == nil && len(list) == 1 && list[0].ManagedSettings == want
	})
}

func TestManagerSendsSettingsToManagedRemote(t *testing.T) {
	remoteStore := statetest.Open(t)
	if err := remoteStore.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	addr := startManagedRemote(t, remoteStore)
	var delay atomic.Int64
	delay.Store(100)
	mgr, _ := startHub(t, addr, func(context.Context) (state.ManagedSettings, error) {
		return state.ManagedSettings{Hub: "hub-1", JudgeModel: "p/m", JudgeDelayMs: delay.Load(), AutoApproveDefault: true}, nil
	})

	// Sent on connect.
	waitForSettingsStatus(t, mgr, ManagedSettingsApplied)
	if got, _ := remoteStore.GetJudgeDelayMs(t.Context()); got != 100 {
		t.Fatalf("delay after connect = %d", got)
	}
	if got, ok := remoteStore.ManagedSettings(); !ok || got.Hub != "hub-1" || !got.AutoApproveDefault || got.JudgeModel != "p/m" {
		t.Fatalf("snapshot = %+v", got)
	}

	// Resent after a hub setting changes.
	delay.Store(200)
	mgr.PushManagedSettings()
	testutil.WaitFor(t, waitTimeout, "the changed delay to reach the remote", func() bool {
		got, _ := remoteStore.GetJudgeDelayMs(t.Context())
		return got == 200
	})
}

func TestManagerReportsUnmanagedAndOlderRemotes(t *testing.T) {
	snapshot := func(context.Context) (state.ManagedSettings, error) { return state.ManagedSettings{Hub: "hub-1"}, nil }

	// A current remote started without -remote-managed keeps its settings.
	plain := statetest.Open(t)
	if err := plain.SetJudgeDelayMs(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	mgr, _ := startHub(t, startManagedRemote(t, plain), snapshot)
	waitForSettingsStatus(t, mgr, ManagedSettingsNotManaged)
	if got, _ := plain.GetJudgeDelayMs(t.Context()); got != 42 {
		t.Fatalf("unmanaged remote delay changed to %d", got)
	}

	// A remote predating the RPC.
	mgr, _ = startHub(t, startManagedRemote(t, nil), snapshot)
	waitForSettingsStatus(t, mgr, ManagedSettingsUnsupported)
}

func TestManagerReportsSnapshotFailure(t *testing.T) {
	remoteStore := statetest.Open(t)
	if err := remoteStore.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	mgr, _ := startHub(t, startManagedRemote(t, remoteStore), func(context.Context) (state.ManagedSettings, error) {
		return state.ManagedSettings{}, errors.New("state unavailable")
	})
	waitForSettingsStatus(t, mgr, ManagedSettingsFailed)
	if _, ok := remoteStore.ManagedSettings(); ok {
		t.Fatal("remote received a snapshot")
	}
}

func TestPushManagedSettingsWithoutProviderOrAfterStop(t *testing.T) {
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), statetest.Open(t), "opencode")
	mgr.PushManagedSettings() // no provider: nothing to send
	mgr.pushManagedSettings(t.Context(), &managedRemote{})
	mgr.UseManagedSettings(func(context.Context) (state.ManagedSettings, error) {
		t.Error("stopped manager read settings")
		return state.ManagedSettings{}, nil
	})
	mgr.Stop()
	mgr.PushManagedSettings()
}

type failingManagedStore struct {
	previous *state.ManagedSettings
	err      error
}

func (s failingManagedStore) ManagedSettings() (state.ManagedSettings, bool) {
	if s.previous == nil {
		return state.ManagedSettings{}, false
	}
	return *s.previous, true
}

func (s failingManagedStore) ApplyManagedSettings(context.Context, state.ManagedSettings, int64) error {
	return s.err
}

func TestApplyManagedSettingsValidation(t *testing.T) {
	srv := NewServer(platforms.NewRegistry(), localStubHost{}, "owner", "test")
	if _, err := srv.ApplyManagedSettings(t.Context(), &pb.CallReq{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("without store = %v", err)
	}
	srv.UseManagedSettings(failingManagedStore{})
	for _, payload := range []string{"bad", `{}`, `{"hub":""}`} {
		if _, err := srv.ApplyManagedSettings(t.Context(), &pb.CallReq{Payload: []byte(payload)}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("payload %q = %v", payload, err)
		}
	}
	valid, _ := json.Marshal(state.ManagedSettings{Hub: "hub-2"})
	srv.UseManagedSettings(failingManagedStore{err: errors.New("disk full")})
	if _, err := srv.ApplyManagedSettings(t.Context(), &pb.CallReq{Payload: valid}); status.Code(err) != codes.Internal {
		t.Fatalf("store failure = %v", err)
	}
	// A different hub replacing the settings is accepted, only warned about.
	srv.UseManagedSettings(failingManagedStore{previous: &state.ManagedSettings{Hub: "hub-1"}})
	if _, err := srv.ApplyManagedSettings(t.Context(), &pb.CallReq{Payload: valid}); err != nil {
		t.Fatalf("second hub = %v", err)
	}
}
