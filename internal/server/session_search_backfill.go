package server

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

func (s *Server) runSessionSearchBackfill(ctx context.Context) {
	runSessionSearchBackfill(ctx, s.historyAnalytics.Store.BackfillSessionSearch)
}

func runSessionSearchBackfill(ctx context.Context, batch func(context.Context) (bool, error)) {
	for ctx.Err() == nil {
		delay := 50 * time.Millisecond
		done := false
		module.RunWithRecover("session search backfill", func() {
			var err error
			done, err = batch(ctx)
			if err != nil && ctx.Err() == nil {
				log.WithError(err).Error("backfill session search index")
				delay = 10 * time.Second
				done = false
			}
		})
		if done {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
