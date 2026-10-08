package local

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	log "github.com/sirupsen/logrus"
)

func (h *Host) stopForReplacement(ctx context.Context, root string, inst *ocruntime.Instance) error {
	if h.deps.BeforeStop != nil {
		if err := h.deps.BeforeStop(ctx, root, inst); err != nil {
			return err
		}
	}
	if err := h.runtime.Stop(ctx, inst); err != nil {
		log.WithError(err).WithFields(log.Fields{"repoRoot": root, "endpoint": inst.Endpoint, "instanceID": inst.ID}).Warn("host: stop failed; preserving instance and aborting replacement")
		return fmt.Errorf("stopping managed opencode: %w", err)
	}
	h.markStopped(root)
	_, err := h.reconcileStopped(ctx, root, false)
	return err
}

func (h *Host) markStopped(root string) {
	h.mu.Lock()
	delete(h.instances, root)
	delete(h.authorized, root)
	h.stopped[root] = true
	h.mu.Unlock()
	if ocv2.InstalledV2() {
		h.publishMachineServer("")
	}
}

// Called under the root's singleflight before discovery, probing or launching.
// A stopped instance is no longer a runtime candidate, even if its row survives.
func (h *Host) reconcileStopped(ctx context.Context, root string, explicitRestart bool) (bool, error) {
	h.mu.Lock()
	stopped := h.stopped[root]
	h.mu.Unlock()
	if !stopped && h.deps.ReplacementStopped != nil {
		var err error
		stopped, err = h.deps.ReplacementStopped(ctx, root)
		if err != nil {
			return false, err
		}
		if stopped {
			h.markStopped(root)
		}
	}
	if !stopped {
		var err error
		stopped, err = h.recoverStopping(ctx, root, explicitRestart)
		if err != nil || !stopped {
			return stopped, err
		}
	}
	if h.deps.AfterStop != nil {
		// Reconciliation shares the detached launch budget; a second, shorter
		// deadline can strand a stopped server while processing its history.
		confirmCtx, cancel := context.WithTimeout(ctx, sfLaunchTimeout)
		defer cancel()
		if err := h.deps.AfterStop(confirmCtx, root); err != nil {
			return true, err
		}
	}
	if h.store != nil {
		if err := h.store.Delete(ctx, root); err != nil {
			return true, fmt.Errorf("deleting stopped managed opencode row: %w", err)
		}
	}
	h.mu.Lock()
	delete(h.stopped, root)
	h.mu.Unlock()
	return true, nil
}
