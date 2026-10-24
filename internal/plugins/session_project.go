package plugins

import (
	"context"
	"math"
	"path/filepath"
	"slices"
)

// SessionUsage is host-observed consumption for the completed assistant turn.
// Nil usage means unavailable, never zero-cost inference.
type SessionUsage struct {
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CostUSD          float64 `json:"costUsd"`
}

func (u SessionUsage) Validate() error {
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.PromptTokens > math.MaxInt64-u.CompletionTokens || !(u.CostUSD >= 0) || math.IsInf(u.CostUSD, 1) {
		return ErrInvalidMessage
	}
	return nil
}

func projectSessionDeclared(d Description) bool {
	return slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
		return c.Name == "session" && c.Version.Major == 1 && c.Version.Minor >= 1
	})
}

// ResolveProject resolves opaque IDs through the executing owner's project
// database. Never resolve on the hub or accept a plugin-supplied directory.
func (b *SessionBroker) project(ctx context.Context, d Description, grants []string, admitted Config, params SessionRunParams, version Version) (Config, error) {
	if params.ProjectID == "" {
		if params.NoTools && version.Minor < 1 {
			return Config{}, &WireError{Category: ErrorInvalidArgument}
		}
		return admitted, nil
	}
	if version.Minor < 1 || !projectSessionDeclared(d) || !slices.Contains(grants, SessionProjectGrant) || b.ResolveProject == nil {
		return Config{}, &WireError{Category: ErrorPermissionDenied}
	}
	directory, err := b.ResolveProject(ctx, params.ProjectID)
	if err != nil || !filepath.IsAbs(directory) {
		return Config{}, &WireError{Category: ErrorPermissionDenied}
	}
	admitted.Project = directory
	return admitted, nil
}
