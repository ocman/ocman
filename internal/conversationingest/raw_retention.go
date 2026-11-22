package conversationingest

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

func (m *Module) runRawHistoryRetention(ctx context.Context) {
	const name = "raw conversation history retention"
	const interval = 5 * time.Minute
	defer module.StopSchedule(ctx, name)
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		module.Scheduled(ctx, name, interval, m.sweepRawHistory)
		timer.Reset(interval)
	}
}

func (m *Module) sweepRawHistory(ctx context.Context) {
	store := m.deps.Store
	result, err := store.SweepRawHistory(ctx, time.Now())
	entry := log.WithFields(log.Fields{
		"dry_run":   store.RawHistoryRetentionMode() == statev2.RawHistoryRetentionReport,
		"messages":  result.Messages,
		"snapshots": result.Snapshots,
		"bytes":     result.Bytes,
		"pending":   result.Pending,
	})
	if err != nil {
		if ctx.Err() == nil {
			entry.WithError(err).Warn("raw conversation history sweep failed")
		}
		return
	}
	if result.Messages != 0 || result.Snapshots != 0 {
		entry.Info("raw conversation history sweep slice processed")
	}
}
