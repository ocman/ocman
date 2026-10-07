package local

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (h *Host) ReloadOpencode(ctx context.Context) error {
	if !ocv2.InstalledV2() {
		return fmt.Errorf("configuration reload requires OpenCode v2: %w", platforms.ErrUnsupported)
	}
	root := machineRoot()
	candidate := h.currentInstance(root)
	if candidate == nil && h.store != nil {
		inst, ok, err := h.store.Get(ctx, root)
		if err != nil {
			return err
		}
		if ok {
			candidate = &ocruntime.Instance{Endpoint: inst.Endpoint, Kind: inst.Kind, ID: inst.RuntimeID, PID: inst.PID}
		}
	}
	if candidate == nil || candidate.Endpoint == "" {
		return fmt.Errorf("no managed OpenCode v2 server found")
	}
	inst := *candidate
	inst.RepoRoot = root
	if err := h.runtime.Probe(ctx, &inst); err != nil {
		return fmt.Errorf("verifying managed OpenCode before reload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inst.Endpoint+"/api/location/reload", nil)
	if err != nil {
		return err
	}
	auth := ocapi.Auth{}
	if h.deps.OpenCodeAuth != nil {
		auth = h.deps.OpenCodeAuth()
	}
	client := &http.Client{Timeout: time.Minute, Transport: auth.Transport(nil)}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reloading OpenCode: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("reloading OpenCode: upstream HTTP %d", resp.StatusCode)
	}
	if h.deps.OpenCodeReloaded != nil {
		h.deps.OpenCodeReloaded(req.URL.Port())
	}
	return nil
}
