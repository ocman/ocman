package sessionrecovery

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// ReplacementMembership reports which sessions a runtime replacement
// affects. One OpenCode server serves every project on the machine, so a
// replacement affects every session.
func (s *Service) ReplacementMembership(_ context.Context, _ string, sessions []db.Session) (map[string]bool, error) {
	members := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		members[session.Directory] = true
	}
	return members, nil
}
