package sidebar

import (
	"fmt"
	"slices"
)

// A grouped SQL projection must never choose the first account claiming a
// source. The routing labels are supplied by owner composition, not the client.
func validateRoutingScopes(scopes []Scope) error {
	for i, a := range scopes {
		for _, b := range scopes[:i] {
			if a.Owner != b.Owner || a.Platform != b.Platform {
				continue
			}
			aKey, bKey := a.RoutingKey, b.RoutingKey
			if aKey == "" {
				aKey = a.Platform
			}
			if bKey == "" {
				bKey = b.Platform
			}
			if aKey == bKey {
				continue
			}
			if !a.Scoped || !b.Scoped {
				return fmt.Errorf("sidebar: ambiguous account source ownership")
			}
			keys := append([]string{a.Source}, a.Sources...)
			for _, key := range keys {
				if key != "" && (key == b.Source || slices.Contains(b.Sources, key)) {
					return fmt.Errorf("sidebar: ambiguous account source ownership")
				}
			}
		}
	}
	return nil
}
