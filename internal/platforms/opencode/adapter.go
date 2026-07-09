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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
	"github.com/NoUseFreak/ocman/internal/state"
)

// PlatformID is the stable identifier used in URLs and state.db rows.
const PlatformID platforms.ID = "opencode"

// FavoritesReader is the subset of the ocman state DB the adapter
// needs to read model favorites. Kept as an interface so tests can
// pass a stub and so the adapter doesn't force a writable *state.DB
// on callers that only want the read side.
type FavoritesReader interface {
	ModelFavorites(platform string) ([]state.ModelFavorite, error)
}

// CostCalculator computes API-equivalent cost from token counts and a
// model id. Mirrors the same-named interface in internal/db/stats.go
// so the adapter can be tested without pulling in the real pricing
// table. A nil CostCalculator is permitted; the adapter falls back
// to whatever cost values the upstream messages carry, which is
// often zero for subscription-plan sessions.
type CostCalculator interface {
	CalcCost(modelID string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64
}

// Adapter implements platforms.Platform for OpenCode.
type Adapter struct {
	db        *db.DB
	favorites FavoritesReader
	pricing   CostCalculator
	// childLinks reads ocman's own MCP/worktree child->parent links
	// from state.db so pending prompts from those children bubble to
	// their parent (OpenCode never records a parent_id for them). Nil
	// when the favorites reader isn't a full state.db (tests).
	childLinks mcpParentLookup
}

// New returns a new OpenCode adapter backed by the given read-only DB.
// A nil DB is permitted so Available() can report absence cleanly.
// A nil favorites reader is also permitted; SessionModels will then
// skip the favorites merge step.
//
// Use NewWithPricing in production to pass a pricing table; New keeps
// the legacy two-arg shape for tests and call sites that don't need
// calculated cost.
func New(database *db.DB, favorites FavoritesReader) *Adapter {
	return &Adapter{db: database, favorites: favorites, childLinks: childLinksFrom(favorites)}
}

// NewWithPricing is like New but also wires in a pricing table used to
// estimate cost for assistant messages whose upstream `cost` field is
// zero (typical of subscription-plan sessions: the API was hit but the
// message metadata records cost=0).
func NewWithPricing(database *db.DB, favorites FavoritesReader, pricing CostCalculator) *Adapter {
	return &Adapter{db: database, favorites: favorites, pricing: pricing, childLinks: childLinksFrom(favorites)}
}

// childLinksFrom returns favorites as an mcpParentLookup when it also
// exposes ocman's child_sessions links (the production *state.DB does).
// Returns nil for a bare favorites stub so the bubble helper skips the
// MCP fallback cleanly.
func childLinksFrom(favorites FavoritesReader) mcpParentLookup {
	if l, ok := favorites.(mcpParentLookup); ok {
		return l
	}
	return nil
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
		ShellExec:         true,
		FileChanges:       true,
		SessionInfo:       true,
		AutoApprove:       true,
		PermissionRules:   true,
		// OpenCode only exposes an HTTP API when it's started with an
		// explicit --port flag. `--port 0` asks OpenCode to pick a free
		// port and advertises it via lsof, which ocman scans.
		LiveConnectionHint: "Start OpenCode with `opencode --port 0` so ocman can discover and connect to it.",
	}
}

// Sessions returns all OpenCode sessions, optionally filtered by directory
// and/or updated-after timestamp. In addition to Platform, the adapter
// populates LiveConnection, PendingPermission, and PendingQuestion by
// probing running OpenCode instances — the server package no longer
// needs to know about lsof or OpenCode HTTP endpoints to list sessions.
func (a *Adapter) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	if a.db == nil {
		return nil, nil
	}
	dbPhase := srvtiming.Begin(ctx, "db_get_sessions")
	sessions, err := getSessionsCached(a.db, dir, since)
	dbPhase.End()
	if err != nil {
		return nil, err
	}
	// The cached slice is shared across concurrent readers; the
	// per-session overlay below mutates entries by index, so we
	// need our own slice. A shallow copy is enough — Session is a
	// value type with no pointer-shared mutable state we'd care
	// about here.
	sessions = append([]db.Session(nil), sessions...)

	// Fan out to every running OpenCode instance to collect liveness
	// flags. Failures are silent — this is a best-effort UI hint.
	portsPhase := srvtiming.Begin(ctx, "lsof_ports")
	ports := discoverOpenCodePorts()
	portsPhase.End()

	promptsPhase := srvtiming.Begin(ctx, "pending_prompts")
	pendingPerms, pendingQuestions := collectPendingPromptsByDir(ports)
	promptsPhase.EndWithDesc(fmt.Sprintf("%d instances", len(ports)))

	// OpenCode emits subagent prompts with the subagent's session ID,
	// not the parent's. The listing only contains parent sessions
	// (subagents are filtered out by SQL), so we resolve each prompted
	// subagent to its parent and apply the flag there. Parent prompts
	// pass through unchanged (their id maps to themselves).
	bubblePhase := srvtiming.Begin(ctx, "bubble_parents")
	pendingPerms = bubbleUpPromptsToParent(pendingPerms, a.db, a.childLinks)
	pendingQuestions = bubbleUpPromptsToParent(pendingQuestions, a.db, a.childLinks)
	bubblePhase.End()

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

// bubbleUpPromptsToParent rewrites every key in `prompted` that is the
// ID of a child session to its parent session ID. Keys that already
// refer to a top-level session pass through unchanged. A nil/empty
// input returns an empty map.
//
// Two kinds of child are resolved:
//   - OpenCode Task subagents, via OpenCode's own session.parent_id
//     (read from the read-only OpenCode DB through `dbConn`).
//   - ocman MCP/worktree children, via ocman's own child_sessions
//     links (read from state.db through `mcpConn`). These have NO
//     OpenCode parent_id — since #268 they run on the shared project
//     instance in a worktree directory — so without this fallback their
//     pending-prompt flag maps to a session ID the directory-scoped
//     listing never contains and is silently dropped.
//
// This is what makes a child's permission/question prompt visible on
// the parent session row in the listing. OpenCode's parent_id wins when
// both lookups know a child (they point at the same parent in practice).
func bubbleUpPromptsToParent(prompted map[string]bool, dbConn parentLookup, mcpConn mcpParentLookup) map[string]bool {
	if len(prompted) == 0 {
		return prompted
	}
	ids := make([]string, 0, len(prompted))
	for id := range prompted {
		ids = append(ids, id)
	}

	parents := map[string]string{}
	if dbConn != nil {
		if p, err := dbConn.GetSessionParentIDs(ids); err == nil {
			parents = p
		}
	}
	// Fill any gaps with ocman's own MCP/worktree child links.
	if mcpConn != nil {
		if mcpParents, err := mcpConn.ChildSessionParents(); err == nil {
			for _, id := range ids {
				if _, ok := parents[id]; ok {
					continue // OpenCode parent_id wins
				}
				if parent, ok := mcpParents[state.Key{Platform: string(PlatformID), SessionID: id}]; ok && parent != "" {
					parents[id] = parent
				}
			}
		}
	}

	if len(parents) == 0 {
		return prompted
	}
	out := make(map[string]bool, len(prompted))
	for id := range prompted {
		if parent, ok := parents[id]; ok {
			out[parent] = true
		} else {
			out[id] = true
		}
	}
	return out
}

// parentLookup is the subset of *db.DB that bubbleUpPromptsToParent
// needs. Defined as an interface so the helper can be unit-tested
// without spinning up a SQLite database.
type parentLookup interface {
	GetSessionParentIDs(childIDs []string) (map[string]string, error)
}

// mcpParentLookup is the subset of *state.DB that resolves ocman's own
// MCP/worktree child->parent links. Defined as an interface so the
// bubble helper stays unit-testable and so a nil state.db (tests, or an
// adapter constructed without one) degrades gracefully.
type mcpParentLookup interface {
	ChildSessionParents() (map[state.Key]string, error)
}

// Owns reports whether this OpenCode session ID exists in the local
// OpenCode database. It does NOT touch the live HTTP API or the lsof
// port discovery path, so it's cheap enough to call from the
// registry's cold-cache fan-out without paying the multi-hundred-ms
// round-trip that Session would.
func (a *Adapter) Owns(_ context.Context, sessionID string) bool {
	if a.db == nil || sessionID == "" {
		return false
	}
	_, err := a.db.GetSession(sessionID)
	return err == nil
}

// Session returns full detail for one session. Prefers live OpenCode
// API data (for in-flight streams) with a fallback to the read-only
// DB. Both paths return the same typed SessionDetail shape.
func (a *Adapter) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	// Try live data from OpenCode first.
	livePhase := srvtiming.Begin(ctx, "live_path")
	detail, ok := a.fetchSessionFromOpenCodeCtx(ctx, id, limit, offset)
	livePhase.EndWithDesc("fetchSessionFromOpenCode (incl lsof + 2x HTTP)")
	if ok {
		return detail, nil
	}

	// Fall back to DB. Wrap the whole DB-read sequence in a single
	// phase so the trace shows one bar that ends when the entire
	// fallback finishes (success or any of the early-error returns).
	fallbackPhase := srvtiming.Begin(ctx, "db_fallback")
	session, err := a.db.GetSession(id)
	if err != nil {
		fallbackPhase.End()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, platforms.ErrNotFound
		}
		return nil, err
	}
	session.Platform = string(PlatformID)

	messages, err := a.db.GetSessionMessages(id)
	if err != nil {
		fallbackPhase.End()
		return nil, err
	}
	applySessionDetailMetadataFromMessages(session, messages)
	parts, err := a.db.GetSessionParts(id)
	if err != nil {
		fallbackPhase.End()
		return nil, err
	}

	totalMessages := len(messages)
	pagedMessages, _ := db.PaginateMessages(messages, limit, offset)
	filteredParts := db.FilterPartsForMessages(parts, pagedMessages)

	contextTokens, _ := a.db.GetContextTokenCount(id)
	defaults, _ := getSessionDefaultsCached(a.db, id, session.Directory)
	fallbackPhase.EndWithDesc("live path miss; full DB read")

	return &platforms.SessionDetail{
		Session:           session,
		Messages:          pagedMessages,
		Parts:             filteredParts,
		TotalMessages:     totalMessages,
		ContextTokenCount: contextTokens,
		DefaultAgent:      defaults.Agent, // composer-agent (OpenCode role), unchanged name
		DefaultModel:      defaults.Model,
		Warnings:          sessionWarningsForDirectory(session.Directory),
	}, nil
}

func applySessionDetailMetadataFromMessages(session *db.Session, messages []db.Message) {
	if session == nil {
		return
	}
	if len(messages) == 0 {
		session.Status = db.InferSessionStatus("", "", "", false)
		return
	}
	last := messages[len(messages)-1]
	var data struct {
		Role   string `json:"role"`
		Finish string `json:"finish"`
		Error  *struct {
			Name    string `json:"name"`
			Message string `json:"message"`
			Data    *struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(last.Data, &data); err != nil {
		return
	}
	lastErr := ""
	if data.Error != nil {
		lastErr = "true"
		session.LastErrorName = data.Error.Name
		if data.Error.Data != nil {
			session.LastErrorMessage = data.Error.Data.Message
		} else {
			session.LastErrorMessage = data.Error.Message
		}
		session.LastErrorAt = last.TimeCreated
	}
	session.Status = db.InferSessionStatus(data.Role, data.Finish, lastErr, false)
}

// SessionsInactiveBefore returns OpenCode sessions last updated before the
// cutoff, for use by the auto-archive background job.
func (a *Adapter) SessionsInactiveBefore(_ context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error) {
	if a.db == nil {
		return nil, nil
	}
	return a.db.GetSessionsInactiveBefore(cutoff)
}

// UnreadCounts implements platforms.UnreadCounter. For each
// (sessionID, cutoff) pair, it returns the number of messages in
// that OpenCode session with time_created > cutoff. Sessions with
// zero unread are omitted to keep the response compact.
//
// The OpenCode message table has a covering index on
// (session_id, time_created, id), so this is a pure index-only
// range scan. Called from applySessionState on every /api/sessions
// poll; budget is sub-10ms even on large databases.
func (a *Adapter) UnreadCounts(_ context.Context, cutoffs map[string]int64) (map[string]int, error) {
	if a.db == nil {
		return nil, nil
	}
	return a.db.MessageCountsSince(cutoffs)
}

// SessionChanges aggregates every file-touching tool call in a session
// into a per-file changes summary. See changes.go for the algorithm.
//
// Instrumented with split timing — the parts query is by far the most
// likely to dominate (it pulls every part for the session, which can
// be hundreds of rows of JSON-blob data) so we time it separately
// from the metadata fetch and the in-memory aggregation. The split
// landed alongside the optimization plan in docs/profiling.md (B5):
// a single sample showed this endpoint at 1.87s with no obvious cost
// driver, and we want a per-call breakdown rather than a guess.
func (a *Adapter) SessionChanges(_ context.Context, sessionID string) (*platforms.SessionChanges, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	defer timeIt("session_changes", logrus.Fields{"sessionID": sessionID})()

	partsStart := time.Now()
	parts, err := a.db.GetSessionParts(sessionID)
	partsDur := time.Since(partsStart)
	if err != nil {
		return nil, err
	}

	sessionStart := time.Now()
	session, err := a.db.GetSession(sessionID)
	sessionDur := time.Since(sessionStart)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	observeDuration("session_changes_db", partsDur+sessionDur, logrus.Fields{
		"sessionID":   sessionID,
		"parts_count": len(parts),
		"parts_ms":    partsDur.Milliseconds(),
		"session_ms":  sessionDur.Milliseconds(),
	})

	directory := ""
	if session != nil {
		directory = session.Directory
	}
	return aggregateChanges(sessionID, directory, parts), nil
}

// LiveStatus returns nil: OpenCode uses on-demand port discovery rather
// than maintaining an in-memory hook-driven live-state cache. The server
// computes liveConnection/pending flags at Sessions() time using the
// discovered port map.
func (a *Adapter) LiveStatus(string) *platforms.LiveState { return nil }
