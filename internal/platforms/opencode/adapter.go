// Package opencode implements the platforms.Platform interface for OpenCode.
//
// It wraps the read-only SQLite reader in internal/db and exposes the
// HTTP-proxy behavior (port discovery, composer, etc.) that the server
// package uses today. Handlers are being migrated to go through this
// adapter as part of the multi-platform work; Phase 1 establishes the
// interface and identity, later phases move behavior here.
package opencode

import (
	"context"
	"database/sql"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// PlatformID is the stable identifier used in URLs and state.db rows.
const PlatformID platforms.ID = "opencode"

// Adapter implements platforms.Platform for OpenCode.
type Adapter struct {
	db *db.DB
}

// New returns a new OpenCode adapter backed by the given read-only DB.
// A nil DB is permitted so Available() can report absence cleanly.
func New(database *db.DB) *Adapter {
	return &Adapter{db: database}
}

// ID returns the OpenCode platform identifier.
func (a *Adapter) ID() platforms.ID { return PlatformID }

// DisplayName returns the user-facing name.
func (a *Adapter) DisplayName() string { return "OpenCode" }

// Available reports whether the underlying OpenCode database is usable.
// Returns false when the adapter has no DB (OpenCode not installed).
func (a *Adapter) Available(context.Context) bool {
	return a.db != nil
}

// Capabilities returns OpenCode's capability set. OpenCode supports every
// feature ocman exposes today.
func (a *Adapter) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{
		Composer:          true,
		RespondPermission: true,
		RespondQuestion:   true,
		Abort:             true,
		Compact:           true,
		Events:            true,
		AgentCatalog:      true,
		ModelCatalog:      true,
		SlashCommands:     true,
	}
}

// Sessions returns all OpenCode sessions, optionally filtered by directory
// and/or updated-after timestamp. In addition to Platform, the adapter
// populates LiveConnection, PendingPermission, and PendingQuestion by
// probing running OpenCode instances — the server package no longer
// needs to know about lsof or OpenCode HTTP endpoints to list sessions.
func (a *Adapter) Sessions(_ context.Context, dir string, since int64) ([]db.Session, error) {
	if a.db == nil {
		return nil, nil
	}
	sessions, err := a.db.GetSessions(dir, since)
	if err != nil {
		return nil, err
	}

	// Fan out to every running OpenCode instance to collect liveness
	// flags. Failures are silent — this is a best-effort UI hint.
	ports := discoverOpenCodePorts()
	pendingPerms, pendingQuestions := collectPendingPromptsByDir(ports)

	for i := range sessions {
		sessions[i].Platform = string(PlatformID)
		if _, ok := ports[sessions[i].Directory]; ok {
			sessions[i].LiveConnection = true
		}
		if pendingPerms[sessions[i].ID] {
			sessions[i].PendingPermission = true
		}
		if pendingQuestions[sessions[i].ID] {
			sessions[i].PendingQuestion = true
		}
	}
	return sessions, nil
}

// Session returns full detail for one session. Prefers live OpenCode
// API data (for in-flight streams) with a fallback to the read-only
// DB. Both paths return the same typed SessionDetail shape.
func (a *Adapter) Session(_ context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	// Try live data from OpenCode first.
	if detail, ok := a.fetchSessionFromOpenCode(id, limit, offset); ok {
		return detail, nil
	}

	// Fall back to DB.
	session, err := a.db.GetSession(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platforms.ErrNotFound
		}
		return nil, err
	}
	session.Platform = string(PlatformID)

	messages, err := a.db.GetSessionMessages(id)
	if err != nil {
		return nil, err
	}
	parts, err := a.db.GetSessionParts(id)
	if err != nil {
		return nil, err
	}

	totalMessages := len(messages)
	pagedMessages, _ := db.PaginateMessages(messages, limit, offset)
	filteredParts := db.FilterPartsForMessages(parts, pagedMessages)

	contextTokens, _ := a.db.GetContextTokenCount(id)
	defaults, _ := a.db.GetSessionDefaults(id, session.Directory)

	return &platforms.SessionDetail{
		Session:           session,
		Messages:          pagedMessages,
		Parts:             filteredParts,
		TotalMessages:     totalMessages,
		ContextTokenCount: contextTokens,
		DefaultAgent:      defaults.Agent, // composer-agent (OpenCode role), unchanged name
		DefaultModel:      defaults.Model,
	}, nil
}

// SessionsInactiveBefore returns OpenCode sessions last updated before the
// cutoff, for use by the auto-archive background job.
func (a *Adapter) SessionsInactiveBefore(_ context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error) {
	if a.db == nil {
		return nil, nil
	}
	return a.db.GetSessionsInactiveBefore(cutoff)
}

// LiveStatus returns nil: OpenCode uses on-demand port discovery rather
// than maintaining an in-memory hook-driven live-state cache. The server
// computes liveConnection/pending flags at Sessions() time using the
// discovered port map.
func (a *Adapter) LiveStatus(string) *platforms.LiveState { return nil }
