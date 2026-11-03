package platformsettings

import (
	"slices"

	"github.com/ocman/ocman/internal/platforms"
)

// Option is one platform the Settings page offers. Setup reports that
// enabling it first checks host prerequisites through the setup routes.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Setup bool   `json:"setup,omitempty"`
}

// catalog lists the selectable platforms in display order.
var catalog = []Option{
	{ID: string(platforms.OpenCodeID)},
	{ID: "claudecode"},
	{ID: "codex", Setup: true},
}

// Options returns the selectable platforms with their display names.
func Options() []Option {
	out := slices.Clone(catalog)
	for i := range out {
		out[i].Label = platforms.DisplayName(platforms.ID(out[i].ID))
	}
	return out
}

func option(id string) (Option, bool) {
	i := slices.IndexFunc(catalog, func(o Option) bool { return o.ID == id })
	if i < 0 {
		return Option{}, false
	}
	return catalog[i], true
}
