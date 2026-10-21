package owned

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// HistoryReference supports retained pre-cutover rows. Canonical history keys
// are validated directly against their physical instance, without native lookup.
func HistoryReference(ctx context.Context, catalogue *sessioncatalog.Store, history *statev2.DB, instance, reference string) (string, error) {
	if history == nil {
		return "", platforms.ErrHistoryUnavailable
	}
	metadata, err := history.Metadata(ctx, statev2.Key{InstanceID: instance, SessionID: reference})
	if err != nil {
		return "", err
	}
	if sessioncatalog.IsID(reference) {
		row, err := catalogue.Get(ctx, reference)
		if err != nil {
			return "", err
		}
		if row.OwnerID != metadata.Instance.OwnerID || row.Platform != metadata.Instance.Platform || row.SourceKey != metadata.Instance.SourceKey {
			return "", sessioncatalog.ErrBindingConflict
		}
		return row.ID, nil
	}
	binding := sessioncatalog.Binding{OwnerID: metadata.Instance.OwnerID, Platform: metadata.Instance.Platform, SourceKey: metadata.Instance.SourceKey, NativeID: reference}
	row, err := catalogue.FindBinding(ctx, binding)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		source := sessioncatalog.Source{OwnerID: binding.OwnerID, Platform: binding.Platform, SourceKey: binding.SourceKey}
		if metadata.Purged {
			err = catalogue.Scrub(ctx, source, reference)
		} else {
			_, err = catalogue.Reconcile(ctx, source, []sessioncatalog.NativeSession{{ID: reference, Title: metadata.Session.Title, Directory: metadata.Session.Directory, ParentID: metadata.Session.ParentID, CreatedAt: metadata.Session.CreatedAt}})
		}
		if err == nil {
			row, err = catalogue.FindBinding(ctx, binding)
		}
	}
	if err != nil {
		return "", err
	}
	return row.ID, nil
}
