package owned

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

type hostNative struct {
	*nativePlatform
	hostsvc.PlatformHooks
	hostsvc.IdleProjectRuntime
	addr    string
	handler func(providerprotocol.Event)
	events  []providerprotocol.Event
}

func (p *hostNative) SetMCPAddr(addr string) { p.addr = addr }
func (*hostNative) RuntimeAvailable() bool   { return false }
func (*hostNative) EnsureRuntime(context.Context, string) (*hostsvc.EnsureRuntimeResult, error) {
	return &hostsvc.EnsureRuntimeResult{Endpoint: "unix://provider"}, nil
}
func (*hostNative) WorktreeName(context.Context, string, string) (string, error) {
	return "worktree", nil
}
func (p *hostNative) SetEventHandler(handler func(providerprotocol.Event)) { p.handler = handler }
func (p *hostNative) WatchEvents(context.Context) error {
	for _, event := range p.events {
		p.handler(event)
	}
	return nil
}

func TestCanonicalHostCapabilitiesAndProviderEventsSurviveComposition(t *testing.T) {
	a, p, _ := fixture(t)
	native := &hostNative{nativePlatform: p}
	a.native = native
	a.SetMCPAddr("127.0.0.1:1234")
	services := a.HostServices()
	if native.addr != "127.0.0.1:1234" || a.RuntimeAvailable() || services.Runtime == nil || services.Namer == nil || services.Hooks == nil || services.Idle == nil {
		t.Fatal("host composition lost optional services")
	}
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	native.events = []providerprotocol.Event{
		{Type: "permission.asked", Properties: json.RawMessage(`{"sessionID":"native","id":"native-permission","tool":"shell"}`)},
		{Type: "permission.asked", Properties: json.RawMessage(`{"sessionID":"unattributed"}`)},
		{Type: "permission.asked", Properties: json.RawMessage(`invalid`)},
	}
	var received []providerprotocol.Event
	a.SetEventHandler(func(event providerprotocol.Event) { received = append(received, event) })
	if err := a.WatchEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 {
		t.Fatalf("invalid/unattributed event escaped: %+v", received)
	}
	var props map[string]string
	if err := json.Unmarshal(received[0].Properties, &props); err != nil || props["sessionID"] != row.ID || props["id"] != "native-permission" {
		t.Fatalf("provider event identity changed: %+v %v", props, err)
	}
	a.native = p
	a.SetMCPAddr("unused")
	a.SetEventHandler(func(providerprotocol.Event) { t.Fatal("unsupported event source invoked callback") })
	if err := a.WatchEvents(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	services = a.HostServices()
	if !a.RuntimeAvailable() || services.Runtime != nil || services.Namer != nil || services.Hooks != nil {
		t.Fatal("invented host capability")
	}
}

func TestAuthoritativeRuntimeIdlenessDoesNotRequireBrowsableSource(t *testing.T) {
	a, p, _ := fixture(t)
	a.native = &runtimeNative{nativePlatform: p, idle: true}
	a.unavailable = platforms.ErrHistoryUnavailable
	if idle, err := a.ReloadIdle(t.Context()); err != nil || !idle {
		t.Fatalf("unavailable transcript blocked authoritative idle: %v %v", idle, err)
	}
}
