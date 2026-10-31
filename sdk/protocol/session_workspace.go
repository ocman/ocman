package protocol

import (
	"context"
	"path/filepath"
	"slices"
)

// The owner chooses the private directory, never a plugin-supplied path. This
// session.plugin alone only starts tool-denied model turns. session.source
// additionally permits host-pinned reads of prepared source under a subpath.
func (b *SessionBroker) workspace(ctx context.Context, pluginID string, d Description, grants []string, admitted Config, params SessionRunParams, version Version) (Config, error) {
	declared := slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == "session" && c.Version.Major == 1 && c.Version.Minor >= 2 })
	if version.Minor < 2 || !declared || !slices.Contains(d.RequestedGrants, SessionPluginGrant) || !slices.Contains(grants, SessionPluginGrant) || b.ResolveWorkspace == nil {
		return Config{}, &WireError{Category: ErrorPermissionDenied}
	}
	if params.ReadOnly {
		declaredSource := slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == "session" && c.Version.Major == 1 && c.Version.Minor >= 4 })
		if version.Minor < 4 || !declaredSource || !slices.Contains(d.RequestedGrants, SessionSourceGrant) || !slices.Contains(grants, SessionSourceGrant) {
			return Config{}, &WireError{Category: ErrorPermissionDenied}
		}
	} else if !params.NoTools {
		return Config{}, &WireError{Category: ErrorPermissionDenied}
	}
	dir, err := b.ResolveWorkspace(ctx, pluginID, params.WorkspaceID)
	if err != nil || !filepath.IsAbs(dir) {
		return Config{}, &WireError{Category: ErrorPermissionDenied}
	}
	admitted.AssociatedProject = ""
	if params.AssociatedProjectID != "" {
		if !slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == "session" && c.Version.Major == 1 && c.Version.Minor >= 6 }) || !slices.Contains(d.RequestedGrants, SessionAssociateGrant) || !slices.Contains(grants, SessionAssociateGrant) || b.ResolveProject == nil {
			return Config{}, &WireError{Category: ErrorPermissionDenied}
		}
		target, err := b.ResolveProject(ctx, params.AssociatedProjectID)
		if err != nil || !filepath.IsAbs(target) {
			return Config{}, &WireError{Category: ErrorPermissionDenied}
		}
		admitted.AssociatedProject = target
	}
	admitted.Project = dir
	return admitted, nil
}
