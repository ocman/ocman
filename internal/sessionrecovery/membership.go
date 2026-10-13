package sessionrecovery

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
)

// Membership uses the same owner-local identity as managed launch. Each
// distinct directory is resolved once per baseline or reconciliation scan.
func (s *Service) ReplacementMembership(ctx context.Context, root string, sessions []db.Session) (map[string]bool, error) {
	members := make(map[string]bool)
	if s.deps.MachineWide() {
		for _, session := range sessions {
			members[session.Directory] = true
		}
		return members, nil
	}
	reader := s.deps.ManagedRootReader()
	if reader == nil {
		return nil, fmt.Errorf("owner does not support managed project identity")
	}
	for _, session := range sessions {
		if _, resolved := members[session.Directory]; resolved {
			continue
		}
		managedRoot, err := reader.ManagedOpencodeRoot(ctx, session.Directory)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && !errors.Is(err, git.ErrNotARepo) && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("resolving managed project for %q: %w", session.Directory, err)
		}
		members[session.Directory] = err == nil && managedRoot == root
	}
	return members, nil
}
