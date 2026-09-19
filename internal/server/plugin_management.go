package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

var errPluginConflict = errors.New("plugin lifecycle conflict")

func pluginManagementError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "plugin management failed"
	switch {
	case errors.Is(err, state.ErrPluginNotFound):
		status, message = http.StatusNotFound, "plugin not found"
	case errors.Is(err, state.ErrPluginInvalid), errors.Is(err, plugins.ErrInvalidMessage):
		status, message = http.StatusBadRequest, "invalid plugin configuration or grants"
	case errors.Is(err, errPluginConflict):
		status, message = http.StatusConflict, errPluginConflict.Error()
	case errors.Is(err, plugins.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, "plugin readiness failed"
	}
	http.Error(w, message, status)
}

func (s *Server) handlePluginCatalog(w http.ResponseWriter, r *http.Request) {
	s.servePluginOperation(w, r, remote.PluginRequest{Operation: "catalog"})
}

func (s *Server) handlePluginDiscovery(w http.ResponseWriter, r *http.Request) {
	s.servePluginOperation(w, r, remote.PluginRequest{Operation: "discovery"})
}

func (s *Server) handlePluginRescan(w http.ResponseWriter, r *http.Request) {
	s.servePluginOperation(w, r, remote.PluginRequest{Operation: "rescan"})
}

// The action routes have longer mux matches and never pass through this handler.
func (s *Server) handlePluginManagement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, action, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	if !ok || id == "" {
		http.NotFound(w, r)
		return
	}
	read := false
	switch action {
	case "health", "stderr":
		read = true
	case "grants", "configuration":
		read = r.Method == http.MethodGet
	case "enable", "disable", "restart", "retry", "configuration/validate", "remove-data":
	default:
		http.NotFound(w, r)
		return
	}
	handler := func(w http.ResponseWriter, r *http.Request) { s.managePlugin(w, r, id, action, read) }
	if read {
		if action == "stderr" {
			handler = s.requireLocalhost(handler)
		}
		requireGET(handler)(w, r)
	} else {
		requirePOST(s.requireLocalhost(handler))(w, r)
	}
}

type pluginManagementInput = remote.PluginInput

func (s *Server) managePlugin(w http.ResponseWriter, r *http.Request, id, action string, read bool) {
	var input *pluginManagementInput
	if !read {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, plugins.MaxMessageBytes))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input == nil || decoder.Decode(new(any)) != io.EOF {
			pluginManagementError(w, state.ErrPluginInvalid)
			return
		}
	} else {
		input = &pluginManagementInput{}
	}
	s.servePluginOperation(w, r, remote.PluginRequest{Operation: action, PluginID: id, Read: read, Input: *input})
}

func (s *Server) manageLocalPlugin(ctx context.Context, id, action string, read bool, input pluginManagementInput) (any, error) {
	// ponytail: one lifecycle lock includes readiness waits; use per-plugin locks
	// if concurrent management needs to avoid blocking other plugins' admission.
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if s.stateDB == nil {
		return nil, plugins.ErrUnavailable
	}
	p, err := s.stateDB.GetPlugin(ctx, id)
	if err != nil {
		// Allow retrying file cleanup after a partially successful deletion.
		if errors.Is(err, state.ErrPluginNotFound) && action == "remove-data" {
			err = s.stateDB.DeletePluginPermanently(ctx, id)
			if err == nil {
				return map[string]bool{"removed": true}, nil
			}
		}
		return nil, err
	}
	if read {
		switch action {
		case "health":
			return p.Health, nil
		case "grants":
			return map[string]any{"requested": p.Description.RequestedGrants, "approved": p.Grants}, nil
		case "configuration":
			return p.Configuration, nil
		case "stderr":
			text := s.pluginStderr[id]
			if process := s.pluginProcesses[id]; process != nil {
				text += process.RedactedStderr(func(text string) string { return s.stateDB.RedactPluginDiagnostics(id, text) })
			}
			text = s.stateDB.RedactPluginDiagnostics(id, text)
			return map[string]string{"stderr": text[:min(len(text), 2*plugins.MaxStderrBytes)]}, nil
		}
		return nil, state.ErrPluginInvalid
	}
	if err := s.mutatePluginLocked(ctx, p, action, input); err != nil {
		return nil, err
	}
	if action == "configuration/validate" || action == "remove-data" {
		return map[string]bool{"ok": true}, nil
	}
	return s.stateDB.GetPlugin(ctx, id)
}

func validatePluginConfiguration(p state.PluginRegistration, input pluginManagementInput) (map[string]json.RawMessage, error) {
	values := make(map[string]json.RawMessage)
	known := make(map[string]bool)
	for _, setting := range p.Description.Settings {
		known[setting.Key] = true
		if setting.Secret {
			if _, exists := input.Values[setting.Key]; exists {
				return nil, state.ErrPluginInvalid
			}
			secret, updated := input.Secrets[setting.Key]
			present := p.Configuration.Secrets[setting.Key]
			if updated {
				present = secret != ""
				raw, _ := json.Marshal(secret)
				if present && !setting.Accepts(raw) {
					return nil, state.ErrPluginInvalid
				}
			}
			if setting.Required && !present {
				return nil, state.ErrPluginInvalid
			}
			continue
		}
		if _, exists := input.Secrets[setting.Key]; exists {
			return nil, state.ErrPluginInvalid
		}
		value, exists := input.Values[setting.Key]
		if !exists {
			value = setting.Default
		}
		if len(value) == 0 {
			if setting.Required {
				return nil, state.ErrPluginInvalid
			}
			continue
		}
		if !setting.Accepts(value) {
			return nil, state.ErrPluginInvalid
		}
		values[setting.Key] = value
	}
	for key := range input.Values {
		if !known[key] {
			return nil, state.ErrPluginInvalid
		}
	}
	for key := range input.Secrets {
		if !known[key] {
			return nil, state.ErrPluginInvalid
		}
	}
	return values, nil
}

func approvedPluginGrants(p state.PluginRegistration, grants *[]string, all bool) bool {
	if grants == nil {
		return false
	}
	seen := make(map[string]bool)
	for _, grant := range *grants {
		if seen[grant] || !slices.Contains(p.Description.RequestedGrants, grant) {
			return false
		}
		seen[grant] = true
	}
	return !all || len(seen) == len(p.Description.RequestedGrants)
}

func (s *Server) mutatePluginLocked(ctx context.Context, p state.PluginRegistration, action string, input pluginManagementInput) error {
	id := p.Description.ID
	switch action {
	case "enable", "restart", "retry":
		if p.Removed || p.Health.Status == "conflict" {
			return errPluginConflict
		}
		if action == "enable" {
			if !approvedPluginGrants(p, input.Grants, true) {
				return state.ErrPluginInvalid
			}
			if input.Approval == "" || input.Approval != p.Approval {
				return errPluginConflict
			}
			values, err := validatePluginConfiguration(p, pluginManagementInput{Values: p.Configuration.Values})
			if err != nil {
				return err
			}
			if err := s.stateDB.SetPluginConfiguration(ctx, id, values, nil); err != nil {
				return err
			}
			if err := s.stateDB.SetPluginEnabled(ctx, id, true, *input.Grants); err != nil {
				return err
			}
		} else if !p.Enabled {
			return errPluginConflict
		}
		if err := s.restartPluginLocked(ctx, id); err != nil {
			return err
		}
		return s.stateDB.MarkPluginConfigurationWorking(ctx, id)
	case "disable":
		if err := s.stateDB.SetPluginEnabled(ctx, id, false, nil); err != nil {
			return err
		}
		s.stopOnePluginLocked(id)
		if p.Health.Status == "conflict" {
			return nil
		}
		return s.stateDB.SetPluginHealth(ctx, id, state.PluginHealth{Status: "disabled"})
	case "grants":
		if !approvedPluginGrants(p, input.Grants, false) {
			return state.ErrPluginInvalid
		}
		if err := s.stateDB.SetPluginGrants(ctx, id, *input.Grants); err != nil {
			return err
		}
		// Stop in-flight work before acknowledging revocation. Future admission
		// uses the new durable grants under the same lifecycle lock.
		if p.Enabled {
			return s.restartPluginLocked(ctx, id)
		}
		return nil
	case "configuration", "configuration/validate":
		values, err := validatePluginConfiguration(p, input)
		if err != nil || action == "configuration/validate" {
			return err
		}
		if p.Enabled {
			return s.activatePluginConfigurationLocked(ctx, p, values, input.Secrets)
		}
		return s.stateDB.SetPluginConfiguration(ctx, id, values, input.Secrets)
	case "remove-data":
		if p.Enabled {
			return errPluginConflict
		}
		s.stopOnePluginLocked(id)
		delete(s.pluginStderr, id)
		return s.stateDB.DeletePluginPermanently(ctx, id)
	}
	return state.ErrPluginInvalid
}

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
	deadline := time.NewTimer(4 * time.Second)
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
	// Rollback must complete even if the HTTP request was cancelled.
	recovery, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
