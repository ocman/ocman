package codextransport

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

type ownerRuntime interface {
	Instances(string) ([]*ocruntime.Instance, error)
	DrainIdle(context.Context, *ocruntime.Instance, string) (bool, error)
	SealIdle(context.Context, *ocruntime.Instance, string) (bool, error)
	Resume(context.Context, *ocruntime.Instance, string) error
	Stop(context.Context, *ocruntime.Instance) error
}

// doSession retries only a definite pre-turn ownership rejection, never an
// ambiguous turn/start or transport failure. Stop must be verified first.
func (p *Platform) doSession(ctx context.Context, id string, fn func(*provider.Adapter) error) error {
	return withOwnershipRecovery(ctx, func() error { return p.do(ctx, fn) }, func(ctx context.Context) (bool, error) {
		p.ensureMu.Lock()
		defer p.ensureMu.Unlock()
		p.mu.Lock()
		endpoint := p.endpoint
		p.mu.Unlock()
		return recoverManagedOwner(ctx, p.mgr.Runtime, p.home, endpoint, p.root, id)
	})
}

func withOwnershipRecovery(ctx context.Context, run func() error, recoverOwner func(context.Context) (bool, error)) error {
	err := run()
	if !errors.Is(err, platforms.ErrSessionOwnedElsewhere) {
		return err
	}
	recovery, cancel := context.WithTimeout(ctx, 5*time.Second)
	recovered, recoveryErr := recoverOwner(recovery)
	cancel()
	if recoveryErr != nil || !recovered {
		if recoveryErr != nil {
			log.WithError(recoveryErr).Warn("codex: ownership recovery failed")
		}
		return err
	}
	// The old app-server exits on stdin EOF after its supervisor stops. Allow
	// bounded retries while that writer releases its OS lock; no turn was sent.
	for range 5 {
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		err = run()
		if !errors.Is(err, platforms.ErrSessionOwnedElsewhere) {
			return err
		}
	}
	return err
}

func recoverManagedOwner(ctx context.Context, runtime ownerRuntime, home, current, root, id string) (bool, error) {
	instances, err := runtime.Instances(root)
	if err != nil {
		return false, err
	}
	for _, inst := range instances {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if inst.Endpoint == current {
			continue
		}
		probe, cancel := context.WithTimeout(ctx, time.Second)
		owned := ownsIdleThread(probe, inst, home, id)
		cancel()
		if !owned {
			continue
		}
		token := rand.Text()
		idle, err := runtime.DrainIdle(ctx, inst, token)
		if err != nil || !idle {
			resumeOwner(ctx, runtime, inst, token)
			continue
		}
		// Recheck ownership under the admission fence, then atomically seal
		// that fence. A concurrent resume invalidates our token and blocks Stop.
		if !ownsIdleThread(ctx, inst, home, id) {
			resumeOwner(ctx, runtime, inst, token)
			continue
		}
		sealed, err := runtime.SealIdle(ctx, inst, token)
		if err != nil || !sealed {
			resumeOwner(ctx, runtime, inst, token)
			continue
		}
		if err := runtime.Stop(ctx, inst); err != nil {
			// A failed stop can be ambiguous. Never undo a committed drain.
			return false, err
		}
		log.WithFields(log.Fields{"runtime": inst.ID, "sessionID": id}).Info("codex: recovered an idle managed thread owner")
		return true, nil
	}
	return false, nil
}

func ownsIdleThread(ctx context.Context, inst *ocruntime.Instance, home, id string) bool {
	a, err := provider.New(ctx, strings.TrimPrefix(inst.Endpoint, "unix://"))
	if err != nil {
		return false
	}
	defer a.Close()
	if a.ID() != "codex" {
		return false
	}
	owned, err := a.SessionOwnership(ctx, id)
	return err == nil && owned.Loaded && owned.Idle && owned.SourceKey == home && home != ""
}

func resumeOwner(ctx context.Context, runtime ownerRuntime, inst *ocruntime.Instance, token string) {
	rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = runtime.Resume(rollback, inst, token)
}
