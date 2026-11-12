package server

import (
	"context"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
)

// prToolSessions deliberately avoids the recent-session cap and best-effort
// fan-out: neither truncation nor a failed owner read can establish uniqueness.
func (s *Server) prToolSessions(ctx context.Context, directory, platform, owner string) ([]prworkflow.Target, error) {
	if owner != "" && owner != "local" {
		if _, ok := s.router().LookupRemote(owner); !ok {
			return nil, platforms.ErrRemoteOffline
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var matches []prworkflow.Target
	found := false
	for _, entry := range s.registry.Entries() {
		key := string(entry.Key)
		remote, _ := platforms.SplitPlatformID(key)
		if remote == "" {
			remote = "local"
		}
		if (platform != "" && key != platform) || (owner != "" && remote != owner) {
			continue
		}
		found = true
		rows, err := entry.Platform.Sessions(ctx, directory, 0)
		if err != nil {
			return nil, fmt.Errorf("reading session candidates: %w", err)
		}
		for _, row := range rows {
			if row.Directory == directory && row.ID != "" {
				matches = append(matches, prworkflow.Target{Platform: key, SessionID: row.ID, Directory: row.Directory, RemoteID: remote})
			}
		}
	}
	if !found && (platform != "" || owner != "") {
		return nil, platforms.ErrPlatformUnreachable
	}
	return matches, nil
}
