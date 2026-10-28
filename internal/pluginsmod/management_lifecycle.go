package pluginsmod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// Include proportional scheduling slack beyond the supervisor's handshake
// deadline. Defaults remain 15s for the handshake and 20s for management.
func (m *Module) managementReadyTimeout() time.Duration {
	ready := m.deps.ReadyTimeout
	if ready <= 0 {
		ready = plugins.ReadyTimeout
	}
	return ready + ready/3
}

func (m *Module) stopOnePluginLocked(id string) {
	if process := m.processes[id]; process != nil {
		process.Stop()
		if m.stderr == nil {
			m.stderr = make(map[string]string)
		}
		m.stderr[id] = process.RedactedStderr(func(text string) string { return m.store.RedactPluginDiagnostics(id, text) })
		delete(m.processes, id)
	}
}

func (m *Module) restartPluginLocked(ctx context.Context, id string) error {
	m.stopOnePluginLocked(id)
	if m.ctx == nil || m.ctx.Err() != nil {
		return plugins.ErrUnavailable
	}
	if err := m.store.SetPluginHealth(ctx, id, state.PluginHealth{Status: "starting"}); err != nil {
		return err
	}
	if err := m.syncPluginProcessesLocked(ctx); err != nil {
		return err
	}
	process := m.processes[id]
	if process == nil {
		return plugins.ErrUnavailable
	}
	// ponytail: readiness polling uses the supervisor's existing synchronized
	// health; add notifications only if management traffic warrants them.
	deadline := time.NewTimer(m.managementReadyTimeout())
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		health := process.Health()
		if health.Status == "ready" {
			return nil
		}
		if health.LastError != "" || health.RestartCount > 0 || health.Status == "stopped" || health.Status == "unhealthy" {
			break
		}
		select {
		case <-ctx.Done():
			break wait
		case <-deadline.C:
			break wait
		case <-tick.C:
		}
	}
	m.stopOnePluginLocked(id)
	// Use a fresh context so a disconnected client cannot leave a failed launch
	// retrying indefinitely or lose its terminal health.
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.store.SetPluginHealth(cleanup, id, state.PluginHealth{Status: "unhealthy", LastError: "plugin readiness failed"}); err != nil {
		return err
	}
	return plugins.ErrUnavailable
}

func (m *Module) activatePluginConfigurationLocked(ctx context.Context, p state.PluginRegistration, values map[string]json.RawMessage, secrets map[string]string) error {
	id := p.Description.ID
	process := m.processes[id]
	if process == nil || process.Health().Status != "ready" {
		return errPluginConflict
	}
	if err := m.store.MarkPluginConfigurationWorking(ctx, id); err != nil {
		return err
	}
	if err := m.store.SetPluginConfiguration(ctx, id, values, secrets); err != nil {
		return err
	}
	err := m.restartPluginLocked(ctx, id)
	if err == nil {
		err = m.store.MarkPluginConfigurationWorking(ctx, id)
	}
	if err == nil {
		return nil
	}
	// Rollback must complete even if the HTTP request was cancelled. Allow
	// shutdown and database work as well as a full replacement handshake.
	recovery, cancel := context.WithTimeout(context.Background(), 2*m.managementReadyTimeout())
	defer cancel()
	m.stopOnePluginLocked(id)
	if rollback := m.store.RollbackPluginConfiguration(recovery, id); rollback != nil {
		return rollback
	}
	if rollback := m.restartPluginLocked(recovery, id); rollback != nil {
		return rollback
	}
	return err
}
