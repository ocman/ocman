package previewauth

import "context"

// Workspaces lists the workspace IDs a viewer has connected for a provider.
func (m *Manager) Workspaces(ctx context.Context, viewerID, ownerID, providerID string) ([]string, error) {
	creds, err := m.db.PreviewCredentials(ctx, viewerID, ownerID, providerID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(creds))
	for _, c := range creds {
		ids = append(ids, c.WorkspaceID)
	}
	return ids, nil
}
