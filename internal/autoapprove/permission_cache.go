package autoapprove

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Safe verdicts are cached in memory by session and exact request. Unsafe
// verdicts are always re-evaluated. Child sessions may inherit a parent's cache.
func commandHash(metadata map[string]any) string {
	cmd, ok := metadata["command"].(string)
	if !ok || cmd == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("command\x00" + cmd))
	return hex.EncodeToString(sum[:])
}

func permissionHash(permission string, patterns []string, metadata map[string]any) string {
	switch strings.ToLower(permission) {
	case "bash", "bash command":
		return commandHash(metadata)
	}
	payload, err := json.Marshal(struct {
		Permission string         `json:"permission"`
		Patterns   []string       `json:"patterns"`
		Metadata   map[string]any `json:"metadata"`
	}{permission, patterns, metadata})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("permission\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func endpointCacheScope(config JudgeEndpoint) string {
	if config.Format == "" {
		return ""
	}
	encoded, _ := json.Marshal(config)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func scopedPermissionHash(hash, scope string) string {
	if hash == "" || scope == "" {
		return hash
	}
	sum := sha256.Sum256([]byte("judge\x00" + scope + "\x00" + hash))
	return hex.EncodeToString(sum[:])
}

func (s *Service) permissionCacheHash(ctx context.Context, permission string, patterns []string, metadata map[string]any) string {
	config, err := LoadJudgeEndpoint(ctx, s.deps.Store)
	if err != nil {
		return ""
	}
	return scopedPermissionHash(permissionHash(permission, patterns, metadata), endpointCacheScope(config))
}

func (s *Service) lookupSafeCommandVerdict(sessionID, hash string) (string, bool) {
	if s == nil || hash == "" {
		return "", false
	}
	s.safeCommandCacheMu.Lock()
	defer s.safeCommandCacheMu.Unlock()
	bySession, ok := s.safeCommandCache[sessionID]
	if !ok {
		return "", false
	}
	reasoning, ok := bySession[hash]
	return reasoning, ok
}

// ponytail: bounded ancestor walk; eight levels cover normal subagent trees.
const maxParentWalk = 8

func (s *Service) lookupInheritedSafeCommandVerdict(ctx context.Context, sessionID, hash string) (string, bool) {
	if s == nil || hash == "" {
		return "", false
	}
	if reasoning, ok := s.lookupSafeCommandVerdict(sessionID, hash); ok {
		return reasoning, true
	}
	cur := sessionID
	for i := 0; i < maxParentWalk; i++ {
		parent, ok := s.ResolveParentSessionID(ctx, cur)
		if !ok || parent == "" || parent == cur {
			return "", false
		}
		if reasoning, ok := s.lookupSafeCommandVerdict(parent, hash); ok {
			return "inherited from parent: " + reasoning, true
		}
		cur = parent
	}
	return "", false
}

func (s *Service) recordSafeCommandVerdict(sessionID, hash, reasoning string) {
	if s == nil || hash == "" {
		return
	}
	s.safeCommandCacheMu.Lock()
	defer s.safeCommandCacheMu.Unlock()
	if s.safeCommandCache == nil {
		s.safeCommandCache = make(map[string]map[string]string)
	}
	bySession, ok := s.safeCommandCache[sessionID]
	if !ok {
		if len(s.safeCommandCache) >= maxSafeCommandSessions {
			for oldSession := range s.safeCommandCache {
				delete(s.safeCommandCache, oldSession)
				break
			}
		}
		bySession = make(map[string]string)
		s.safeCommandCache[sessionID] = bySession
	}
	if _, exists := bySession[hash]; !exists && len(bySession) >= maxSafeCommandsPerSession {
		for oldHash := range bySession {
			delete(bySession, oldHash)
			break
		}
	}
	bySession[hash] = reasoning
}
