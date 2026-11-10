package platforms

import (
	"context"
	"slices"
)

// RegistryEntry retains the routing identity separately from the native kind.
type RegistryEntry struct {
	Key      ID
	Platform Platform
}

func (r *Registry) Entries() []RegistryEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]RegistryEntry, 0, len(r.order))
	for _, key := range r.order {
		entries = append(entries, RegistryEntry{Key: key, Platform: r.byID[key]})
	}
	return entries
}

// KeyFor returns the exact registration key, not Platform.ID's native kind.
func (r *Registry) KeyFor(p Platform) (ID, bool) {
	for _, entry := range r.Entries() {
		if entry.Platform == p {
			return entry.Key, true
		}
	}
	return "", false
}

// SetSourceResolver installs durable owner/source routing before consumers start.
func (r *Registry) SetSourceResolver(resolve func(context.Context, string, string, string) (ID, error)) {
	r.mu.Lock()
	r.sourceResolver = resolve
	r.mu.Unlock()
}

func (r *Registry) PlatformForCanonicalSource(ctx context.Context, owner, kind, source string) (Platform, ID, bool) {
	var matched *RegistryEntry
	for _, entry := range r.Entries() {
		var o, k string
		var sources []string
		if provenance, ok := entry.Platform.(interface {
			CanonicalSources() (string, string, []string)
		}); ok {
			o, k, sources = provenance.CanonicalSources()
		} else if provenance, ok := entry.Platform.(interface {
			CanonicalSource() (string, string, string)
		}); ok {
			var s string
			o, k, s = provenance.CanonicalSource()
			sources = []string{s}
		}
		if source != "" && o == owner && k == kind && slices.Contains(sources, source) {
			if matched != nil {
				return nil, "", false
			}
			copy := entry
			matched = &copy
		}
	}
	if matched != nil {
		return matched.Platform, matched.Key, true
	}
	r.mu.RLock()
	resolve := r.sourceResolver
	r.mu.RUnlock()
	if resolve == nil {
		return nil, "", false
	}
	key, err := resolve(ctx, owner, kind, source)
	if err != nil {
		return nil, "", false
	}
	p, ok := r.Get(key)
	return p, key, ok
}

func (r *Registry) SetLegacyResolver(resolve func(context.Context, string) (ID, error)) {
	r.mu.Lock()
	r.legacyResolver = resolve
	r.mu.Unlock()
}
func (r *Registry) ResolveLegacyScope(ctx context.Context, scope string) (ID, error) {
	r.mu.RLock()
	resolve := r.legacyResolver
	r.mu.RUnlock()
	if resolve == nil {
		return ID(scope), nil
	}
	return resolve(ctx, scope)
}
