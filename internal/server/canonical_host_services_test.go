package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type canonicalHostProvider struct {
	*fakePlatform
	available bool
}

func (p *canonicalHostProvider) HostServices() hostsvc.PlatformServices {
	return hostsvc.PlatformServices{Runtime: p, Idle: idleProvider{}}
}
func (p *canonicalHostProvider) RuntimeAvailable() bool { return p.available }
func (*canonicalHostProvider) EnsureRuntime(context.Context, string) (*hostsvc.EnsureRuntimeResult, error) {
	return nil, nil
}

// Expose only the canonical adapter's service-provider interface, not the
// native RuntimeEnsurer used to implement it.
type canonicalHostFacade struct {
	platforms.Platform
	provider *canonicalHostProvider
}

func (p canonicalHostFacade) HostServices() hostsvc.PlatformServices {
	return p.provider.HostServices()
}
func (p canonicalHostFacade) RuntimeAvailable() bool { return p.provider.RuntimeAvailable() }

func TestCanonicalHostServicesKeepLaunchCapabilitiesAndIdleCulling(t *testing.T) {
	p := &canonicalHostProvider{fakePlatform: &fakePlatform{id: "claudecode"}}
	registry := platforms.NewRegistry()
	registry.Register(canonicalHostFacade{Platform: p.fakePlatform, provider: p})
	s := &Server{registry: registry}
	if s.providerRuntime() {
		t.Fatal("unavailable runtime enabled managed launch")
	}
	p.available = true
	if !s.providerRuntime() {
		t.Fatal("canonical provider lost managed launch capability")
	}
	targets := s.idleProjectTargets()
	if len(targets) != 1 || targets[0].Platform.ID() != p.ID() || targets[0].Runtime == nil {
		t.Fatalf("canonical provider lost idle runtime: %+v", targets)
	}
}
