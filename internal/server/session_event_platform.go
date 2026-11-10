package server

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// publicSessionPlatform resolves only persisted provenance. Native kind and
// canonical ID remain the keys used by lifecycle callbacks and internal state.
func (s *Server) publicSessionPlatform(platform, id string) (string, bool) {
	if s == nil || s.registry == nil || id == "" {
		return platform, true
	}
	if s.stateDB != nil {
		catalogue, err := sessioncatalog.New(s.stateDB)
		if err != nil {
			return "", false
		}
		row, err := catalogue.GetCanonical(s.lifetime(), id)
		if err == nil {
			_, key, ok := s.registry.PlatformForCanonicalSource(s.lifetime(), row.OwnerID, row.Platform, row.SourceKey)
			if !ok {
				return s.legacySessionPlatform(platform, row)
			}
			// An already public key must agree with the persisted source too.
			owner, kind := platforms.SplitPlatformID(platform)
			if platform != "" && platform != string(key) && (kind != row.Platform || owner != "" && owner != row.OwnerID) {
				return "", false
			}
			return string(key), true
		}
		if !errors.Is(err, sessioncatalog.ErrNotFound) {
			return "", false
		}
	}
	// Legacy native IDs need no catalogue. Do not use a same-kind connection
	// as a substitute when canonical provenance is missing.
	if s.hasNamedSessionPlatform(platform) {
		return "", false
	}
	return platform, true
}

func eventNativeKind(adapter platforms.Platform) string {
	if native, ok := adapter.(interface{ NativeKind() platforms.ID }); ok {
		return string(native.NativeKind())
	}
	_, kind := platforms.SplitPlatformID(string(adapter.ID()))
	return kind
}

func (s *Server) hasNamedSessionPlatform(platform string) bool {
	owner, kind := platforms.SplitPlatformID(platform)
	for _, entry := range s.registry.Entries() {
		entryOwner, entryKind := platforms.SplitPlatformID(string(entry.Key))
		nativeKind := eventNativeKind(entry.Platform)
		if entryKind != nativeKind && (platform == "" || platform == string(entry.Key) || owner == entryOwner && kind == nativeKind) {
			return true
		}
	}
	return false
}

// Explicit legacy CLI composition has no durable connection source resolver.
// Only its original native registration may retain the old wire identity.
func (s *Server) legacySessionPlatform(platform string, row sessioncatalog.Session) (string, bool) {
	owner, _ := platforms.SplitPlatformID(platform)
	if s.hasNamedSessionPlatform(platforms.CompoundPlatformID(row.OwnerID, row.Platform)) || owner == "" && s.hasNamedSessionPlatform(row.Platform) {
		return "", false
	}
	key := platform
	if key == "" {
		key = row.Platform
	}
	adapter, ok := s.registry.Get(platforms.ID(key))
	keyOwner, keyKind := platforms.SplitPlatformID(key)
	if !ok || keyKind != row.Platform || eventNativeKind(adapter) != row.Platform || keyOwner != "" && keyOwner != row.OwnerID {
		return "", false
	}
	if source, ok := adapter.(interface{ ReadSourceKeys() []string }); ok && !slices.Contains(source.ReadSourceKeys(), row.SourceKey) {
		return "", false
	}
	return key, true
}

// publicSessionEvent normalizes a copy of the wire payload, never callback state.
// The caller must keep the original data for history ingestion and other hooks.
func (s *Server) publicSessionEvent(event string, data []byte) ([]byte, bool) {
	switch event {
	case "ocman.session.changed", "ocman.session.status", "ocman.session.activity", "ocman.session.idle", "ocman.permission.resolved", "ocman.question.resolved":
	default:
		return data, true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return data, true
	}
	var platform, id string
	_ = json.Unmarshal(fields["platform"], &platform)
	_ = json.Unmarshal(fields["sessionID"], &id)
	if id == "" {
		return data, true
	}
	key, ok := s.publicSessionPlatform(platform, id)
	if !ok {
		return nil, false
	}
	if key == "" {
		return data, true
	}
	fields["platform"], _ = json.Marshal(key)
	for _, field := range []string{"session", "patch"} {
		var row map[string]json.RawMessage
		if json.Unmarshal(fields[field], &row) != nil || row == nil {
			continue
		}
		if _, exists := row["platform"]; exists {
			row["platform"] = fields["platform"]
			fields[field], _ = json.Marshal(row)
		}
	}
	data, _ = json.Marshal(fields)
	return data, true
}
