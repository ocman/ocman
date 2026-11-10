package owned

import (
	"context"
	"sort"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
)

// NewRetained constructs a canonical read boundary without touching native
// configuration, credentials or a runtime. All mutations fail source checks.
func NewRetained(catalogue *sessioncatalog.Store, history *statev2.DB, owner string, source platforms.HistoryIdentity) (*Adapter, error) {
	if catalogue == nil || history == nil || owner == "" || source.Platform == "" || source.SourceKey == "" {
		return nil, sessioncatalog.ErrInvalid
	}
	native := &retainedNative{source: source}
	return newAdapter(native, catalogue, history, owner, source, nil, platforms.ErrPlatformUnreachable).WithSourceScope(), nil
}

// WithSourceScope pins named browsing to this immutable physical source. Call
// only during composition, before registration or background readers start.
func (a *Adapter) WithSourceScope() *Adapter { a.sourceScoped = true; return a }

func (a *Adapter) WithReadSources(keys []string) *Adapter {
	a.sourceScoped = true
	a.readSources = make(map[string]bool, len(keys)+1)
	a.readSources[a.source.SourceKey] = true
	for _, key := range keys {
		if key != "" {
			a.readSources[key] = true
		}
	}
	return a
}

func (a *Adapter) allowsReadSource(key string) bool {
	return !a.sourceScoped || key == a.source.SourceKey || a.readSources[key]
}

// ReadSourceKeys exposes frozen provenance only to authenticated owner metadata.
// Membership grants retained reads, never native execution authority.
func (a *Adapter) ReadSourceKeys() []string {
	keys := make([]string, 0, len(a.readSources)+1)
	for key := range a.readSources {
		keys = append(keys, key)
	}
	if len(keys) == 0 && a.source.SourceKey != "" {
		keys = append(keys, a.source.SourceKey)
	}
	sort.Strings(keys)
	return keys
}

type retainedNative struct {
	platforms.Integration
	source platforms.HistoryIdentity
}

func (p *retainedNative) ID() platforms.ID                   { return platforms.ID(p.source.Platform) }
func (p *retainedNative) DisplayName() string                { return p.source.Name }
func (*retainedNative) Available(context.Context) bool       { return false }
func (*retainedNative) Capabilities() platforms.Capabilities { return platforms.Capabilities{} }

func (*retainedNative) ReloadIdle(context.Context) (bool, error) { return true, nil }
