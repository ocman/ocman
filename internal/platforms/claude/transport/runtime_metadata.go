package claudecode

import (
	"context"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
)

// Only the owning supervisor's runtime inventory can prove a pre-transcript
// child's identity. Status-only observations and shared history cannot.
func (c *claudeProjects) runtimeSession(ctx context.Context, id string) (*db.Session, error) {
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type result struct {
		adapter *provider.Adapter
		rows    []db.Session
		err     error
	}
	adapters := c.adapters()
	results := make(chan result, len(adapters))
	for _, a := range adapters {
		go func() {
			rows, err := a.RuntimeSessions(probe)
			results <- result{a, rows, err}
		}()
	}
	var session *db.Session
	var owner *provider.Adapter
	var failures error
	ambiguous := false
	for range adapters {
		r := <-results
		failures = errors.Join(failures, r.err)
		for _, row := range r.rows {
			if row.ID != id {
				continue
			}
			if owner != nil && owner != r.adapter {
				ambiguous = true
			}
			owner, session = r.adapter, &row
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ambiguous {
		return nil, platforms.ErrBusy
	}
	if failures != nil {
		return nil, errors.Join(platforms.ErrPlatformUnreachable, failures)
	}
	if session == nil {
		return nil, platforms.ErrNotFound
	}
	c.bindSession(id, owner)
	return session, nil
}
