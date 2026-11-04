package conversationingest

import "context"

const initialImportKey = "initial-import"

// Record eligibility before the first content write, so restarting a partial
// import does not mistake it for an existing installation. Upgrades opt out.
func (w *sourceWorker) prepareInitialImport(ctx context.Context, saved map[string]string) error {
	w.initialImport = saved[initialImportKey]
	if w.initialImport != "" {
		return nil
	}
	w.initialImport = "done"
	if len(saved) == 0 && w.module.deps.OnInitialImport != nil {
		sessions, err := w.module.deps.Store.Sessions(ctx, w.instance.ID)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			w.initialImport = "pending"
		}
	}
	return w.module.deps.Store.SetSourceState(ctx, w.instance.ID, initialImportKey, w.initialImport)
}

func (w *sourceWorker) completeInitialImport(ctx context.Context) error {
	if w.initialImport != "pending" || w.module.deps.OnInitialImport == nil {
		return nil
	}
	sessions, err := w.module.deps.Store.Sessions(ctx, w.instance.ID)
	if err != nil {
		return err
	}
	if err := w.module.deps.OnInitialImport(ctx, w.source.AdapterID, sessions); err != nil {
		return err
	}
	if err := w.module.deps.Store.SetSourceState(ctx, w.instance.ID, initialImportKey, "done"); err != nil {
		return err
	}
	w.initialImport = "done"
	return nil
}
