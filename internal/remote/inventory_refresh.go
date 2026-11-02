package remote

import (
	"context"
	"sync"
)

const inventoryRefreshParallelism = 4

// RefreshInventories re-fetches connected remote inventories with bounded
// concurrency. Call periodically and on reconnect. Safe to call concurrently.
func (m *Manager) RefreshInventories(ctx context.Context) {
	m.mu.RLock()
	managed := make([]*managedRemote, 0, len(m.remotes))
	for _, mr := range m.remotes {
		managed = append(managed, mr)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	jobs := make(chan *managedRemote)
	for range min(inventoryRefreshParallelism, len(managed)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for mr := range jobs {
				if ctx.Err() == nil {
					m.refreshInventory(ctx, mr)
				}
			}
		}()
	}
	for _, mr := range managed {
		select {
		case jobs <- mr:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}
