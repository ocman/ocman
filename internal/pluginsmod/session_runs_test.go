package pluginsmod

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestSessionRunReservationConcurrentAndUncertain(t *testing.T) {
	d := openPluginStore(t)
	p := plugins.SessionRunParams{Prompt: "once"}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, claimed, err := d.reserveSessionRun(t.Context(), "p", "op", "/repo", p)
			if claimed {
				claims.Add(1)
			} else if !errors.Is(err, plugins.ErrUnavailable) {
				t.Errorf("uncertain run: %v", err)
			}
		})
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("claims=%d", claims.Load())
	}
	// A restart with a pending receipt cannot safely retry creation.
	restarted := &Store{db: d.db}
	if _, claimed, err := restarted.reserveSessionRun(t.Context(), "p", "op", "/repo", p); claimed || !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("pending replay: %v %v", claimed, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := d.finishSessionRun(ctx, "p", "op", "", &plugins.WireError{Category: plugins.ErrorNotFound}); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := restarted.reserveSessionRun(t.Context(), "p", "op", "/repo", p); claimed || plugins.RequestError(err).Category != plugins.ErrorNotFound {
		t.Fatalf("failed replay: %v %v", claimed, err)
	}
	if _, claimed, err := d.reserveSessionRun(t.Context(), "other-plugin", "op", "/repo", p); !claimed || err != nil {
		t.Fatalf("plugin scope: %v %v", claimed, err)
	}
	if _, _, err := d.reserveSessionRun(t.Context(), "p", "op", "/another-project", p); plugins.RequestError(err).Category != plugins.ErrorConflict {
		t.Fatalf("project conflict: %v", err)
	}
}

func TestSessionRunStorageFailsClosed(t *testing.T) {
	d := openPluginStore(t)
	if err := d.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := d.reserveSessionRun(t.Context(), "p", "op", "/repo", plugins.SessionRunParams{Prompt: "go"}); claimed || !errors.Is(err, state.ErrPluginState) {
		t.Fatalf("reserve: %v %v", claimed, err)
	}
	if err := d.finishSessionRun(t.Context(), "p", "op", "ses-1", nil); !errors.Is(err, state.ErrPluginState) {
		t.Fatalf("finish: %v", err)
	}
}
