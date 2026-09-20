package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/state"
)

const databaseSizeSampleInterval = time.Hour

func (s *Server) runDatabaseSizeLoop(ctx context.Context) {
	if s.stateDB == nil {
		return
	}

	tick := func() {
		if err := s.collectDatabaseSizes(ctx, time.Now()); err != nil {
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

func (s *Server) collectDatabaseSizes(ctx context.Context, now time.Time) error {
	if s.stateDB == nil {
		return nil
	}

	bucket := now.Truncate(databaseSizeSampleInterval).UnixMilli()
	samples := make([]state.DatabaseSizeSample, 0, 2)
	var errs []error
	if s.db != nil {
		size, err := s.db.SizeBytes(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("opencode: %w", err))
		} else {
			samples = append(samples, state.DatabaseSizeSample{Database: "opencode", SampledAt: bucket, SizeBytes: size})
		}
	}
	size, err := s.stateDB.SizeBytes(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("ocman: %w", err))
	} else {
		samples = append(samples, state.DatabaseSizeSample{Database: "ocman", SampledAt: bucket, SizeBytes: size})
	}
	if len(samples) > 0 {
		if err := s.stateDB.RecordDatabaseSizeSamples(ctx, samples); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
