package claudecode

import (
	"context"
	"sort"

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
		out = append(out, sessionFromParsed(pf))
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

	return &platforms.SessionDetail{
		Session:       sessionFromParsedPtr(pf),
		Messages:      pagedMessages,
		Parts:         pagedParts,
		TotalMessages: totalMessages,
	}, nil
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
func sessionFromParsed(pf *parsedFile) db.Session {
	title := pf.Title
	if title == "" {
		title = "(untitled Claude Code session)"
	}
	status := inferStatus(pf)
	return db.Session{
		ID:           pf.SessionID,
		Platform:     string(PlatformID),
		ProjectID:    pf.Cwd, // no separate projectID concept
		Title:        title,
		Directory:    pf.Cwd,
		TimeCreated:  pf.TimeCreated,
		TimeUpdated:  pf.TimeUpdated,
		MessageCount: pf.UserMessageCount,
		DurationMs:   pf.TimeUpdated - pf.TimeCreated,
		Status:       status,
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
