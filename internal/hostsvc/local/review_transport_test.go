package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

func TestTransportFailuresPreserveManagedRuntime(t *testing.T) {
	for _, cause := range []error{syscall.ECONNRESET, io.EOF, io.ErrUnexpectedEOF} {
		t.Run(cause.Error(), func(t *testing.T) {
			probeErr := fmt.Errorf("%w: transport: %w", ocruntime.ErrProbeUnreachable, cause)
			rt := &probeErrorRuntime{err: probeErr}
			h := New(Deps{Runtime: rt})
			inst := &ocruntime.Instance{Endpoint: "old", ID: "owned"}
			h.authorizeInstance("repo", inst)
			h.deps.BeforeReplace = func(context.Context, string, string) error { t.Error("prepared after inconclusive probe"); return nil }
			if _, err := h.ensureLocked(t.Context(), "repo"); !errors.Is(err, probeErr) || rt.stopCount() != 0 || rt.launchCount() != 0 || h.currentInstance("repo") != inst || h.authorizedEndpoint("repo") != "old" {
				t.Fatalf("transport failure replaced live runtime: err=%v stops=%d launches=%d", err, rt.stopCount(), rt.launchCount())
			}
		})
	}
}
