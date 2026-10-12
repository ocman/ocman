package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
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

func (m *Module) handlePluginCatalog(w http.ResponseWriter, r *http.Request) {
	m.servePluginOperation(w, r, remote.PluginRequest{Operation: "catalog"})
}

func (m *Module) handlePluginDiscovery(w http.ResponseWriter, r *http.Request) {
	m.servePluginOperation(w, r, remote.PluginRequest{Operation: "discovery"})
}

func (m *Module) handlePluginRescan(w http.ResponseWriter, r *http.Request) {
	m.servePluginOperation(w, r, remote.PluginRequest{Operation: "rescan"})
}

func (m *Module) handlePluginProjectCatalog(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string `json:"directory"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &input) || input.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	values := map[string]json.RawMessage{"directory": json.RawMessage(strconv.Quote(input.Directory))}
	m.servePluginOperation(w, r, remote.PluginRequest{Operation: "project-catalog", PluginID: "org.ocman.slack", Input: remote.PluginInput{Values: values}})
}

func (m *Module) pluginProjectCatalog(ctx context.Context, input remote.PluginInput) (any, error) {
	var directory string
	if input.Values == nil || json.Unmarshal(input.Values["directory"], &directory) != nil || directory == "" {
		return nil, plugins.ErrInvalidMessage
	}
	ensured, err := m.deps.Local().EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: directory})
	if err != nil || ensured == nil {
		return nil, err
	}
	agents, models, err := opencode.ProjectCatalog(ctx, ensured.Endpoint, directory)
	if err != nil {
		return nil, err
	}
	return struct {
		Agents []string `json:"agents"`
		Models []string `json:"models"`
	}{agents, models}, nil
}

// The action routes have longer mux matches and never pass through this handler.
func (m *Module) handlePluginManagement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, action, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	if !ok || id == "" {
		http.NotFound(w, r)
		return
	}
	read := false
	switch action {
	case "health", "stderr", "conversations":
		read = true
	case "grants", "configuration":
		read = r.Method == http.MethodGet
	case "enable", "disable", "restart", "retry", "configuration/validate", "remove-data",
		// Retrying or discarding a dead letter is a privileged control, not a
		// read: it decides whether a reply reaches a thread. It takes the same
		// localhost-only POST path as every other plugin mutation.
		"conversations/retry", "conversations/discard":
	default:
		http.NotFound(w, r)
		return
	}
	handler := func(w http.ResponseWriter, r *http.Request) { m.managePlugin(w, r, id, action, read) }
	if read {
		if action == "stderr" {
			handler = m.deps.Guards.Localhost(handler)
		}
		get(handler)(w, r)
	} else {
		post(m.deps.Guards.Localhost(handler))(w, r)
	}
}

type pluginManagementInput = remote.PluginInput

func (m *Module) managePlugin(w http.ResponseWriter, r *http.Request, id, action string, read bool) {
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
	m.servePluginOperation(w, r, remote.PluginRequest{Operation: action, PluginID: id, Read: read, Input: *input})
}

func (m *Module) manageLocalPlugin(ctx context.Context, id, action string, read bool, input pluginManagementInput) (any, error) {
	// ponytail: one lifecycle lock includes readiness waits; use per-plugin locks
	// if concurrent management needs to avoid blocking other plugins' admission.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil, plugins.ErrUnavailable
	}
	p, err := m.store.GetPlugin(ctx, id)
	if err != nil {
		// Allow retrying file cleanup after a partially successful deletion.
		if errors.Is(err, state.ErrPluginNotFound) && action == "remove-data" {
			err = m.store.DeletePluginPermanently(ctx, id)
			if err == nil {
				return map[string]bool{"removed": true}, nil
			}
		}
		return nil, err
	}
	if strings.HasPrefix(action, "conversations") {
		return m.manageConversationBacklog(ctx, id, action, input)
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
			text := m.stderr[id]
			if process := m.processes[id]; process != nil {
				text += process.RedactedStderr(func(text string) string { return m.store.RedactPluginDiagnostics(id, text) })
			}
			text = m.store.RedactPluginDiagnostics(id, text)
			return map[string]string{"stderr": text[:min(len(text), 2*plugins.MaxStderrBytes)]}, nil
		}
		return nil, state.ErrPluginInvalid
	}
	if err := m.mutatePluginLocked(ctx, p, action, input); err != nil {
		return nil, err
	}
	if action == "configuration/validate" || action == "remove-data" {
		return map[string]bool{"ok": true}, nil
	}
	return m.store.GetPlugin(ctx, id)
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

func (m *Module) mutatePluginLocked(ctx context.Context, p state.PluginRegistration, action string, input pluginManagementInput) error {
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
			if err := m.store.SetPluginConfiguration(ctx, id, values, nil); err != nil {
				return err
			}
			if err := m.store.SetPluginEnabled(ctx, id, true, *input.Grants); err != nil {
				return err
			}
		} else if !p.Enabled {
			return errPluginConflict
		}
		if err := m.restartPluginLocked(ctx, id); err != nil {
			return err
		}
		return m.store.MarkPluginConfigurationWorking(ctx, id)
	case "disable":
		if err := m.store.SetPluginEnabled(ctx, id, false, nil); err != nil {
			return err
		}
		m.stopOnePluginLocked(id)
		if p.Health.Status == "conflict" {
			return nil
		}
		return m.store.SetPluginHealth(ctx, id, state.PluginHealth{Status: "disabled"})
	case "grants":
		if !approvedPluginGrants(p, input.Grants, false) {
			return state.ErrPluginInvalid
		}
		if err := m.store.SetPluginGrants(ctx, id, *input.Grants); err != nil {
			return err
		}
		// Stop in-flight work before acknowledging revocation. Future admission
		// uses the new durable grants under the same lifecycle lock.
		if p.Enabled {
			return m.restartPluginLocked(ctx, id)
		}
		return nil
	case "configuration", "configuration/validate":
		values, err := validatePluginConfiguration(p, input)
		if err != nil || action == "configuration/validate" {
			return err
		}
		if p.Enabled {
			return m.activatePluginConfigurationLocked(ctx, p, values, input.Secrets)
		}
		return m.store.SetPluginConfiguration(ctx, id, values, input.Secrets)
	case "remove-data":
		if p.Enabled {
			return errPluginConflict
		}
		m.stopOnePluginLocked(id)
		delete(m.stderr, id)
		return m.store.DeletePluginPermanently(ctx, id)
	}
	return state.ErrPluginInvalid
}
