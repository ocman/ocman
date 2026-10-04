package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type lifecycleFakePlatform struct {
	*fakePlatform
	lifecycle *platforms.SessionLifecycle
	sessionID string
}

func (f *lifecycleFakePlatform) SessionLifecycle(_ context.Context, id string) (*platforms.SessionLifecycle, error) {
	f.sessionID = id
	return f.lifecycle, nil
}

func TestRemotePlatformSessionLifecycle(t *testing.T) {
	want := platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m9", LatestMessageCreated: 9, LatestMessageRole: "assistant"}
	fp := &lifecycleFakePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: &want}
	reg := platforms.NewRegistry()
	reg.Register(fp)
	conn := NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	got, err := rp.SessionLifecycle(t.Context(), "s1")
	if err != nil || *got != want || fp.sessionID != "s1" {
		t.Fatalf("lifecycle = %+v, %v (owner saw %q); want %+v for s1", got, err, fp.sessionID, want)
	}
}

// An owner whose adapter cannot serve the bounded read answers
// Unimplemented, which the hub must see as ErrUnsupported so the queue
// falls back to Session rather than failing closed.
func TestRemotePlatformSessionLifecycleUnsupported(t *testing.T) {
	rp := newRemotePlatform(connectedPair(t, &fakePlatform{id: "opencode"}), "opencode", func() string { return "Box" })
	if _, err := rp.SessionLifecycle(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	offline := newRemotePlatform(NewRemoteConn("grpc://127.0.0.1:1", "tok"), "opencode", func() string { return "Box" })
	if _, err := offline.SessionLifecycle(t.Context(), "s1"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("offline err = %v, want ErrRemoteOffline", err)
	}
}
