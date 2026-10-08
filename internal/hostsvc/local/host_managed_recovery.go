package local

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func validateReplacementRoot(root string) error {
	if ocv2.InstalledV2() && filepath.Clean(root) != machineRoot() {
		return fmt.Errorf("OpenCode v2 replacement requires machine root, got %q", root)
	}
	return nil
}

// Recover the original attempt before inventory or preparation can replace its
// handle and evidence. Stop's successful return, not Probe, establishes closure.
func (h *Host) recoverStopping(ctx context.Context, root string, explicitRestart bool) (bool, error) {
	if h.deps.ReplacementStopping == nil {
		return false, nil
	}
	inst, err := h.deps.ReplacementStopping(ctx, root)
	if err != nil || inst == nil {
		return false, err
	}
	if inst.Endpoint == "" || (inst.RepoRoot != "" && inst.RepoRoot != root) {
		return false, errors.New("unresolved replacement has an invalid runtime handle")
	}
	// Only an exact warm handle retains process-local routing proof. A durable
	// handle alone, or a recycled endpoint with another handle, authorizes nothing.
	h.mu.Lock()
	warm := h.instances[root]
	matching := warm != nil && *warm == *inst && h.authorized[root] == inst.Endpoint
	h.mu.Unlock()
	if !matching {
		h.clearInstance(root)
		if ocv2.InstalledV2() {
			h.publishMachineServer("")
		}
	}
	probeErr := h.runtime.Probe(ctx, inst)
	if probeErr == nil {
		if h.deps.CancelReplacementStop == nil {
			return false, errors.New("unresolved running replacement requires cancellation callback")
		}
		if err := h.deps.CancelReplacementStop(ctx, root); err != nil {
			return false, err
		}
		h.authorizeInstance(root, inst)
		return false, nil
	}
	rejected := errors.Is(probeErr, ocruntime.ErrProbeIdentityMismatch)
	unauthenticated := errors.Is(probeErr, ocapi.ErrAuthentication)
	if rejected || unauthenticated {
		h.revokeInstanceAuthorization(root)
		if ocv2.InstalledV2() {
			h.publishMachineServer("")
		}
	}
	retryOwnedStop := false
	if explicitRestart && unauthenticated && inst.ID != "" && inst.RepoRoot == root {
		// The inventory proves cleanup ownership independently of the HTTP
		// endpoint. An adopted or different handle cannot use this exception.
		retryOwnedStop = matching
		if !retryOwnedStop && h.store != nil {
			registered, found, err := h.store.Get(ctx, root)
			if err != nil {
				return false, fmt.Errorf("verifying replacement runtime ownership: %w", err)
			}
			retryOwnedStop = found && registered.Endpoint == inst.Endpoint && registered.Kind == inst.Kind && registered.RuntimeID == inst.ID && registered.PID == inst.PID
		}
	}
	if !retryOwnedStop && !rejected && (ambiguousProbe(probeErr) || !errors.Is(probeErr, ocruntime.ErrProbeUnreachable)) {
		return false, probeErr
	}
	// Repeat only the idempotent closure check with the saved handle. Do not
	// resample BeforeReplace/BeforeStop: those belong to the original attempt.
	if err := h.runtime.Stop(ctx, inst); err != nil {
		return false, fmt.Errorf("recovering managed opencode stop: %w", err)
	}
	h.markStopped(root)
	return true, nil
}
