package local

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (h *Host) ReloadOpencode(ctx context.Context) error {
	if !ocv2.InstalledV2() {
		return fmt.Errorf("configuration reload requires OpenCode v2: %w", platforms.ErrUnsupported)
	}
	endpoint := ""
	if inst := h.currentInstance(machineRoot()); inst != nil {
		endpoint = inst.Endpoint
	} else if h.store != nil {
		inst, ok, err := h.store.Get(ctx, machineRoot())
		if err != nil {
			return err
		}
		if ok {
			endpoint = inst.Endpoint
		}
	}
	if endpoint == "" {
		return fmt.Errorf("no managed OpenCode v2 server found")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/location/reload", nil)
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
