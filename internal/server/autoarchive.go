package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// autoArchiveTickFn is the per-tick body of runAutoArchiveLoop, lifted
// to a package-level variable so tests can inject a panicking
// implementation (FR-11) and assert the loop survives.
var autoArchiveTickFn = func(ctx context.Context, s *Server) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	s.autoArchiveInactiveSessions(ctx)
	s.autoArchiveInactiveProjects(ctx)
	if ctx.Err() != nil {
		return
	}
	if removed := sweepComposerAttachments(composerAttachmentRoot(), composerAttachmentTTL); removed > 0 {
		log.WithField("removed", removed).Info("swept expired composer attachments")
	}
}

func (s *Server) runAutoArchiveLoop(ctx context.Context) {
	runWithRecover("auto-archive", func() { autoArchiveTickFn(ctx, s) })

	ticker := time.NewTicker(autoArchiveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("auto-archive", func() { autoArchiveTickFn(ctx, s) })
		}
	}
}

func (s *Server) autoArchiveInactiveSessions(ctx context.Context) {
	// Each tick has an independent span while retaining server cancellation.
	ctx, span := telemetry.Tracer().Start(ctx, "ocman.auto_archive.tick")
	defer span.End()
	settings, err := s.getAutoArchiveSettings(ctx)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("reading auto-archive settings")
		return
	}
	if !settings.Enabled {
		return
	}
	cutoff := time.Now().Add(-time.Duration(settings.TTLDays) * 24 * time.Hour).UnixMilli()

	if autoArchiveRuns != nil {
		autoArchiveRuns.Add(ctx, 1)
	}

	archivedCount := 0

	// Sessions the user deliberately brought back more recently than the
	// inactivity cutoff. Without this the loop — which runs at boot and
	// re-derives archive state purely from inactivity — silently re-hid
	// anything unarchived just to look at it.
	keep, err := s.stateDB.SessionsUnarchivedSince(ctx, cutoff)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("listing recently unarchived sessions")
		keep = nil
	}

	for _, adapter := range s.registry.Platforms() {
		if !adapter.Available(ctx) {
			continue
		}
		sessions, err := adapter.SessionsInactiveBefore(ctx, cutoff)
		if err != nil {
			span.RecordError(err)
			log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
				Error("listing inactive sessions for auto-archive")
			continue
		}
		for _, session := range sessions {
			if keep[state.Key{Platform: string(adapter.ID()), SessionID: session.ID}] {
				continue
			}
			if err := s.stateDB.ArchiveSession(ctx, string(adapter.ID()), session.ID, session.TimeUpdated); err != nil {
				span.RecordError(err)
				log.WithFields(log.Fields{
					"platform":  adapter.ID(),
					"sessionID": session.ID,
					"error":     err,
				}).Error("auto-archiving inactive session")
				continue
			}
			archivedCount++
			if autoArchiveSessions != nil {
				autoArchiveSessions.Add(ctx, 1, metric.WithAttributes(
					attribute.String("platform", string(adapter.ID())),
				))
			}
		}
	}

	span.SetAttributes(
		attribute.Int64("ocman.archived_count", int64(archivedCount)),
		attribute.Int64("ocman.cutoff_ms", cutoff),
	)

	log.WithFields(log.Fields{
		"cutoff":   cutoff,
		"archived": archivedCount,
	}).Info("auto-archive pass completed")
}

// autoArchiveInactiveProjects archives local projects whose most recent
// session activity is older than the configured auto-archive TTL. Archive state
// is keyed by folded project root; already-archived roots are skipped.
// A project auto-unarchives later (in applyProjectArchiveState) once it
// sees fresh activity, so this is safe to re-run.
func (s *Server) autoArchiveInactiveProjects(ctx context.Context) {
	if s.stateDB == nil || s.db == nil {
		return
	}
	ctx, span := telemetry.Tracer().Start(ctx, "ocman.auto_archive_projects.tick")
	defer span.End()
	settings, err := s.getAutoArchiveSettings(ctx)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("reading auto-archive settings")
		return
	}
	if !settings.Enabled {
		return
	}
	cutoff := time.Now().Add(-time.Duration(settings.TTLDays) * 24 * time.Hour).UnixMilli()

	projects, err := s.router().Local().Projects(ctx)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("listing projects for auto-archive")
		return
	}

	archived, err := s.stateDB.ArchivedProjects(ctx)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("listing archived projects for auto-archive")
		return
	}

	// Newest activity per folded root. This loop only sees the hub's own
	// projects (router().Local()), so every key is the local host.
	newest := map[string]int64{}
	for _, p := range projects {
		root := projectRootForDirectory(p.Directory)
		if p.LastUsed > newest[root] {
			newest[root] = p.LastUsed
		}
	}

	// Same guard as sessions: don't undo a deliberate unarchive.
	keep, err := s.stateDB.ProjectsUnarchivedSince(ctx, cutoff)
	if err != nil {
		span.RecordError(err)
		log.WithError(err).Error("listing recently unarchived projects")
		keep = nil
	}

	archivedCount := 0
	for root, last := range newest {
		if last >= cutoff {
			continue
		}
		key := state.ProjectKey{RemoteID: state.LocalRemoteID, Root: root}
		if _, ok := archived[key]; ok {
			continue
		}
		if keep[key] {
			continue
		}
		// Best-effort, same as the manual archive handler: a dead tmux
		// session or missing directory must not block auto-archiving.
		if err := s.router().Local().StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}); err != nil {
			span.RecordError(err)
			log.WithFields(log.Fields{"projectRoot": root, "error": err}).
				Warn("stopping opencode for auto-archived project")
		}
		if err := s.stateDB.ArchiveProject(ctx, state.LocalRemoteID, root); err != nil {
			span.RecordError(err)
			log.WithFields(log.Fields{"projectRoot": root, "error": err}).
				Error("auto-archiving inactive project")
			continue
		}
		archivedCount++
	}

	span.SetAttributes(
		attribute.Int64("ocman.archived_count", int64(archivedCount)),
		attribute.Int64("ocman.cutoff_ms", cutoff),
	)
	log.WithFields(log.Fields{
		"cutoff":   cutoff,
		"archived": archivedCount,
	}).Info("project auto-archive pass completed")
}
