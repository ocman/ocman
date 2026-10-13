package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func (h *Host) lockProject(root string) func() {
	value, _ := h.projectLocks.LoadOrStore(root, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (h *Host) ManagedIdleProjects(ctx context.Context) ([]string, error) {
	if ocv2.InstalledV2() {
		return nil, nil
	}
	instances, err := h.ManagedOpencodes(ctx)
	var roots []string
	for _, instance := range instances {
		if !instance.Machine {
			roots = append(roots, instance.RepoRoot)
		}
	}
	return roots, err
}

func (h *Host) CullIdleProject(ctx context.Context, root string, directories []string, check func(context.Context) (bool, error)) (bool, error) {
	if ocv2.InstalledV2() || !h.opencodeEnabled() || h.store == nil {
		return false, nil
	}
	unlock := h.lockProject(root)
	defer unlock()
	row, exists, err := h.store.Get(ctx, root)
	if err != nil || !exists {
		return false, err
	}
	inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root}
	if inst.ID == "" { // Only ocman-owned processes may be stopped.
		return false, nil
	}
	if err := h.runtime.Probe(ctx, inst); err != nil {
		return false, err
	}
	idle, err := check(ctx)
	if err != nil || !idle {
		return false, err
	}
	if idle, err = h.opencodeDirectoriesIdle(ctx, inst.Endpoint, append(directories, root)); err != nil || !idle {
		return false, err
	}
	if err := h.runtime.Stop(ctx, inst); err != nil {
		return false, err
	}
	h.clearInstance(root)
	return true, h.store.Delete(ctx, root)
}

func (h *Host) opencodeDirectoriesIdle(ctx context.Context, endpoint string, directories []string) (bool, error) {
	auth := ocapi.Auth{}
	if h.deps.OpenCodeAuth != nil {
		auth = h.deps.OpenCodeAuth()
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: auth.Transport(nil), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	seen := map[string]bool{}
	for _, directory := range directories {
		if seen[directory] {
			continue
		}
		seen[directory] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/session/status?directory="+url.QueryEscape(directory), nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		var statuses map[string]struct {
			Type string `json:"type"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&statuses)
		resp.Body.Close()
		if err != nil {
			return false, fmt.Errorf("reading OpenCode idle status: %w", err)
		}
		if resp.StatusCode != http.StatusOK || statuses == nil {
			return false, fmt.Errorf("reading OpenCode idle status: HTTP %d or missing status map", resp.StatusCode)
		}
		for _, status := range statuses {
			if status.Type != "idle" {
				return false, nil
			}
		}
	}
	return true, nil
}
