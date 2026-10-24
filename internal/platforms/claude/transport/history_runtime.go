package claudecode

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Runtime discovery augments successful transcript inventory, never spends the
// entire source read deadline on one unavailable supervisor. Probe concurrently
// so the bound is independent of the number of attached projects.
func (c *claudeProjects) historyRuntimeInventory(ctx context.Context) ([]db.Session, error) {
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type result struct {
		rows []db.Session
		err  error
	}
	adapters := c.adapters()
	results := make(chan result, len(adapters))
	var wg sync.WaitGroup
	for _, a := range adapters {
		wg.Go(func() { rows, err := a.RuntimeSessions(probe); results <- result{rows, err} })
	}
	wg.Wait()
	close(results)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	rows := []db.Session{}
	var cause error
	missing := 0
	for r := range results {
		if r.err != nil {
			missing++
			cause = errors.Join(cause, r.err)
			continue
		}
		rows = append(rows, r.rows...)
	}
	if missing > 0 {
		return rows, &platforms.HistoryDeferred{Count: missing, Err: cause}
	}
	return rows, nil
}
