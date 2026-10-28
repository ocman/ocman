package local

import (
	"context"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	log "github.com/sirupsen/logrus"
)

// ErrServiceNotManaged refuses lifecycle actions on OpenCode's own
// background service, which other OpenCode clients share.
var ErrServiceNotManaged = errors.New("OpenCode's background service is not managed by ocman; restart it from OpenCode")

func isService(inst *ocruntime.Instance) bool {
	return inst != nil && inst.Kind == ocruntime.KindService
}

// useService routes the v2 machine to OpenCode's own background service
// when one is registered, instead of launching a second server on the
// same database (two servers migrating one database lock each other out).
//
// handled is false when there is no usable service and the caller should
// launch its own. Authentication rejection is handled without launching:
// the service still owns the database. A live service whose probe is
// inconclusive (busy, e.g. migrating) is handled: it is tracked unauthorized
// and the probe error returned, so nothing is launched next to it.
func (h *Host) useService(ctx context.Context, root string) (res *hostsvc.EnsureRuntimeResult, handled bool, err error) {
	if h.deps.OpenCodeService == nil || !ocv2.InstalledV2() || root != machineRoot() {
		return nil, false, nil
	}
	svc, err := h.deps.OpenCodeService()
	if err != nil {
		if !errors.Is(err, ocv2.ErrNoService) {
			log.WithError(err).Warn("host: reading OpenCode service registration")
		}
		return nil, false, nil
	}
	// RepoRoot stays empty: the service is rooted wherever the user first
	// ran opencode, so there is no launch directory to verify.
	inst := &ocruntime.Instance{Endpoint: svc.URL, Kind: ocruntime.KindService, PID: svc.PID}
	fields := log.Fields{"endpoint": svc.URL, "pid": svc.PID, "version": svc.Version}
	probeErr := h.runtime.Probe(ctx, inst)
	switch {
	case probeErr == nil:
		h.authorizeInstance(root, inst)
		log.WithFields(fields).Info("host: using OpenCode background service")
		return &hostsvc.EnsureRuntimeResult{Endpoint: inst.Endpoint, RepoRoot: root, Runtime: *inst}, true, nil
	case errors.Is(probeErr, ocapi.ErrAuthentication):
		return nil, true, fmt.Errorf("authenticating OpenCode background service; check configured OpenCode credentials against its service configuration: %w", probeErr)
	case ambiguousProbe(probeErr):
		h.setInstance(root, inst)
		log.WithError(probeErr).WithFields(fields).Warn("host: OpenCode background service is busy; waiting for it")
		return nil, true, fmt.Errorf("OpenCode background service is not ready yet: %w", probeErr)
	default:
		log.WithError(probeErr).WithFields(fields).Warn("host: OpenCode background service unusable; launching ocman's own server")
		return nil, false, nil
	}
}

// launchOrUseService prefers the background service over a launch.
func (h *Host) launchOrUseService(ctx context.Context, root string) (*hostsvc.EnsureRuntimeResult, error) {
	if res, handled, err := h.useService(ctx, root); handled {
		return res, err
	}
	return h.launchAndTrack(ctx, root)
}

// forgetService drops routing to a service without stopping it.
func (h *Host) forgetService(root string) {
	h.clearInstance(root)
	h.runtimeInventoryChanged()
	if ocv2.InstalledV2() {
		h.publishMachineServer("")
	}
}
