package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	log "github.com/sirupsen/logrus"
)

// EnsureProjectOpencode shares one managed instance across a project's worktrees.
func (h *Host) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	if ocv2.InstalledV2() {
		return h.ensureMachine(ctx, req.ProjectDir, h.ensureLocked)
	}
	repoRoot, err := projectOpencodeRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	return h.sfDoDetached(ctx, repoRoot, h.ensureLocked)
}

const sfLaunchTimeout = 2 * time.Minute

// The shared launch outlives any one caller, bounded by sfLaunchTimeout.
func (h *Host) sfDoDetached(ctx context.Context, repoRoot string, fn func(context.Context, string) (*hostsvc.EnsureProjectOpencodeResult, error)) (*hostsvc.EnsureProjectOpencodeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type sfResult struct {
		v   any
		err error
	}
	done := make(chan sfResult, 1)
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), sfLaunchTimeout)
	go func() {
		defer cancel()
		v, err, _ := h.sf.Do(repoRoot, func() (any, error) { return fn(detached, repoRoot) })
		done <- sfResult{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		return r.v.(*hostsvc.EnsureProjectOpencodeResult), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func ambiguousProbe(err error) bool {
	var networkError net.Error
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ocruntime.ErrProbeNotReady) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &networkError) && networkError.Timeout())
}

func (h *Host) beforeReplace(ctx context.Context, root, reason string, inst *ocruntime.Instance) error {
	if h.deps.BeforeReplace != nil {
		if err := h.deps.BeforeReplace(ctx, root, reason); err != nil {
			return err
		}
	}
	log.WithFields(log.Fields{"repoRoot": root, "endpoint": inst.Endpoint, "instanceID": inst.ID, "reason": reason}).Warn("host: restarting managed opencode")
	return nil
}

func (h *Host) ensureLocked(ctx context.Context, repoRoot string) (*hostsvc.EnsureProjectOpencodeResult, error) {
	if err := validateReplacementRoot(repoRoot); err != nil {
		return nil, err
	}
	if stopped, err := h.reconcileStopped(ctx, repoRoot, false); err != nil {
		return nil, err
	} else if stopped {
		return h.launchAndTrack(ctx, repoRoot)
	}
	if inst := h.reuseCandidate(ctx, repoRoot); inst != nil {
		// A recovered candidate remains retained until replacement actually stops it.
		h.setInstance(repoRoot, inst)
		probeErr := h.runtime.Probe(ctx, inst)
		if probeErr == nil {
			h.authorizeInstance(repoRoot, inst)
			return &hostsvc.EnsureProjectOpencodeResult{Endpoint: inst.Endpoint, RepoRoot: repoRoot, Runtime: *inst}, nil
		}
		if errors.Is(probeErr, ocruntime.ErrProbeIdentityMismatch) {
			// Retain the local cleanup handle, never authorize the rejected URL.
			h.revokeInstanceAuthorization(repoRoot)
			if ocv2.InstalledV2() {
				h.publishMachineServer("")
			}
		}
		// A slow or overloaded API is not proof that its active turns are dead.
		if ambiguousProbe(probeErr) {
			log.WithError(probeErr).WithFields(log.Fields{"repoRoot": repoRoot, "endpoint": inst.Endpoint, "instanceID": inst.ID}).Warn("host: managed opencode probe inconclusive; preserving server")
			return nil, probeErr
		}
		log.WithError(probeErr).WithFields(log.Fields{"repoRoot": repoRoot, "endpoint": inst.Endpoint, "instanceID": inst.ID}).Warn("host: managed opencode probe failed; relaunching")
		if err := h.beforeReplace(ctx, repoRoot, "failed health probe", inst); err != nil {
			return nil, err
		}
		if err := h.stopForReplacement(ctx, repoRoot, inst); err != nil {
			return nil, err
		}
		return h.launchAndTrack(ctx, repoRoot)
	}
	if h.deps.DiscoverPort != nil {
		if port := h.deps.DiscoverPort(repoRoot); port != "" {
			inst := &ocruntime.Instance{Endpoint: "http://127.0.0.1:" + port, Kind: ocruntime.KindNativeTmux}
			probeErr := h.runtime.Probe(ctx, inst)
			if probeErr == nil {
				h.authorizeInstance(repoRoot, inst)
				return &hostsvc.EnsureProjectOpencodeResult{Endpoint: inst.Endpoint, RepoRoot: repoRoot, Runtime: *inst}, nil
			}
			if ambiguousProbe(probeErr) {
				h.setInstance(repoRoot, inst)
				log.WithError(probeErr).WithFields(log.Fields{"repoRoot": repoRoot, "endpoint": inst.Endpoint}).Warn("host: discovered opencode probe inconclusive; preserving server")
				return nil, probeErr
			}
			log.WithError(probeErr).WithFields(log.Fields{"repoRoot": repoRoot, "endpoint": inst.Endpoint}).Warn("host: discovered opencode probe failed; launching replacement")
			log.WithFields(log.Fields{"repoRoot": repoRoot, "endpoint": inst.Endpoint, "reason": "failed discovered health probe"}).Warn("host: launching replacement opencode")
		}
	}
	return h.launchAndTrack(ctx, repoRoot)
}

func (h *Host) restartLocked(ctx context.Context, repoRoot string) (*hostsvc.EnsureProjectOpencodeResult, error) {
	if err := validateReplacementRoot(repoRoot); err != nil {
		return nil, err
	}
	if stopped, err := h.reconcileStopped(ctx, repoRoot, true); err != nil {
		return nil, err
	} else if stopped {
		return h.launchAndTrack(ctx, repoRoot)
	}
	if inst := h.reuseCandidate(ctx, repoRoot); inst != nil {
		// Cleanup ownership is not routing authorization. Revalidate even an
		// explicit restart so a durable rejected/recycled URL cannot reappear.
		h.setInstance(repoRoot, inst)
		if probeErr := h.runtime.Probe(ctx, inst); errors.Is(probeErr, ocruntime.ErrProbeIdentityMismatch) {
			h.revokeInstanceAuthorization(repoRoot)
			if ocv2.InstalledV2() {
				h.publishMachineServer("")
			}
		} else if probeErr == nil {
			h.authorizeInstance(repoRoot, inst)
		}
		if err := h.beforeReplace(ctx, repoRoot, "requested restart", inst); err != nil {
			return nil, err
		}
		if err := h.stopForReplacement(ctx, repoRoot, inst); err != nil {
			return nil, err
		}
	} else {
		log.WithFields(log.Fields{"repoRoot": repoRoot, "reason": "requested restart", "trackedInstance": false}).Warn("host: restarting managed opencode")
		h.clearInstance(repoRoot)
		if h.store != nil {
			if err := h.store.Delete(ctx, repoRoot); err != nil {
				log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: deleting managed opencode row for restart")
			}
		}
	}
	return h.launchAndTrack(ctx, repoRoot)
}

func (h *Host) launchAndTrack(ctx context.Context, repoRoot string) (*hostsvc.EnsureProjectOpencodeResult, error) {
	permJSON, err := buildExternalDirectoryPermission(worktreesRoot(repoRoot))
	if err != nil {
		return nil, fmt.Errorf("building OPENCODE_PERMISSION: %w", err)
	}
	port, err := ocruntime.AllocateLoopbackPort()
	if err != nil {
		return nil, err
	}
	log.WithFields(log.Fields{"repoRoot": repoRoot, "port": port}).Info("host: launching project opencode")
	inst, err := h.runtime.Launch(ctx, ocruntime.LaunchSpec{
		RepoRoot: repoRoot, Host: "127.0.0.1", Port: port, PermissionJSON: permJSON, V2: ocv2.InstalledV2(),
	})
	if err != nil {
		log.WithError(err).WithField("repoRoot", repoRoot).Error("host: failed to launch project opencode")
		return nil, err
	}
	if probeErr := h.waitForProbe(ctx, inst); probeErr != nil {
		log.WithError(probeErr).WithField("repoRoot", repoRoot).Error("host: launched opencode never became healthy; stopping it")
		if err := h.runtime.Stop(context.WithoutCancel(ctx), inst); err != nil {
			log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: stopping unhealthy managed opencode")
		}
		h.clearInstance(repoRoot)
		if h.store != nil {
			if err := h.store.Delete(context.WithoutCancel(ctx), repoRoot); err != nil {
				log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: deleting managed opencode row after failed launch")
			}
		}
		return nil, probeErr
	}
	h.authorizeInstance(repoRoot, inst)
	if h.store != nil {
		mi := ManagedInstance{Endpoint: inst.Endpoint, Kind: inst.Kind, RuntimeID: inst.ID, PID: inst.PID, LaunchedAt: time.Now()}
		if err := h.store.Upsert(ctx, repoRoot, mi); err != nil {
			log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: persisting managed opencode row")
		}
	}
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: inst.Endpoint, RepoRoot: repoRoot, Runtime: *inst, Launched: true}, nil
}

func (h *Host) currentInstance(repoRoot string) *ocruntime.Instance {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.instances[repoRoot]
}

func (h *Host) reuseCandidate(ctx context.Context, repoRoot string) *ocruntime.Instance {
	if inst := h.currentInstance(repoRoot); inst != nil {
		return inst
	}
	if h.store == nil {
		return nil
	}
	mi, ok, err := h.store.Get(ctx, repoRoot)
	if err != nil {
		log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: reading persisted managed opencode row")
		return nil
	}
	if !ok {
		return nil
	}
	return &ocruntime.Instance{Endpoint: mi.Endpoint, Kind: mi.Kind, ID: mi.RuntimeID, PID: mi.PID, RepoRoot: repoRoot}
}

func (h *Host) setInstance(repoRoot string, inst *ocruntime.Instance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instances[repoRoot] = inst
}

func (h *Host) clearInstance(repoRoot string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.instances, repoRoot)
	delete(h.authorized, repoRoot)
}

func (h *Host) waitForProbe(ctx context.Context, inst *ocruntime.Instance) error {
	deadline := time.Now().Add(h.portWaitTimeout)
	for {
		err := h.runtime.Probe(ctx, inst)
		if err == nil {
			return nil
		}
		if errors.Is(err, ocapi.ErrAuthentication) {
			return fmt.Errorf("managed OpenCode authentication failed: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("opencode launched for %q but did not become healthy within %s", inst.Endpoint, h.portWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.portWaitInterval):
		}
	}
}

func worktreesRoot(repoRoot string) string {
	clean := filepath.Clean(repoRoot)
	return filepath.Join(filepath.Dir(clean), ".worktrees", filepath.Base(clean))
}

func (h *Host) StopProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	if ocv2.InstalledV2() {
		if filepath.Clean(req.ProjectDir) != machineRoot() {
			return nil
		}
	}
	repoRoot, err := projectOpencodeRoot(ctx, req.ProjectDir)
	if ocv2.InstalledV2() {
		repoRoot, err = machineRoot(), nil
	}
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			return nil
		}
		return err
	}
	_, err, _ = h.sf.Do(repoRoot, func() (any, error) {
		if stopped, err := h.reconcileStopped(ctx, repoRoot, false); stopped || err != nil {
			return nil, err
		}
		if inst := h.reuseCandidate(ctx, repoRoot); inst != nil {
			if err := h.runtime.Stop(ctx, inst); err != nil {
				return nil, err
			}
		}
		h.clearInstance(repoRoot)
		if ocv2.InstalledV2() {
			h.publishMachineServer("")
		}
		if h.store != nil {
			if err := h.store.Delete(ctx, repoRoot); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func (h *Host) RestartProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	if ocv2.InstalledV2() {
		return h.ensureMachine(ctx, req.ProjectDir, h.restartLocked)
	}
	root, err := projectOpencodeRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	return h.sfDoDetached(ctx, root, h.restartLocked)
}

func (h *Host) ManagedOpencodes(ctx context.Context) ([]hostsvc.ManagedOpencode, error) {
	if h.store == nil {
		return nil, nil
	}
	instances, err := h.store.List(ctx)
	if err != nil {
		return nil, err
	}
	v2, machine := ocv2.InstalledV2(), machineRoot()
	var out []hostsvc.ManagedOpencode
	for root := range instances {
		if v2 && root != machine {
			continue
		}
		out = append(out, hostsvc.ManagedOpencode{RepoRoot: root, Machine: root == machine})
	}
	return out, nil
}
