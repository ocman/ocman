package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type pluginDiscoveryFailure struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// RescanPlugins is the shared startup/manual discovery path. When the server is
// running, reconcile approved processes only after the complete scan is recorded.
func (m *Module) RescanPlugins(ctx context.Context) ([]plugins.Discovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir, err := plugins.DiscoveryDirectory()
	if err != nil {
		return nil, err
	}
	var previous []state.PluginRegistration
	known := make(map[string]string)
	if m.store != nil {
		if !m.recovered {
			if err := m.store.RecoverPluginConfigurations(ctx); err != nil {
				return nil, err
			}
			m.recovered = true
		}
		previous, err = m.store.ListPlugins(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range previous {
			known[p.ExecutablePath] = p.Description.ID
		}
	}
	catalog, err := plugins.Scan(ctx, dir, known)
	if err != nil {
		return nil, err
	}
	m.discovery = []pluginDiscoveryFailure{}
	for _, candidate := range catalog {
		if candidate.Err != nil && len(m.discovery) < 128 {
			// Scan errors are fixed host messages, never executable output.
			m.discovery = append(m.discovery, pluginDiscoveryFailure{Filename: filepath.Base(candidate.Path), Error: candidate.Err.Error()})
		}
	}
	if m.store == nil {
		return catalog, nil
	}
	if err := m.recordPluginDiscovery(ctx, catalog, previous, known); err != nil {
		m.stopPluginProcessesLocked()
		return nil, err
	}
	if err := m.syncPluginProcessesLocked(ctx); err != nil {
		return nil, err
	}
	return catalog, nil
}

// SyncPluginProcesses applies durable enable/disable changes. Management callers
// invoke it after updating registration. Rescans also call it; unchanged enabled
// processes, including terminally unhealthy ones, are never started twice.
func (m *Module) SyncPluginProcesses(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.syncPluginProcessesLocked(ctx)
}

func (m *Module) syncPluginProcessesLocked(ctx context.Context) error {
	if m.ctx == nil || m.ctx.Err() != nil || m.store == nil {
		return nil
	}
	registrations, err := m.store.ListPlugins(ctx)
	if err != nil {
		m.stopPluginProcessesLocked()
		return err
	}
	enabled := make(map[string]state.PluginRegistration)
	for _, registration := range registrations {
		if registration.Enabled && !registration.Removed {
			enabled[registration.Description.ID] = registration
		} else if registration.Health.Status != "conflict" {
			if err := m.store.SetPluginHealth(ctx, registration.Description.ID, state.PluginHealth{Status: "disabled"}); err != nil {
				return err
			}
		}
	}
	for id := range m.processes {
		if _, ok := enabled[id]; !ok {
			m.stopOnePluginLocked(id)
		}
	}
	for id, registration := range enabled {
		if m.processes[id] != nil || registration.Health.Status == "unhealthy" {
			continue
		}
		dir, err := m.store.PluginDataDir(ctx, id)
		if err != nil {
			return err
		}
		values := make(map[string]json.RawMessage)
		for key, value := range registration.Configuration.Values {
			values[key] = value
		}
		if registration.Description.WantsCatalog {
			if data := m.launchCatalog(ctx, values[plugins.CatalogProjectSetting]); data != nil {
				values[plugins.CatalogLaunchKey] = data
			}
		}
		var configuration json.RawMessage
		if err := m.store.WithPluginSecrets(ctx, id, func(secrets map[string]string) error {
			for _, setting := range registration.Description.Settings {
				if secret, exists := secrets[setting.Key]; setting.Secret && exists {
					values[setting.Key], _ = json.Marshal(secret)
				}
			}
			configuration, err = json.Marshal(values)
			if err == nil && len(configuration) > plugins.MaxMessageBytes {
				// The catalog is optional: drop it rather than fail startup.
				delete(values, plugins.CatalogLaunchKey)
				configuration, err = json.Marshal(values)
			}
			return err
		}); err != nil {
			return err
		}
		process, err := plugins.StartProcess(m.ctx, plugins.LaunchConfig{
			ReadyTimeout:  m.deps.ReadyTimeout,
			Candidate:     plugins.Discovery{Path: registration.ExecutablePath, Checksum: registration.Checksum, Description: registration.Description},
			Supported:     []plugins.Capability{plugins.ActionCapability, plugins.ConversationCapability, plugins.AssociatedSessionCapability, plugins.PaneCapability, plugins.PageTableCapability, plugins.UICapability},
			DataDir:       dir,
			Configuration: configuration,
			OnHealth: func(h plugins.Health) {
				writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = m.store.SetPluginProcessHealth(writeCtx, id, state.PluginHealth{Status: h.Status, RestartCount: h.RestartCount, LastError: h.LastError})
			},
		})
		if err != nil {
			return err
		}
		if m.processes == nil {
			m.processes = make(map[string]*plugins.Process)
		}
		m.processes[id] = process
		// Events and inbound requests must be drained or the supervisor fails. The
		// context is captured here, under mu, because Stop clears m.ctx.
		m.workers.Go(func(eventCtx context.Context) {
			module.RunWithRecover("plugin-events", func() { m.consumePluginEvents(eventCtx, id, process) })
		})
	}
	return nil
}

// launchCatalog resolves the launch-time catalog snapshot a wantsCatalog serve
// launch receives under the reserved fd-3 "catalog" member, from the local
// instance of the plugin's configured project directory. The catalog is
// optional launch configuration: an unresolved project, no live local instance
// or an oversized payload all yield nil, so the member is simply omitted and
// startup never fails on it.
func (m *Module) launchCatalog(ctx context.Context, raw json.RawMessage) json.RawMessage {
	var project string
	if json.Unmarshal(raw, &project) != nil || project == "" || m.deps.LiveCatalog == nil {
		return nil
	}
	catalogCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	catalog := m.deps.LiveCatalog(catalogCtx, project)
	cancel()
	if catalog == nil {
		return nil
	}
	// Round-trip through the wire DTOs: the shared JSON tags are the
	// contract, and this drops the catalog members a plugin never needs.
	data, err := json.Marshal(catalog)
	if err != nil {
		return nil
	}
	var out plugins.LaunchCatalog
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	data, err = json.Marshal(&out)
	if err != nil || len(data) > plugins.MaxMessageBytes/2 {
		return nil
	}
	return data
}

func (m *Module) stopPluginProcessesLocked() {
	for id := range m.processes {
		m.stopOnePluginLocked(id)
	}
}

func (m *Module) recordPluginDiscovery(ctx context.Context, catalog []plugins.Discovery, previous []state.PluginRegistration, known map[string]string) error {
	byID := make(map[string]plugins.Discovery)
	for _, candidate := range catalog {
		// Do not let a changed identity replace the durable path binding, even
		// when that new identity also conflicts with another executable.
		if id := known[candidate.Path]; id != "" && id != candidate.Description.ID {
			continue
		}
		if candidate.Err == nil || errors.Is(candidate.Err, plugins.ErrDuplicateID) {
			if _, exists := byID[candidate.Description.ID]; !exists {
				byID[candidate.Description.ID] = candidate
			}
		}
	}
	for _, old := range previous {
		if _, exists := byID[old.Description.ID]; exists {
			continue
		}
		if err := m.store.RemovePlugin(ctx, old.Description.ID); err != nil {
			return err
		}
	}
	for id, candidate := range byID {
		if err := m.store.DiscoverPlugin(ctx, candidate.Description, candidate.Path, candidate.Checksum, nil); err != nil {
			return err
		}
		if candidate.Err != nil {
			if err := m.store.SetPluginEnabled(ctx, id, false, nil); err != nil {
				return err
			}
			if err := m.store.SetPluginHealth(ctx, id, state.PluginHealth{Status: "conflict", LastError: plugins.ErrDuplicateID.Error()}); err != nil {
				return err
			}
		} else {
			p, err := m.store.GetPlugin(ctx, id)
			if err != nil {
				return err
			}
			if !p.Enabled {
				if err := m.store.SetPluginHealth(ctx, id, state.PluginHealth{Status: "disabled"}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
