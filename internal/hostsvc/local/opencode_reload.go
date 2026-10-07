package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
		var diagnostic struct {
			Message string `json:"message"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		message := fmt.Sprintf("OpenCode reload returned HTTP %d", resp.StatusCode)
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&diagnostic); err == nil {
			if diagnostic.Message != "" {
				message = diagnostic.Message
			} else if diagnostic.Data.Message != "" {
				message = diagnostic.Data.Message
			}
		}
		if len(message) > 1024 {
			message = strings.ToValidUTF8(message[:1024], "")
		}
		return &platforms.UpstreamError{Status: resp.StatusCode, Message: message}
	}
	if h.deps.OpenCodeReloaded != nil {
		h.deps.OpenCodeReloaded(req.URL.Port())
	}
	return nil
}
