package routines

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineProvider struct{ *testPlatform }

func (routineProvider) ID() platforms.ID { return "fake-provider" }

type routineRuntimeHost struct {
	*testHost
	platform string
}

func (h *routineRuntimeHost) EnsureRuntime(ctx context.Context, platform, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	h.platform = platform
	return h.testHost.EnsureRuntime(ctx, platform, dir)
}

func TestRoutinePlatformIsSavedAndDispatched(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "setting", true: "explicit"}[explicit], func(t *testing.T) {
			h := newHarness(t)
			provider := &routineProvider{&testPlatform{directory: "/repo"}}
			h.svc.platforms.Register(provider)
			host := &routineRuntimeHost{testHost: h.host}
			h.svc.router = hostsvc.NewRouter(host)
			input := validInput()
			if explicit {
				input.Platform, input.Model = "fake-provider", "fake-provider:anthropic/claude"
			} else if err := h.db.SetSetting(t.Context(), state.DefaultPlatformSetting, "fake-provider"); err != nil {
				t.Fatal(err)
			}
			routine, err := h.svc.Create(t.Context(), input)
			if err != nil || routine.Platform != "fake-provider" {
				t.Fatalf("create = %+v, %v", routine, err)
			}
			// Changing the global setting cannot move an already-saved routine.
			if err := h.db.SetSetting(t.Context(), state.DefaultPlatformSetting, "opencode"); err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.RunNow(t.Context(), routine.ID)
			if err != nil || run.Platform != "fake-provider" || host.platform != "fake-provider" {
				t.Fatalf("run = %+v, %v; ensured %q", run, err, host.platform)
			}
			if created, _ := provider.counts(); created != 1 {
				t.Fatalf("provider creates = %d", created)
			}
			if created, _ := h.platform.counts(); created != 0 {
				t.Fatalf("OpenCode creates = %d", created)
			}
			updated, err := h.svc.Update(t.Context(), routine.ID, validInput())
			if err != nil || updated.Platform != "fake-provider" {
				t.Fatalf("update = %+v, %v", updated, err)
			}
		})
	}
}
