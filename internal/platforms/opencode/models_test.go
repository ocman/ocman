package opencode

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

// providersFixture returns a minimal /provider response covering one
// connected provider with two models. Enough to exercise the merger
// without wiring up a full fake instance.
func providersFixture() OpenCodeProvidersResponse {
	return OpenCodeProvidersResponse{
		All: []OpenCodeProvider{{
			ID:   "anthropic",
			Name: "Anthropic",
			Models: map[string]OpenCodeProviderModel{
				"claude-opus-4":   {ID: "claude-opus-4", Name: "Claude Opus 4"},
				"claude-sonnet-4": {ID: "claude-sonnet-4", Name: "Claude Sonnet 4"},
			},
		}},
		Connected: []string{"anthropic"},
		Default:   map[string]string{"anthropic": "claude-sonnet-4"},
	}
}

func TestBuildSessionModelEntries_TagsFavorites(t *testing.T) {
	providers := providersFixture()
	favorites := []state.ModelFavorite{
		{Platform: "opencode", Provider: "anthropic", Model: "claude-opus-4"},
	}

	entries := buildSessionModelEntries(nil, favorites, providers, true, "")

	var found bool
	for _, e := range entries {
		if e.Provider == "anthropic" && e.Model == "claude-opus-4" {
			if !e.IsFavorite {
				t.Errorf("expected claude-opus-4 to be marked IsFavorite, got false")
			}
			found = true
		} else {
			if e.IsFavorite {
				t.Errorf("did not expect %s/%s to be favorite", e.Provider, e.Model)
			}
		}
	}
	if !found {
		t.Errorf("claude-opus-4 not in result")
	}
}

// TestBuildSessionModelEntries_FavoritesSortAfterSessionDefault
// verifies the ordering: session default is always first, then
// favorites (in insertion order), then recents, then the rest.
func TestBuildSessionModelEntries_FavoritesSortAfterSessionDefault(t *testing.T) {
	providers := providersFixture()
	recents := []db.RecentModel{
		{Provider: "anthropic", Model: "claude-sonnet-4"}, // also the provider default
	}
	favorites := []state.ModelFavorite{
		{Platform: "opencode", Provider: "anthropic", Model: "claude-opus-4"},
	}

	entries := buildSessionModelEntries(recents, favorites, providers, true, "anthropic/claude-sonnet-4")

	// Expect: claude-sonnet-4 (session default) first, claude-opus-4
	// (favorite) second. Recents with no favorite star sort in the
	// recents band, which is after favorites.
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}
	if entries[0].Model != "claude-sonnet-4" || !entries[0].IsSessionDefault {
		t.Errorf("position 0: expected claude-sonnet-4 (session default), got %+v", entries[0])
	}
	if entries[1].Model != "claude-opus-4" || !entries[1].IsFavorite {
		t.Errorf("position 1: expected claude-opus-4 (favorite), got %+v", entries[1])
	}
}

// TestBuildSessionModelEntries_FavoritesPreserveInsertionOrder
// checks that when several favorites compete, they come back in the
// order they were added.
func TestBuildSessionModelEntries_FavoritesPreserveInsertionOrder(t *testing.T) {
	providers := OpenCodeProvidersResponse{
		All: []OpenCodeProvider{{
			ID:   "anthropic",
			Name: "Anthropic",
			Models: map[string]OpenCodeProviderModel{
				"claude-opus-4":   {ID: "claude-opus-4", Name: "Claude Opus 4"},
				"claude-sonnet-4": {ID: "claude-sonnet-4", Name: "Claude Sonnet 4"},
				"claude-haiku-4":  {ID: "claude-haiku-4", Name: "Claude Haiku 4"},
			},
		}},
		Connected: []string{"anthropic"},
	}
	favorites := []state.ModelFavorite{
		{Platform: "opencode", Provider: "anthropic", Model: "claude-opus-4"},
		{Platform: "opencode", Provider: "anthropic", Model: "claude-haiku-4"},
		{Platform: "opencode", Provider: "anthropic", Model: "claude-sonnet-4"},
	}

	entries := buildSessionModelEntries(nil, favorites, providers, true, "")

	// The first three entries must be the favorites, in the order
	// they were added.
	wantOrder := []string{"claude-opus-4", "claude-haiku-4", "claude-sonnet-4"}
	for i, want := range wantOrder {
		if i >= len(entries) {
			t.Fatalf("not enough entries: %d", len(entries))
		}
		if entries[i].Model != want {
			t.Errorf("position %d: expected %s, got %s", i, want, entries[i].Model)
		}
		if !entries[i].IsFavorite {
			t.Errorf("position %d (%s): expected IsFavorite=true", i, entries[i].Model)
		}
	}
}

// TestBuildSessionModelEntries_FavoriteFromDisconnectedProvider
// ensures a favorited model stays visible even when the provider is
// no longer in the `connected` set — the star should outlive a
// temporarily unavailable API key.
func TestBuildSessionModelEntries_FavoriteFromDisconnectedProvider(t *testing.T) {
	providers := OpenCodeProvidersResponse{
		All:       nil, // nothing connected
		Connected: nil,
	}
	favorites := []state.ModelFavorite{
		{Platform: "opencode", Provider: "anthropic", Model: "claude-opus-4"},
	}

	entries := buildSessionModelEntries(nil, favorites, providers, true, "")

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (the orphan favorite), got %d", len(entries))
	}
	e := entries[0]
	if !e.IsFavorite {
		t.Error("orphan favorite should still be marked IsFavorite")
	}
	if e.IsAvailable {
		t.Error("orphan favorite should NOT be marked available (provider disconnected)")
	}
}
