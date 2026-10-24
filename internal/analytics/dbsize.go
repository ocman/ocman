package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
)

const databaseSizeSampleInterval = time.Hour

func (m *Module) runDatabaseSizeLoop(ctx context.Context) {
	if m.deps.Store == nil {
		return
	}

	module.Every(ctx, "database-size", databaseSizeSampleInterval, func(ctx context.Context) {
		if err := m.collectDatabaseSizes(ctx, time.Now()); err != nil {
			log.WithError(err).Warn("collecting database sizes")
		}
	})
}

func (m *Module) collectDatabaseSizes(ctx context.Context, now time.Time) error {
	if m.deps.Store == nil {
		return nil
	}

	bucket := now.Truncate(databaseSizeSampleInterval).UnixMilli()
	samples := make([]state.DatabaseSizeSample, 0, 3)
	var errs []error
	size, err := m.deps.Store.SizeBytes(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("ocman: %w", err))
	} else {
		samples = append(samples, state.DatabaseSizeSample{Database: "ocman", SampledAt: bucket, SizeBytes: size})
	}
	if len(samples) > 0 {
		// Sampling is independent of ingestion readiness: the store already
		// occupies space during its initial import and during source outages.
		if m.deps.HistoryAnalytics != nil {
			if h := m.deps.HistoryAnalytics(); h != nil && h.Store != nil {
				if size, err := h.Store.SizeBytes(ctx); err != nil {
					errs = append(errs, fmt.Errorf("statev2: %w", err))
				} else {
					samples = append(samples, state.DatabaseSizeSample{Database: "statev2", SampledAt: bucket, SizeBytes: size})
				}
			}
		}
		if err := m.deps.Store.RecordDatabaseSizeSamples(ctx, samples); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
