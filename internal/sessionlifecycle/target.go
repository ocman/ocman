package sessionlifecycle

import (
	"context"
	"net/http"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

type Target struct {
	Platform  string `json:"platform"`
	Directory string `json:"directory"`
	RemoteID  string `json:"remoteId"`
}

// ResolvePlatform treats an explicit owner as authoritative, even when machines share a path.
func (t *Target) ResolvePlatform(ctx context.Context, m *Module, w http.ResponseWriter, model string) bool {
	if t.RemoteID == "" {
		t.RemoteID = remoteIDForPlatform(t.Platform)
	}
	if t.Platform != "" && remoteIDForPlatform(t.Platform) != t.RemoteID {
		http.Error(w, "platform does not belong to remoteId", http.StatusBadRequest)
		return false
	}
	host, ok := m.d.Owner(w, t.Directory, t.RemoteID)
	if !ok {
		return false
	}
	var err error
	t.Platform, _, err = m.d.CreatePlatform(ctx, host, t.Platform, model)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	if !m.d.Sessions().KnownPlatform(t.Platform) {
		http.Error(w, "unknown platform", http.StatusBadRequest)
		return false
	}
	return true
}

func (t Target) Validate(w http.ResponseWriter) bool {
	if t.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return false
	}
	if !filepath.IsAbs(t.Directory) {
		http.Error(w, "directory must be an absolute path", http.StatusBadRequest)
		return false
	}
	return true
}

func remoteIDForPlatform(platform string) string {
	if remoteID, _ := platforms.SplitPlatformID(platform); remoteID != "" {
		return remoteID
	}
	return "local"
}

func (m *Module) draftOwner(w http.ResponseWriter, platform, directory string) (hostsvc.Host, bool) {
	host, ok := m.d.Owner(w, directory, remoteIDForPlatform(platform))
	if !ok {
		return nil, false
	}
	// Reject unknown platforms before launching a managed instance.
	if !m.d.Sessions().KnownPlatform(platform) {
		http.Error(w, "unknown platform", http.StatusBadRequest)
		return nil, false
	}
	return host, true
}

// EnsureProjectForCreate launches on the platform's owner, folding worktrees to their project root.
// Local OpenCode ensure failures retain the existing port-discovery fallback.
func (m *Module) EnsureProjectForCreate(w http.ResponseWriter, r *http.Request, platform, directory string) (hostsvc.Host, string, bool) {
	remoteID, base := platforms.SplitPlatformID(platform)
	host, ok := m.draftOwner(w, platform, directory)
	if !ok {
		return nil, "", false
	}
	ensured, err := host.EnsureRuntime(r.Context(), base, state.ProjectRootForDirectory(directory))
	fields := log.Fields{"platform": platform, "directory": directory}
	switch {
	case err != nil && (remoteID != "" || base != string(platforms.OpenCodeID)):
		log.WithError(err).WithFields(fields).Warn("hub: ensure project opencode failed")
		writeSessionError(w, "creating session", err)
		return nil, "", false
	case err != nil:
		log.WithError(err).WithFields(fields).Debug("hub: ensure project opencode failed; falling back to port discovery")
		return host, "", true
	default:
		return host, ensured.Port(), true
	}
}
