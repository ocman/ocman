package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

type lifecyclePlatform struct {
	*fakePlatform
	supported bool
	updates   []platforms.SetBackgroundLifecycleRequest
}

func (p *lifecyclePlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{BackgroundTaskLifecycle: p.supported}
}
func (p *lifecyclePlatform) BackgroundTasks(context.Context, string) (*platforms.BackgroundSnapshot, error) {
	return nil, nil
}
func (p *lifecyclePlatform) SetBackgroundLifecycle(_ context.Context, req platforms.SetBackgroundLifecycleRequest) error {
	p.updates = append(p.updates, req)
	return nil
}

func TestBackgroundLifecycleValidationAndOwner(t *testing.T) {
	p := &lifecyclePlatform{fakePlatform: &fakePlatform{id: "r-owner:opencode"}, supported: true}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.id: p}}, Hooks{})
	for _, lifecycle := range []string{"task", "service"} {
		req := platforms.SetBackgroundLifecycleRequest{SessionID: "s1", TaskID: "shell", Lifecycle: lifecycle}
		if err := svc.SetBackgroundLifecycle(t.Context(), string(p.id), req); err != nil {
			t.Fatal(err)
		}
		if p.updates[len(p.updates)-1] != req {
			t.Fatal("owner request changed")
		}
	}
	for _, req := range []platforms.SetBackgroundLifecycleRequest{{SessionID: "s1", Lifecycle: "service"}, {SessionID: "s1", TaskID: "shell", Lifecycle: "auto"}} {
		var validation *ValidationError
		if err := svc.SetBackgroundLifecycle(t.Context(), string(p.id), req); !errors.As(err, &validation) {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	p.supported = false
	err := svc.SetBackgroundLifecycle(t.Context(), string(p.id), platforms.SetBackgroundLifecycleRequest{SessionID: "s1", TaskID: "shell", Lifecycle: "service"})
	if !errors.Is(err, platforms.ErrUnsupported) || len(p.updates) != 2 {
		t.Fatalf("err=%v calls=%v", err, p.updates)
	}
}
