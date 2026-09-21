package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

type pluginDiscoveryFailure struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// RescanPlugins is the shared startup/manual discovery path. When the server is
// running, reconcile approved processes only after the complete scan is recorded.
func (s *Server) RescanPlugins(ctx context.Context) ([]plugins.Discovery, error) {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	dir, err := plugins.DiscoveryDirectory()
	if err != nil {
		return nil, err
	}
	var previous []state.PluginRegistration
	known := make(map[string]string)
	if s.stateDB != nil {
		if !s.pluginRecovered {
			if err := s.stateDB.RecoverPluginConfigurations(ctx); err != nil {
				return nil, err
			}
			s.pluginRecovered = true
		}
		previous, err = s.stateDB.ListPlugins(ctx)
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
	s.pluginDiscovery = []pluginDiscoveryFailure{}
	for _, candidate := range catalog {
		if candidate.Err != nil && len(s.pluginDiscovery) < 128 {
			// Scan errors are fixed host messages, never executable output.
			s.pluginDiscovery = append(s.pluginDiscovery, pluginDiscoveryFailure{Filename: filepath.Base(candidate.Path), Error: candidate.Err.Error()})
		}
	}
	if s.stateDB == nil {
		return catalog, nil
	}
	if err := s.recordPluginDiscovery(ctx, catalog, previous, known); err != nil {
		s.stopPluginProcessesLocked()
		return nil, err
	}
	if err := s.syncPluginProcessesLocked(ctx); err != nil {
		return nil, err
	}
	return catalog, nil
}

// SyncPluginProcesses applies durable enable/disable changes. Management callers
// invoke it after updating registration. Rescans also call it; unchanged enabled
// processes, including terminally unhealthy ones, are never started twice.
func (s *Server) SyncPluginProcesses(ctx context.Context) error {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	return s.syncPluginProcessesLocked(ctx)
}

func (s *Server) syncPluginProcessesLocked(ctx context.Context) error {
	if s.pluginCtx == nil || s.pluginCtx.Err() != nil || s.stateDB == nil {
		return nil
	}
	registrations, err := s.stateDB.ListPlugins(ctx)
	if err != nil {
		s.stopPluginProcessesLocked()
		return err
	}
	enabled := make(map[string]state.PluginRegistration)
	for _, registration := range registrations {
		if registration.Enabled && !registration.Removed {
			enabled[registration.Description.ID] = registration
		} else if registration.Health.Status != "conflict" {
			if err := s.stateDB.SetPluginHealth(ctx, registration.Description.ID, state.PluginHealth{Status: "disabled"}); err != nil {
				return err
			}
		}
	}
	for id := range s.pluginProcesses {
		if _, ok := enabled[id]; !ok {
			s.stopOnePluginLocked(id)
		}
	}
	for id, registration := range enabled {
		if s.pluginProcesses[id] != nil || registration.Health.Status == "unhealthy" {
			continue
		}
		dir, err := s.stateDB.PluginDataDir(ctx, id)
		if err != nil {
			return err
		}
		values := make(map[string]json.RawMessage)
		for key, value := range registration.Configuration.Values {
			values[key] = value
		}
		var configuration json.RawMessage
		if err := s.stateDB.WithPluginSecrets(ctx, id, func(secrets map[string]string) error {
			for _, setting := range registration.Description.Settings {
				if secret, exists := secrets[setting.Key]; setting.Secret && exists {
					values[setting.Key], _ = json.Marshal(secret)
				}
			}
			configuration, err = json.Marshal(values)
			return err
		}); err != nil {
			return err
		}
		process, err := plugins.StartProcess(s.pluginCtx, plugins.LaunchConfig{
			Candidate:     plugins.Discovery{Path: registration.ExecutablePath, Checksum: registration.Checksum, Description: registration.Description},
			Supported:     []plugins.Capability{plugins.ActionCapability, plugins.ConversationCapability},
			DataDir:       dir,
			Configuration: configuration,
			OnHealth: func(h plugins.Health) {
				writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = s.stateDB.SetPluginProcessHealth(writeCtx, id, state.PluginHealth{Status: h.Status, RestartCount: h.RestartCount, LastError: h.LastError})
			},
		})
		if err != nil {
			return err
		}
		if s.pluginProcesses == nil {
			s.pluginProcesses = make(map[string]*plugins.Process)
		}
		s.pluginProcesses[id] = process
		// Events must be drained or the supervisor fails the process. The
		// context is captured here, under pluginMu, because stopPluginProcesses
		// clears s.pluginCtx.
		eventCtx := context.WithoutCancel(s.pluginCtx)
		go runWithRecover("plugin-events", func() { s.consumePluginEvents(eventCtx, id, process) })
	}
	return nil
}

func (s *Server) stopPluginProcessesLocked() {
	for id := range s.pluginProcesses {
		s.stopOnePluginLocked(id)
	}
}

func (s *Server) stopPluginProcesses() {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	s.pluginCtx = nil
	s.stopPluginProcessesLocked()
}

func (s *Server) recordPluginDiscovery(ctx context.Context, catalog []plugins.Discovery, previous []state.PluginRegistration, known map[string]string) error {
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
		if err := s.stateDB.RemovePlugin(ctx, old.Description.ID); err != nil {
			return err
		}
	}
	for id, candidate := range byID {
		if err := s.stateDB.DiscoverPlugin(ctx, candidate.Description, candidate.Path, candidate.Checksum, nil); err != nil {
			return err
		}
		if candidate.Err != nil {
			if err := s.stateDB.SetPluginEnabled(ctx, id, false, nil); err != nil {
				return err
			}
			if err := s.stateDB.SetPluginHealth(ctx, id, state.PluginHealth{Status: "conflict", LastError: plugins.ErrDuplicateID.Error()}); err != nil {
				return err
			}
		} else {
			p, err := s.stateDB.GetPlugin(ctx, id)
			if err != nil {
				return err
			}
			if !p.Enabled {
				if err := s.stateDB.SetPluginHealth(ctx, id, state.PluginHealth{Status: "disabled"}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
