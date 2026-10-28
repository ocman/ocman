package sessionsvc

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

type taskStopPlatform struct {
	*fakePlatform
	supported bool
}

func (p taskStopPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{BackgroundTaskStop: p.supported}
}

func TestTaskStopRequiresOwnerCapability(t *testing.T) {
	for _, supported := range []bool{false, true} {
		p := taskStopPlatform{fakePlatform: &fakePlatform{id: "r-owner:opencode"}, supported: supported}
		svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.id: p}}, Hooks{})
		req := platforms.AbortRequest{SessionID: "s1", TaskID: "one"}
		err := svc.Abort(t.Context(), string(p.id), req)
		if supported {
			if err != nil || len(p.aborts) != 1 || p.aborts[0] != req {
				t.Fatalf("err=%v calls=%v", err, p.aborts)
			}
		} else if !errors.Is(err, platforms.ErrUnsupported) || len(p.aborts) != 0 {
			t.Fatalf("unsupported owner received abort: %v %v", err, p.aborts)
		}
	}
}
