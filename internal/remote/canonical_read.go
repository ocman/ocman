package remote

import "github.com/NoUseFreak/ocman/internal/platforms"

func (s *Server) sessionPlatformFor(platform, id string) (platforms.Platform, error) {
	p, err := s.platformFor(platform)
	if err != nil {
		return nil, err
	}
	if s.registry.CanonicalSessionIDs() {
		if err := validateCanonicalReferences(id, "", true); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (s *Server) eventPlatformFor(platform, id string) (platforms.Platform, error) {
	if id == "" {
		return s.platformFor(platform)
	}
	return s.sessionPlatformFor(platform, id)
}
