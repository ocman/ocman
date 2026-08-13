package relay

import (
	"context"
	"fmt"
	"time"
)

// sweepWindowDays is how far back an expiry sweep looks.
//
// Because shares are partitioned by creation date, the sweeper can
// compute the prefixes it wants to delete instead of listing the store
// to discover them — deleting an absent prefix is a no-op, so it simply
// deletes every day in the window that has fallen past the TTL. That
// keeps expiry O(window) regardless of how much is stored, and needs no
// listing primitive an object store would have to emulate.
//
// ponytail: a relay that stays offline for longer than this window
// leaves the days it skipped behind. Widen the window, or let the object
// store's own lifecycle rules handle expiry, if that ever matters.
const sweepWindowDays = 90

// SweepInterval is how often Run performs an expiry sweep.
const SweepInterval = time.Hour

// Sweep deletes every share whose creation date has fallen past the TTL.
func (s *Server) Sweep(ctx context.Context) error {
	cutoff := s.cfg.Now().UTC().Add(-s.cfg.TTL)
	for i := range sweepWindowDays {
		day := cutoff.AddDate(0, 0, -i-1)
		if err := s.cfg.Store.DeletePrefix(ctx, datePrefix(day)); err != nil {
			return fmt.Errorf("relay: sweeping %s: %w", datePrefix(day), err)
		}
	}
	return nil
}

// Run performs expiry sweeps until the context is cancelled. It sweeps
// once immediately so a restart reclaims anything that expired while the
// relay was down.
func (s *Server) Run(ctx context.Context, onError func(error)) {
	sweep := func() {
		if err := s.Sweep(ctx); err != nil && onError != nil {
			onError(err)
		}
	}
	sweep()

	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
