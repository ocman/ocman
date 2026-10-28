package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/startup"
	"github.com/ocman/ocman/internal/state"
)

func (s *ReferenceStore) restoreLocalState(ctx context.Context, core *state.DB, catalogue *sessioncatalog.Store, owner, scope string) error {
	var cursor string
	processed := 0
	for {
		startup.Report(ctx, "Resolving historical session references", processed, 0)
		page, err := s.referencePage(ctx, scope, cursor)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		mapping := map[string]string{}
		for _, ref := range page {
			processed++
			startup.Report(ctx, "Resolving historical session references", processed, 0)
			id := ref.CanonicalID
			if id == "" {
				row, err := catalogue.FindLegacyBinding(ctx, owner, scope, ref.NativeID)
				if errors.Is(err, sessioncatalog.ErrNotFound) || errors.Is(err, sessioncatalog.ErrBindingConflict) {
					continue
				}
				if err != nil {
					return err
				}
				if row.CreationState != sessioncatalog.Ready {
					continue
				}
				if err := catalogue.RetainLegacyKeys(ctx, owner, row.ID); err != nil {
					return err
				}
				id = row.ID
				if err := s.bind(ctx, scope, ref.ID, id); err != nil {
					return err
				}
			}
			mapping[ref.ID] = id
		}
		if len(mapping) > 0 {
			if err := core.RekeySessionReferences(ctx, scope, mapping); err != nil {
				return err
			}
		}
		cursor = page[len(page)-1].ID
	}
}
