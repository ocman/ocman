package conversationingest

import "context"

// repair runs one prepare/execute pass synchronously, as a source worker's
// loop would after an invalidation.
func (w *sourceWorker) repair(ctx context.Context, dirty map[string]bool) error {
	p, err := w.prepare(ctx, dirty)
	if err != nil {
		return err
	}
	return w.execute(ctx, p, nil)
}
