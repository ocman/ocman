package server

import (
	"context"
	"sort"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectforge"
)

func (s *Server) prLinkedSessions(ctx context.Context, owner, host, repo string) (map[int][]projectforge.SessionRef, error) {
	result := make(map[int][]projectforge.SessionRef)
	for _, p := range s.registry.Platforms() {
		key, ok := s.registry.KeyFor(p)
		if !ok {
			continue
		}
		remote, _ := platforms.SplitPlatformID(string(key))
		if remote == "" {
			remote = "local"
		}
		if remote != owner {
			continue
		}
		links, err := s.prWorkflowModule().LinkedSessions(ctx, string(key), host, repo)
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			result[link.Number] = append(result[link.Number], projectforge.SessionRef{Platform: link.Platform, SessionID: link.SessionID})
		}
	}
	for _, refs := range result {
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Platform != refs[j].Platform {
				return refs[i].Platform < refs[j].Platform
			}
			return refs[i].SessionID < refs[j].SessionID
		})
	}
	return result, nil
}
