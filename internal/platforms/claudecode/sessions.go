package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Sessions scans the projects directory, parses each jsonl's head,
// and returns one db.Session per file. Directory filter and `since`
// are applied post-parse: the scan is cheaper than trying to push
// filters into the filesystem walk.
func (a *Adapter) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	if a.projectsDir == "" {
		return nil, nil
	}
	files, err := scanSessionFiles(a.projectsDir)
	if err != nil {
		return nil, err
	}
	parsed := parseAllHeads(files, a.cache)

	out := make([]db.Session, 0, len(parsed))
	for _, pf := range parsed {
		if pf == nil || pf.SessionID == "" {
			continue
		}
		if dir != "" && pf.Cwd != dir {
			continue
		}
		if since > 0 && pf.TimeUpdated < since {
			continue
		}
		s := sessionFromParsed(pf)
		// Overlay hook-driven live-state on top of the static
		// jsonl-derived session. Only applies when the user has
		// actually invoked a Claude Code hook against this session;
		// otherwise we keep the conservative "done" default from
		// sessionFromParsed. See Phase 5 in the architecture doc.
		applyLiveState(&s, a.live)
		out = append(out, s)
	}
	// Mirror OpenCode's list ordering: newest first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].TimeUpdated > out[j].TimeUpdated
	})

	// Ignore context for now — Phase 4's scan is fast enough not to
	// need cancellation on a single request. Wire it in if/when the
	// dataset grows.
	_ = ctx
	return out, nil
}

// Owns reports whether this Claude Code session ID corresponds to a
// jsonl on disk. It walks projectsDir/<project>/<id>.jsonl with
// os.Stat — no parsing, no live state, and short-circuits on the
// first hit. Cheap enough for the registry's cold-cache fan-out.
func (a *Adapter) Owns(_ context.Context, sessionID string) bool {
	if a.projectsDir == "" || sessionID == "" {
		return false
	}
	// Reject obviously bogus IDs to avoid stat-walking the whole
	// projects tree on garbage input. Real Claude Code session IDs
	// are filename-safe UUIDs; anything with a path separator is
	// definitely not one of ours.
	if strings.ContainsAny(sessionID, `/\`) {
		return false
	}
	projects, err := os.ReadDir(a.projectsDir)
	if err != nil {
		return false
	}
	target := sessionID + ".jsonl"
	for _, pe := range projects {
		if !pe.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(a.projectsDir, pe.Name(), target)); err == nil {
			return true
		}
	}
	return false
}

// Session returns full detail for one session. Pagination is applied
// by taking a window from the end of the messages slice, matching
// OpenCode's PaginateMessages semantics.
func (a *Adapter) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	_ = ctx
	if a.projectsDir == "" {
		return nil, platforms.ErrNotFound
	}

	// Locate the jsonl for this session ID. We have to scan — the
	// directory-encoded cwd might collide if the user has two
	// differently-cased project paths, and even if not, we don't
	// store session-id -> path anywhere.
	files, err := scanSessionFiles(a.projectsDir)
	if err != nil {
		return nil, err
	}
	var target *jsonlFile
	for i := range files {
		// Cheap path-only check: the session ID is the filename.
		// Matches both `<uuid>.jsonl` and (defensively) files with
		// extra prefixes, should the layout ever change.
		if fileStem(files[i].path) == id {
			target = &files[i]
			break
		}
	}
	if target == nil {
		return nil, platforms.ErrNotFound
	}

	pf, err := loadFull(*target, a.cache)
	if err != nil {
		log.WithFields(log.Fields{"path": target.path, "error": err}).
			Warn("claudecode: parsing session detail")
		return nil, err
	}

	totalMessages := len(pf.Messages)
	pagedMessages, _ := db.PaginateMessages(pf.Messages, limit, offset)
	pagedParts := db.FilterPartsForMessages(pf.Parts, pagedMessages)

	detailSession := sessionFromParsedPtr(pf)
	// Same overlay as Sessions(): if a hook event has been observed
	// for this session, prefer its status / pending flags over the
	// static jsonl-derived defaults.
	applyLiveState(detailSession, a.live)

	// Attach the live-tool list (hook-driven) to the most recent
	// running Task tool_use part. This is what makes the UI render
	// "↳ Read /path/to/file" under a still-running subagent Task.
	if a.live != nil {
		if ls := a.live.Get(id); ls != nil && len(ls.CurrentTools) > 0 {
			pagedParts = attachLiveToolsToRunningTask(pagedParts, ls.CurrentTools)
		}
	}

	return &platforms.SessionDetail{
		Session:       detailSession,
		Messages:      pagedMessages,
		Parts:         pagedParts,
		TotalMessages: totalMessages,
	}, nil
}

// attachLiveToolsToRunningTask injects the live-tool list into the
// state.metadata.liveTools field of any running Task tool_use part in
// parts. Returns a new slice with mutated Data for the relevant
// parts; other parts are pass-through.
//
// Scope decision: we attach the full list to EVERY running Task
// part, not filtered by sub-agent. That's because the sub-agent IDs
// we derive from hook transcript paths don't correspond to any
// identifier visible on the parent Task tool_use; matching them up
// would require reading the sub-agent jsonl file (which this minimal
// increment deliberately avoids). The frontend filters/deduplicates
// if there are multiple concurrent Tasks; the common case is one
// Task at a time.
func attachLiveToolsToRunningTask(parts []db.Part, tools []platforms.LiveTool) []db.Part {
	if len(parts) == 0 || len(tools) == 0 {
		return parts
	}
	out := make([]db.Part, len(parts))
	copy(out, parts)

	// Walk from the end — the latest running Task is the one the
	// user is watching. Only inject into the most recent match so
	// older completed Tasks don't flicker back to "running".
	for i := len(out) - 1; i >= 0; i-- {
		var probe struct {
			Type  string          `json:"type"`
			Tool  string          `json:"tool"`
			State json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal(out[i].Data, &probe); err != nil {
			continue
		}
		if probe.Type != "tool" {
			continue
		}
		if probe.Tool != "Task" && probe.Tool != "task" && probe.Tool != "mcp_Task" && probe.Tool != "mcp_task" {
			continue
		}
		var state map[string]interface{}
		if err := json.Unmarshal(probe.State, &state); err != nil || state == nil {
			continue
		}
		if status, _ := state["status"].(string); status != "running" {
			continue
		}
		meta, _ := state["metadata"].(map[string]interface{})
		if meta == nil {
			meta = map[string]interface{}{}
		}
		meta["liveTools"] = tools
		state["metadata"] = meta
		var full map[string]interface{}
		if err := json.Unmarshal(out[i].Data, &full); err != nil {
			continue
		}
		full["state"] = state
		replacement, err := json.Marshal(full)
		if err != nil {
			continue
		}
		out[i].Data = replacement
		return out
	}
	return out
}

// applyLiveState overlays hook-driven live state onto a db.Session.
// No-op when the cache has no entry for the session, or when live is
// nil (defensive — Adapter.New always initialises live, but tests
// occasionally construct bare Adapter{} values).
//
// Also flips LiveConnection=true when there IS a recent hook event,
// because the presence of a hook proves a running Claude Code CLI.
// Without this, the dashboard would keep showing "no live connection"
// even while we're receiving active status updates.
func applyLiveState(s *db.Session, live *liveCache) {
	if s == nil || live == nil {
		return
	}
	ls := live.Get(s.ID)
	if ls == nil {
		return
	}
	if ls.Status != "" {
		s.Status = ls.Status
	}
	if ls.PendingPermission {
		s.PendingPermission = true
	}
	s.LiveConnection = true
}

// fileStem returns the filename without extension, stripped of any
// directory components. Equivalent to filepath.Base + trim suffix.
func fileStem(path string) string {
	base := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			base = path[i+1:]
			break
		}
	}
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			return base[:i]
		}
	}
	return base
}

// sessionFromParsed converts a parsedFile head into a db.Session.
// Claude Code has no concept of per-session token totals, cost, or
// share URL; those fields stay zero / nil. See FR-14 in the
// requirements.
//
// LiveConnection defaults to true for every Claude Code session
// that exists on disk: the composer resumes via `claude -p` without
// needing a running TUI, so "reachable" is always true as long as
// the jsonl is parseable. The hook-driven overlay in applyLiveState
// doesn't downgrade this — it can only set it (redundantly) true.
func sessionFromParsed(pf *parsedFile) db.Session {
	title := pf.Title
	if title == "" {
		title = "(untitled Claude Code session)"
	}
	status := inferStatus(pf)
	return db.Session{
		ID:             pf.SessionID,
		Platform:       string(PlatformID),
		ProjectID:      pf.Cwd, // no separate projectID concept
		Title:          title,
		Directory:      pf.Cwd,
		TimeCreated:    pf.TimeCreated,
		TimeUpdated:    pf.TimeUpdated,
		MessageCount:   pf.UserMessageCount,
		DurationMs:     pf.TimeUpdated - pf.TimeCreated,
		Status:         status,
		LiveConnection: true,
	}
}

func sessionFromParsedPtr(pf *parsedFile) *db.Session {
	s := sessionFromParsed(pf)
	return &s
}

// inferStatus is a Phase-4 placeholder for session status. With
// hooks unavailable we can't distinguish "busy" from "waiting";
// everything non-empty falls back to "done". Phase 5 replaces this
// with hook-driven live status.
func inferStatus(pf *parsedFile) string {
	if pf.UserMessageCount == 0 {
		return "done"
	}
	return "done"
}
