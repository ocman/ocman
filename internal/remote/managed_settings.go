package remote

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/state"
)

// Hub-side results of sending settings to a remote, reported in
// RemoteStatus.ManagedSettings.
const (
	ManagedSettingsApplied = "applied"
	// ManagedSettingsNotManaged: the remote runs without -remote-managed
	// and keeps its own settings.
	ManagedSettingsNotManaged = "not-managed"
	// ManagedSettingsUnsupported: the remote predates managed settings.
	ManagedSettingsUnsupported = "unsupported"
	ManagedSettingsFailed      = "failed"
)

const managedSettingsTimeout = 10 * time.Second

// ManagedSettingsStore is the owner-side settings layer of a managed remote.
type ManagedSettingsStore interface {
	ManagedSettings() (state.ManagedSettings, bool)
	ApplyManagedSettings(context.Context, state.ManagedSettings, int64) error
}

// UseManagedSettings lets hubs send settings to this instance. The store
// refuses them unless the instance runs in remote-managed mode.
func (s *Server) UseManagedSettings(store ManagedSettingsStore) *Server {
	s.managedSettings = store
	return s
}

// ApplyManagedSettings stores a hub's settings snapshot on a managed remote.
func (s *Server) ApplyManagedSettings(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.managedSettings == nil {
		return nil, status.Error(codes.Unimplemented, "managed settings unavailable")
	}
	var snapshot state.ManagedSettings
	if len(req.Payload) > 1<<20 || json.Unmarshal(req.Payload, &snapshot) != nil || snapshot.Hub == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid managed settings")
	}
	previous, hadPrevious := s.managedSettings.ManagedSettings()
	err := s.managedSettings.ApplyManagedSettings(ctx, snapshot, time.Now().UnixMilli())
	if errors.Is(err, state.ErrNotManaged) {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "storing managed settings")
	}
	// Not enforced: several hubs work, but each overwrites the others'
	// settings, so the last one to connect or change a setting wins.
	if hadPrevious && previous.Hub != snapshot.Hub {
		log.WithFields(log.Fields{"previousHub": previous.Hub, "hub": snapshot.Hub}).
			Warn("remote: managed settings replaced by a different hub; use a single hub per managed remote")
	}
	return jsonResp(struct{}{}, nil)
}

// UseManagedSettings supplies the snapshot this hub sends to its remotes.
// Set before Start; nil sends nothing.
func (m *Manager) UseManagedSettings(snapshot func(context.Context) (state.ManagedSettings, error)) {
	m.managedSettings = snapshot
}

// PushManagedSettings resends the current snapshot to every connected
// remote, in the background. Call after a managed setting changes.
func (m *Manager) PushManagedSettings() {
	if m.managedSettings == nil {
		return
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.stopped {
		return
	}
	ctx := m.baseCtx
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mr := range m.remotes {
		if mr.conn.Client() == nil {
			continue
		}
		m.connectors.Add(1)
		go func() {
			defer m.connectors.Done()
			m.pushManagedSettings(ctx, mr)
		}()
	}
}

// pushManagedSettings sends one snapshot. The per-remote lock serializes
// pushes and the snapshot is read under it, so a slow older push can never
// land after a newer one.
func (m *Manager) pushManagedSettings(ctx context.Context, mr *managedRemote) {
	if m.managedSettings == nil {
		return
	}
	mr.settingsMu.Lock()
	defer mr.settingsMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, managedSettingsTimeout)
	defer cancel()
	snapshot, err := m.managedSettings(ctx)
	if err != nil {
		log.WithError(err).Warn("remote: reading managed settings")
		mr.settingsStatus.Store(ManagedSettingsFailed)
		return
	}
	_, err = mr.conn.call(ctx, &pb.CallReq{Method: "ApplyManagedSettings"}, snapshot)
	result := ManagedSettingsApplied
	switch status.Code(err) {
	case codes.OK:
	case codes.FailedPrecondition:
		result = ManagedSettingsNotManaged
	case codes.Unimplemented:
		result = ManagedSettingsUnsupported
	default:
		result = ManagedSettingsFailed
		log.WithError(err).WithField("remote", mr.localID).Warn("remote: sending managed settings")
	}
	mr.settingsStatus.Store(result)
}

func (mr *managedRemote) managedSettingsStatus() string {
	value, _ := mr.settingsStatus.Load().(string)
	return value
}
