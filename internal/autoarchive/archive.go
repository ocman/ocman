package autoarchive

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/telemetry"
)

// Instruments degrade to no-ops when telemetry is disabled.
var archivedSessions, runs metric.Int64Counter

func init() {
	meter := telemetry.Meter()
	var err error
	if archivedSessions, err = meter.Int64Counter(
		"ocman.auto_archive.sessions",
		metric.WithDescription("Sessions archived by the auto-archive background loop."),
		metric.WithUnit("{session}"),
	); err != nil {
		log.WithError(err).Warn("creating auto_archive.sessions counter")
	}
	if runs, err = meter.Int64Counter(
		"ocman.auto_archive.runs",
		metric.WithDescription("Auto-archive loop iterations."),
		metric.WithUnit("{run}"),
	); err != nil {
		log.WithError(err).Warn("creating auto_archive.runs counter")
	}
}

func cutoffFor(settings Settings) int64 {
	return time.Now().Add(-time.Duration(settings.TTLDays) * 24 * time.Hour).UnixMilli()
}

// ArchiveSessions archives every available platform's sessions inactive
// for longer than the configured TTL, sparing sessions the user unarchived
// since the cutoff.
func (m *Module) ArchiveSessions(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, passTimeout)
	defer cancel()
	// Each tick has an independent span while retaining server cancellation.
	ctx, span := telemetry.Tracer().Start(ctx, "ocman.auto_archive.tick")
	defer span.End()
	settings, err := m.Settings(ctx)
	if err != nil {
		module.ReportError(ctx, err)
		span.RecordError(err)
		log.WithError(err).Error("reading auto-archive settings")
		return
	}
	if !settings.Enabled {
		return
	}
	cutoff := cutoffFor(settings)

	if runs != nil {
		runs.Add(ctx, 1)
	}

	archivedCount := 0
	archived, err := m.d.Store.ArchivedSessions(ctx)
	if err != nil {
		module.ReportError(ctx, err)
		log.WithError(err).Error("listing archived sessions")
		return
	}

	// Sessions the user deliberately brought back more recently than the
	// inactivity cutoff. Without this the loop — which runs at boot and
	// re-derives archive state purely from inactivity — silently re-hid
	// anything unarchived just to look at it.
	keep, err := m.d.Store.SessionsUnarchivedSince(ctx, cutoff)
	if err != nil {
		module.ReportError(ctx, err)
		span.RecordError(err)
		log.WithError(err).Error("listing recently unarchived sessions")
		keep = nil
	}

	for _, adapter := range m.d.Platforms() {
		if m.deferMaintenance(ctx, string(adapter.ID())) {
			continue
		}
		if !adapter.Available(ctx) {
			continue
		}
		rpcCtx, cancelRPC := context.WithTimeout(ctx, m.platformTimeout)
		sessions, err := adapter.SessionsInactiveBefore(rpcCtx, cutoff)
		cancelRPC()
		if err != nil {
			span.RecordError(err)
			module.ReportError(ctx, err)
			log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
				Error("listing inactive sessions for auto-archive")
			continue
		}
		for _, session := range sessions {
			key := state.Key{Platform: string(adapter.ID()), SessionID: session.ID}
			if _, ok := archived[key]; ok || keep[key] {
				continue
			}
			inserted, err := m.d.Store.AutoArchiveSession(ctx, string(adapter.ID()), session.ID, session.TimeUpdated, cutoff)
			if err != nil {
				span.RecordError(err)
				module.ReportError(ctx, err)
				log.WithFields(log.Fields{
					"platform":  adapter.ID(),
					"sessionID": session.ID,
					"error":     err,
				}).Error("auto-archiving inactive session")
				continue
			}
			if !inserted {
				continue
			}
			archivedCount++
			if archivedSessions != nil {
				archivedSessions.Add(ctx, 1, metric.WithAttributes(
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

// ArchiveProjects archives local projects whose most recent session
// activity is older than the configured TTL. Archive state is keyed by
// folded project root; already-archived roots are skipped. A project
// auto-unarchives later once it sees fresh activity, so this is safe to
// re-run.
func (m *Module) ArchiveProjects(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, passTimeout)
	defer cancel()
	if m.d.Store == nil || !m.d.StoredHistory() {
		return
	}
	ctx, span := telemetry.Tracer().Start(ctx, "ocman.auto_archive_projects.tick")
	defer span.End()
	settings, err := m.Settings(ctx)
	if err != nil {
		module.ReportError(ctx, err)
		span.RecordError(err)
		log.WithError(err).Error("reading auto-archive settings")
		return
	}
	if !settings.Enabled {
		return
	}
	cutoff := cutoffFor(settings)

	// Otherwise the project sweep could hide the sessions retained by the
	// initial selection, even when the session sweep correctly waits.
	for _, adapter := range m.d.Platforms() {
		if remoteID, _ := platforms.SplitPlatformID(string(adapter.ID())); remoteID == "" && m.deferMaintenance(ctx, string(adapter.ID())) {
			return
		}
	}
	local := m.d.Local()
	projects, err := local.Projects(ctx)
	if err != nil {
		module.ReportError(ctx, err)
		span.RecordError(err)
		log.WithError(err).Error("listing projects for auto-archive")
		return
	}

	archived, err := m.d.Store.ArchivedProjects(ctx)
	if err != nil {
		module.ReportError(ctx, err)
		span.RecordError(err)
		log.WithError(err).Error("listing archived projects for auto-archive")
		return
	}

	// Newest activity per folded root. This pass only sees this machine's
	// own projects, so every key is the local host.
	newest := map[string]int64{}
	for _, p := range projects {
		root := state.ProjectRootForDirectory(p.Directory)
		if p.LastUsed > newest[root] {
			newest[root] = p.LastUsed
		}
	}

	// Same guard as sessions: don't undo a deliberate unarchive.
	keep, err := m.d.Store.ProjectsUnarchivedSince(ctx, cutoff)
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
		if err := local.StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}); err != nil {
			span.RecordError(err)
			log.WithFields(log.Fields{"projectRoot": root, "error": err}).
				Warn("stopping opencode for auto-archived project")
		}
		if err := m.d.Store.ArchiveProject(ctx, state.LocalRemoteID, root); err != nil {
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

func (m *Module) deferMaintenance(ctx context.Context, platform string) bool {
	if m.d.SessionReady != nil && !m.d.SessionReady(platform) {
		return true
	}
	recent, err := m.d.Store.InitialImportArchiveRecent(ctx, platform, time.Now().Add(-24*time.Hour).UnixMilli())
	if err != nil {
		log.WithError(err).Error("reading first-import archive marker")
		return true
	}
	return recent
}
