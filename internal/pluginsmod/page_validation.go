package pluginsmod

import (
	"context"
	"encoding/json"

	plugins "github.com/ocman/ocman/sdk/protocol"
)

// Validation reads the current schema on the owner, never a browser-supplied
// schema. Every dispatch independently rechecks enablement and grants.
func (m *Module) validatePluginPageSave(ctx context.Context, id string, raw json.RawMessage) (plugins.PageDocument, bool, error) {
	req, err := plugins.DecodePageRequest(raw)
	if err != nil {
		return plugins.PageDocument{}, true, err
	}
	var version plugins.Version
	var hook bool
	m.mu.Lock()
	if m.store == nil {
		err = plugins.ErrUnavailable
	} else {
		err = m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
			if !d.PageAllowed(req.PageID, grants) || !d.PageRequestAllowed(req) {
				return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
			}
			version = d.PageVersion()
			for _, page := range d.Pages {
				if page.ID == req.PageID {
					hook = page.Validate
				}
			}
			return nil
		})
	}
	m.mu.Unlock()
	if err != nil || version.Minor < 1 {
		return plugins.PageDocument{}, err != nil, err
	}
	if err = plugins.ValidatePageCall("save", req, version); err != nil {
		return plugins.PageDocument{}, true, err
	}
	read := plugins.PageRequest{PageID: req.PageID, ProjectID: req.ProjectID, ItemID: req.ItemID}
	readRaw, _ := json.Marshal(read)
	doc, err := m.dispatchPluginPage(ctx, id, "read", readRaw)
	if err != nil {
		return doc, true, err
	}
	if req.Revision != doc.Revision {
		return doc, true, &plugins.WireError{Category: plugins.ErrorConflict}
	}
	values, err := plugins.PageValuesObject(req.Values)
	if err != nil {
		return doc, true, err
	}
	doc.Issues = plugins.ValidatePageInput(doc.Fields, values)
	if len(doc.Issues) > 0 {
		return doc, true, nil
	}
	if hook {
		validated, err := m.dispatchPluginPage(ctx, id, "validate", raw)
		if err != nil {
			return doc, true, err
		}
		if len(validated.Issues) > 0 {
			doc.Issues = validated.Issues
			return doc, true, nil
		}
	}
	return doc, false, nil
}
