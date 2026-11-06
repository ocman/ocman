package inbox

import (
	"context"
	"sync"
)

// BadgeAttention uses the same owners as the Inbox, without title or pin enrichment.
// Permission items are already represented by their session's attention state.
func (m *Module) BadgeAttention(ctx context.Context, categories map[string]bool) (bool, error) {
	sources := m.inboxSources()
	attention := make([]bool, len(sources))
	errors := make([]error, len(sources))
	sem := make(chan struct{}, fanoutLimit)
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			readCtx := ctx
			if source != "local" {
				var cancel context.CancelFunc
				readCtx, cancel = context.WithTimeout(ctx, remoteTimeout)
				defer cancel()
			}
			items, err := m.inboxItems(readCtx, source, false)
			errors[i] = err
			for _, item := range items {
				category := item.Category
				if category == "" {
					category = "general"
				}
				if categories[category] && category != "permission" && item.Permission == nil && item.ReadAt == 0 && item.ArchivedAt == 0 {
					attention[i] = true
					break
				}
			}
		})
	}
	wg.Wait()
	found := false
	for i, source := range sources {
		if errors[i] != nil {
			if source == "local" {
				return false, errors[i]
			}
			continue // Match the Inbox's unavailable-owner behaviour.
		}
		found = found || attention[i]
	}
	return found, nil
}
