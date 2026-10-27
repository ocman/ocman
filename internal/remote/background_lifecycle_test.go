package remote

import (
	"context"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type lifecycleOwner struct {
	*fakePlatform
	mu     sync.Mutex
	update platforms.SetBackgroundLifecycleRequest
}

func (*lifecycleOwner) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{BackgroundTaskLifecycle: true}
}
func (*lifecycleOwner) BackgroundTasks(_ context.Context, id string) (*platforms.BackgroundSnapshot, error) {
	if id != "s" {
		return nil, platforms.ErrNotFound
	}
	return &platforms.BackgroundSnapshot{Count: 1, Tasks: []db.BackgroundTask{{ID: "shell", Description: "watch", Lifecycle: "service"}}}, nil
}
func (p *lifecycleOwner) SetBackgroundLifecycle(_ context.Context, req platforms.SetBackgroundLifecycleRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.update = req
	return nil
}

func TestBackgroundLifecycleRoundTripsToOwner(t *testing.T) {
	p := &lifecycleOwner{fakePlatform: &fakePlatform{id: "opencode"}}
	reg := platforms.NewRegistry()
	reg.Register(p)
	srv := NewServer(reg, localStubHost{}, "owner", "v-test")
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	conn := NewRemoteConn("grpc://"+ln.Addr(), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Owner" })
	snapshot, err := rp.BackgroundTasks(t.Context(), "s")
	if err != nil || snapshot.Count != 1 || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Lifecycle != "service" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	req := platforms.SetBackgroundLifecycleRequest{SessionID: "s", TaskID: "shell", Lifecycle: "task"}
	if err := rp.SetBackgroundLifecycle(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.update != req {
		t.Fatalf("owner received %+v", p.update)
	}
}
