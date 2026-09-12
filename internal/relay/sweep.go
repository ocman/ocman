package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/share"
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
		s.mutations.Lock()
		err := s.cfg.Store.DeletePrefix(ctx, datePrefix(day))
		s.mutations.Unlock()
		if err != nil {
			return fmt.Errorf("relay: sweeping %s: %w", datePrefix(day), err)
		}
	}
	objects, err := s.cfg.Store.List(ctx, "inboxes/")
	if err != nil {
		return fmt.Errorf("relay: listing inboxes: %w", err)
	}
	now := s.cfg.Now()
	for _, object := range objects {
		if !strings.HasSuffix(object.Key, "/meta") {
			continue
		}
		data, err := s.cfg.Store.Get(ctx, object.Key)
		if errors.Is(err, share.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("relay: reading inbox metadata: %w", err)
		}
		var m inboxMeta
		if json.Unmarshal(data, &m) == nil && m.CreatedAt > 0 && !now.Before(time.UnixMilli(m.CreatedAt).Add(s.cfg.InboxTTL)) {
			id := strings.TrimSuffix(strings.TrimPrefix(object.Key, "inboxes/"), "/meta")
			s.mutations.Lock()
			err = s.cfg.Store.DeletePrefix(ctx, "inboxes/"+id)
			s.mutations.Unlock()
			if err != nil {
				return fmt.Errorf("relay: expiring inbox: %w", err)
			}
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
