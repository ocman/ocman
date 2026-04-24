package opencode

import (
	"sort"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// variantNamesFor extracts the enabled variant names for a model from the
// provider data, returning them in a stable sorted order.
func variantNamesFor(m OpenCodeProviderModel) []string {
	if len(m.Variants) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.Variants))
	for name, v := range m.Variants {
		if !v.Disabled {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return names
}

// buildSessionModelEntries merges recents, live /provider data, the
// session default, and user-marked favorites into a single sorted list.
//
// Sort order:
//  1. Session default (⭐)
//  2. Favorites, in the order they were added
//  3. Recents, preserving recency order
//  4. Provider defaults (that aren't already above)
//  5. All remaining available models, alphabetical by provider then model
//  6. Any remaining DB-only recents (only reachable when providers weren't)
//
// Favorites are seeded into the entry map the same way recents are, so
// a favorited model stays visible even when its provider is currently
// disconnected — it just shows up as "archived" instead.
//
// Split out from SessionModels so it's unit-testable without a running
// OpenCode instance.
func buildSessionModelEntries(
	recents []db.RecentModel,
	favorites []state.ModelFavorite,
	providers OpenCodeProvidersResponse,
	hasProviders bool,
	sessionDefault string,
) []platforms.SessionModel {
	key := func(provider, model string) string { return provider + "/" + model }

	entryMap := make(map[string]*platforms.SessionModel)
	get := func(provider, model string) *platforms.SessionModel {
		k := key(provider, model)
		if e, ok := entryMap[k]; ok {
			return e
		}
		e := &platforms.SessionModel{Provider: provider, Model: model}
		entryMap[k] = e
		return e
	}

	// Seed from recents so they're always known to the merger, even when
	// the provider no longer appears in /provider.
	for i, rm := range recents {
		e := get(rm.Provider, rm.Model)
		e.RecentRank = i + 1
	}

	// Favorites are also seeded so a favorited model keeps its star even
	// after the provider is disconnected.
	favoriteRank := make(map[string]int, len(favorites))
	for i, f := range favorites {
		e := get(f.Provider, f.Model)
		e.IsFavorite = true
		favoriteRank[key(f.Provider, f.Model)] = i + 1
	}

	// Index the connected provider set for cheap lookup.
	connected := make(map[string]struct{}, len(providers.Connected))
	for _, id := range providers.Connected {
		connected[id] = struct{}{}
	}

	// Live-available models override names and mark availability. Filter to
	// connected providers only — showing 115 unconfigured providers would be
	// useless noise.
	providerName := make(map[string]string, len(providers.All))
	for _, p := range providers.All {
		providerName[p.ID] = p.Name
		if _, ok := connected[p.ID]; !ok {
			continue
		}
		for modelID, m := range p.Models {
			// Hide deprecated models unless we have a reason to show them.
			if m.Status != "" && m.Status != "active" {
				if _, seen := entryMap[key(p.ID, modelID)]; !seen && sessionDefault != key(p.ID, modelID) {
					continue
				}
			}
			e := get(p.ID, modelID)
			e.ProviderName = p.Name
			e.ModelName = m.Name
			e.IsAvailable = true
			if variants := variantNamesFor(m); len(variants) > 0 {
				e.Reasoning = variants
			}
		}
	}
	// Back-fill provider names on entries that aren't in the live set (e.g.
	// recents from a provider the user removed).
	for _, e := range entryMap {
		if e.ProviderName == "" {
			if name := providerName[e.Provider]; name != "" {
				e.ProviderName = name
			}
		}
	}

	// Mark session default + provider defaults.
	if sessionDefault != "" {
		if e, ok := entryMap[sessionDefault]; ok {
			e.IsSessionDefault = true
		} else if slash := strings.IndexByte(sessionDefault, '/'); slash > 0 {
			// Session default refers to a model we haven't seen elsewhere —
			// still surface it so it's selectable.
			e := get(sessionDefault[:slash], sessionDefault[slash+1:])
			e.IsSessionDefault = true
		}
	}
	for providerID, modelID := range providers.Default {
		if e, ok := entryMap[key(providerID, modelID)]; ok {
			e.IsProviderDefault = true
		}
	}

	// Collect and sort.
	out := make([]platforms.SessionModel, 0, len(entryMap))
	for _, e := range entryMap {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		// 1. session default first
		if a.IsSessionDefault != b.IsSessionDefault {
			return a.IsSessionDefault
		}
		// 2. favorites before non-favorites; preserve the order they
		//    were added. A favorited model that's also recent still
		//    sorts in the favorites band — the star is the stronger
		//    signal the user opted into.
		if a.IsFavorite != b.IsFavorite {
			return a.IsFavorite
		}
		if a.IsFavorite && b.IsFavorite {
			return favoriteRank[key(a.Provider, a.Model)] < favoriteRank[key(b.Provider, b.Model)]
		}
		// 3. recents before non-recents; within recents, preserve rank
		aRecent, bRecent := a.RecentRank > 0, b.RecentRank > 0
		if aRecent != bRecent {
			return aRecent
		}
		if aRecent && bRecent {
			return a.RecentRank < b.RecentRank
		}
		// 4. provider defaults before non-defaults
		if a.IsProviderDefault != b.IsProviderDefault {
			return a.IsProviderDefault
		}
		// 5. available before unavailable
		if a.IsAvailable != b.IsAvailable {
			return a.IsAvailable
		}
		// 6. alphabetical by provider then model
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Model < b.Model
	})
	// When we got no live provider data, flip IsAvailable off so the client
	// doesn't try to distinguish "archived" vs "available" in the UI.
	if !hasProviders {
		for i := range out {
			out[i].IsAvailable = false
		}
	}
	return out
}
