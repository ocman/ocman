package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const (
	autoArchiveSettingKey     = "auto_archive"
	defaultAutoArchiveTTLDays = 7
	maximumAutoArchiveTTLDays = 3650
)

type autoArchiveSettingsView struct {
	Enabled bool `json:"enabled"`
	TTLDays int  `json:"ttlDays"`
}

// remoteAccessStatus is the GET /api/settings/remote-access response. It
// describes this instance's own remote-access surface so the operator can
// copy the connection details into a hub. The token itself is never
// included; the masked indicator + explicit reveal action handle that.
type remoteAccessStatus struct {
	InstanceID string `json:"instanceId"`
	Listening  bool   `json:"listening"`
	ListenAddr string `json:"listenAddr"`
	TLS        bool   `json:"tls"`
	Transport  string `json:"transport"`
	TokenSet   bool   `json:"tokenSet"`
}

// handleRemoteAccess handles GET /api/settings/remote-access — this
// instance's identity and gRPC-listen status. Auth-gated; no plaintext
// token (AD-5).
func (s *Server) handleRemoteAccess(w http.ResponseWriter, r *http.Request) {
	status := remoteAccessStatus{
		InstanceID: s.remoteAccess.instanceID,
		Listening:  s.remoteAccess.listening,
		ListenAddr: s.remoteAccess.listenAddr,
		TLS:        s.remoteAccess.tls,
	}
	if status.Listening {
		status.Transport = "trusted-overlay"
		if status.TLS {
			status.Transport = "tls"
		}
	}
	if s.stateDB != nil {
		if ident, err := s.stateDB.InstanceIdentity(r.Context()); err == nil {
			if status.InstanceID == "" {
				status.InstanceID = ident.InstanceID
			}
			status.TokenSet = ident.RemoteToken != ""
		}
	}
	writeJSON(w, status)
}

// handleRevealRemoteToken handles POST /api/settings/remote-access/reveal-token
// — the explicit, authenticated action that returns this instance's own
// plaintext remote-access token for copy-to-clipboard. It never returns
// tokens stored for attached remotes (AD-5, NFR-4).
func (s *Server) handleRevealRemoteToken(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	ident, err := s.stateDB.InstanceIdentity(r.Context())
	if err != nil {
		serverError(w, "reading instance identity", err)
		return
	}
	writeJSON(w, map[string]string{"token": ident.RemoteToken})
}

// handleWorktreeInheritPermissions handles GET and POST
// /api/settings/worktree-inherit-permissions (issue #101).
//
// GET  → {"enabled": bool}. Defaults to enabled when unset.
// POST → accepts {"enabled": bool}, persists, returns the new state.
func (s *Server) handleWorktreeInheritPermissions(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		on, err := s.stateDB.GetWorktreeInheritPermissions(r.Context())
		if err != nil {
			serverError(w, "reading worktree inherit-permissions setting", err)
			return
		}
		writeJSON(w, map[string]bool{"enabled": on})
	case http.MethodPost:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		if err := s.stateDB.SetWorktreeInheritPermissions(r.Context(), body.Enabled); err != nil {
			serverError(w, "saving worktree inherit-permissions setting", err)
			return
		}
		writeJSON(w, map[string]bool{"enabled": body.Enabled})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAutoArchiveSettings(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		settings, err := s.getAutoArchiveSettings(r.Context())
		if err != nil {
			serverError(w, "reading auto-archive settings", err)
			return
		}
		writeJSON(w, settings)
	case http.MethodPost:
		var settings autoArchiveSettingsView
		if !readAndUnmarshal(w, r, maxRequestBody, &settings) {
			return
		}
		if err := validateAutoArchiveSettings(settings); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.setAutoArchiveSettings(r.Context(), settings); err != nil {
			serverError(w, "saving auto-archive settings", err)
			return
		}
		writeJSON(w, settings)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getAutoArchiveSettings(ctx context.Context) (autoArchiveSettingsView, error) {
	settings := autoArchiveSettingsView{Enabled: true, TTLDays: defaultAutoArchiveTTLDays}
	if s.stateDB == nil {
		return settings, errors.New("state database not available")
	}
	value, ok, err := s.stateDB.GetSetting(ctx, autoArchiveSettingKey)
	if err != nil || !ok {
		return settings, err
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, fmt.Errorf("decode auto-archive settings: %w", err)
	}
	if err := validateAutoArchiveSettings(settings); err != nil {
		return settings, err
	}
	return settings, nil
}

func (s *Server) setAutoArchiveSettings(ctx context.Context, settings autoArchiveSettingsView) error {
	if err := validateAutoArchiveSettings(settings); err != nil {
		return err
	}
	value, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.stateDB.SetSetting(ctx, autoArchiveSettingKey, string(value))
}

func validateAutoArchiveSettings(settings autoArchiveSettingsView) error {
	if settings.TTLDays < 1 || settings.TTLDays > maximumAutoArchiveTTLDays {
		return fmt.Errorf("ttlDays must be between 1 and %d", maximumAutoArchiveTTLDays)
	}
	return nil
}
