package sessionsvc

import (
	"github.com/ocman/ocman/internal/platforms"
	"strings"
)

func (s *Service) modelForAdapter(p platforms.Platform, ref string) (string, error) {
	kind, key := p.ID().Base(), p.ID()
	if native, ok := p.(interface{ NativeKind() platforms.ID }); ok {
		kind = native.NativeKind()
	}
	if prefix, _ := platforms.SplitModel(ref); !strings.HasPrefix(string(prefix), "cnx_") {
		return platforms.ModelFor(kind, ref)
	}
	if registry, ok := s.registry.(interface {
		KeyFor(platforms.Platform) (platforms.ID, bool)
	}); ok {
		if selected, found := registry.KeyFor(p); found {
			key = selected
		}
	}
	return platforms.ModelForConnection(key, kind, ref)
}
