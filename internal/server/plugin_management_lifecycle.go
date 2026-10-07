package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Include scheduling slack beyond the supervisor's handshake deadline.
const pluginManagementReadyTimeout = plugins.ReadyTimeout + 5*time.Second

func (s *Server) stopOnePluginLocked(id string) {
	if process := s.pluginProcesses[id]; process != nil {
		process.Stop()
		if s.pluginStderr == nil {
			s.pluginStderr = make(map[string]string)
		}
		s.pluginStderr[id] = process.RedactedStderr(func(text string) string { return s.stateDB.RedactPluginDiagnostics(id, text) })
		delete(s.pluginProcesses, id)
	}
}

func (s *Server) restartPluginLocked(ctx context.Context, id string) error {
	s.stopOnePluginLocked(id)
	if s.pluginCtx == nil || s.pluginCtx.Err() != nil {
		return plugins.ErrUnavailable
	}
	if err := s.stateDB.SetPluginHealth(ctx, id, state.PluginHealth{Status: "starting"}); err != nil {
		return err
	}
	if err := s.syncPluginProcessesLocked(ctx); err != nil {
		return err
	}
	process := s.pluginProcesses[id]
	if process == nil {
		return plugins.ErrUnavailable
	}
	// ponytail: readiness polling uses the supervisor's existing synchronized
	// health; add notifications only if management traffic warrants them.
	deadline := time.NewTimer(pluginManagementReadyTimeout)
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
	s.stopOnePluginLocked(id)
	// Use a fresh context so a disconnected client cannot leave a failed launch
	// retrying indefinitely or lose its terminal health.
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.stateDB.SetPluginHealth(cleanup, id, state.PluginHealth{Status: "unhealthy", LastError: "plugin readiness failed"}); err != nil {
		return err
	}
	return plugins.ErrUnavailable
}

func (s *Server) activatePluginConfigurationLocked(ctx context.Context, p state.PluginRegistration, values map[string]json.RawMessage, secrets map[string]string) error {
	id := p.Description.ID
	process := s.pluginProcesses[id]
	if process == nil || process.Health().Status != "ready" {
		return errPluginConflict
	}
	if err := s.stateDB.MarkPluginConfigurationWorking(ctx, id); err != nil {
		return err
	}
	if err := s.stateDB.SetPluginConfiguration(ctx, id, values, secrets); err != nil {
		return err
	}
	err := s.restartPluginLocked(ctx, id)
	if err == nil {
		err = s.stateDB.MarkPluginConfigurationWorking(ctx, id)
	}
	if err == nil {
		return nil
	}
	// Rollback must complete even if the HTTP request was cancelled. Allow
	// shutdown and database work as well as a full replacement handshake.
	recovery, cancel := context.WithTimeout(context.Background(), 2*pluginManagementReadyTimeout)
	defer cancel()
	s.stopOnePluginLocked(id)
	if rollback := s.stateDB.RollbackPluginConfiguration(recovery, id); rollback != nil {
		return rollback
	}
	if rollback := s.restartPluginLocked(recovery, id); rollback != nil {
		return rollback
	}
	return err
}
