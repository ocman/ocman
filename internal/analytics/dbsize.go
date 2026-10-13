package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/state"
)

const databaseSizeSampleInterval = time.Hour

func (m *Module) runDatabaseSizeLoop(ctx context.Context) {
	if m.deps.Store == nil {
		return
	}

	tick := func() {
		if err := m.collectDatabaseSizes(ctx, time.Now()); err != nil {
			log.WithError(err).Warn("collecting database sizes")
		}
	}
	runWithRecover("database-size", tick)

	ticker := time.NewTicker(databaseSizeSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("database-size", tick)
		}
	}
}

func (m *Module) collectDatabaseSizes(ctx context.Context, now time.Time) error {
	if m.deps.Store == nil {
		return nil
	}

	bucket := now.Truncate(databaseSizeSampleInterval).UnixMilli()
	samples := make([]state.DatabaseSizeSample, 0, 2)
	var errs []error
	if m.deps.DB != nil {
		size, err := m.deps.DB.SizeBytes(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("opencode: %w", err))
		} else {
			samples = append(samples, state.DatabaseSizeSample{Database: "opencode", SampledAt: bucket, SizeBytes: size})
		}
	}
	size, err := m.deps.Store.SizeBytes(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("ocman: %w", err))
	} else {
		samples = append(samples, state.DatabaseSizeSample{Database: "ocman", SampledAt: bucket, SizeBytes: size})
	}
	if len(samples) > 0 {
		if err := m.deps.Store.RecordDatabaseSizeSamples(ctx, samples); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
