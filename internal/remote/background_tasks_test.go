package remote

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestTaskStopNeverReachesOlderRemote(t *testing.T) {
	// No connection is needed: refuse before attempting any RPC. An older
	// owner's Abort would ignore TaskID and stop the entire session.
	p := &remotePlatform{capsSet: true}
	err := p.Abort(t.Context(), platforms.AbortRequest{SessionID: "s1", TaskID: "one"})
	if !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestBackgroundLifecycleNeverReachesOlderRemote(t *testing.T) {
	p := &remotePlatform{capsSet: true}
	if _, err := p.BackgroundTasks(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	err := p.SetBackgroundLifecycle(t.Context(), platforms.SetBackgroundLifecycleRequest{SessionID: "s1", TaskID: "shell", Lifecycle: "service"})
	if !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}
