package mcp

import (
	"fmt"
	"slices"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/db"
)

var sessionOrigins = []string{"user", "routine", "plugin", "factory", "external"}

// sessionListFilter narrows list results before the limit is applied, so
// automated sessions cannot push matching sessions past the cap.
type sessionListFilter struct {
	since            int64
	include, exclude []string
	detail           bool
}

func parseSessionListFilter(req mcplib.CallToolRequest) (sessionListFilter, error) {
	var f sessionListFilter
	args := req.GetArguments()
	// The shared list/search validation has already bounded since_days.
	if _, ok := args["since_days"]; ok {
		f.since = time.Now().AddDate(0, 0, -req.GetInt("since_days", 7)).UnixMilli()
	}
	var err error
	if f.include, err = originList(args, "origin"); err != nil {
		return f, err
	}
	if f.exclude, err = originList(args, "exclude_origin"); err != nil {
		return f, err
	}
	if value, ok := args["detail"]; ok {
		if f.detail, ok = value.(bool); !ok {
			return f, fmt.Errorf("detail must be a boolean")
		}
	}
	return f, nil
}

func originList(args map[string]any, key string) ([]string, error) {
	value, ok := args[key]
	if !ok {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be a comma-separated string", key)
	}
	var out []string
	for _, origin := range strings.Split(text, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if !slices.Contains(sessionOrigins, origin) {
			return nil, fmt.Errorf("%s values must be %s", key, strings.Join(sessionOrigins, ", "))
		}
		out = append(out, origin)
	}
	return out, nil
}

// sessionOrigin falls back to legacy links for rows without catalogue origin.
func sessionOrigin(s db.Session) string {
	switch {
	case s.Origin != "":
		return s.Origin
	case s.RoutineID != "":
		return "routine"
	case s.FactoryAttemptID != "":
		return "factory"
	}
	return "user"
}

func (f sessionListFilter) apply(sessions []db.Session) []db.Session {
	if f.since == 0 && f.include == nil && f.exclude == nil {
		return sessions
	}
	out := make([]db.Session, 0, len(sessions))
	for _, s := range sessions {
		origin := sessionOrigin(s)
		if s.TimeUpdated < f.since || (f.include != nil && !slices.Contains(f.include, origin)) || slices.Contains(f.exclude, origin) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// compactSession is the default list row. Accounting and read-state fields
// make a 500-row list several hundred kilobytes; detail returns them.
type compactSession struct {
	ID          string           `json:"id"`
	Platform    string           `json:"platform"`
	Title       string           `json:"title"`
	Directory   string           `json:"directory"`
	Branch      string           `json:"branch,omitempty"`
	Status      db.SessionStatus `json:"status"`
	Origin      string           `json:"origin"`
	RoutineID   string           `json:"routineId,omitempty"`
	ParentID    string           `json:"parentId,omitempty"`
	TimeCreated int64            `json:"timeCreated"`
	TimeUpdated int64            `json:"timeUpdated"`
	Archived    bool             `json:"archived"`
	RemoteID    string           `json:"remoteId,omitempty"`
}

func compactSessions(sessions []db.Session) []compactSession {
	out := make([]compactSession, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, compactSession{ID: s.ID, Platform: s.Platform, Title: s.Title, Directory: s.Directory, Branch: s.Branch, Status: s.Status, Origin: sessionOrigin(s), RoutineID: s.RoutineID, ParentID: s.ParentID, TimeCreated: s.TimeCreated, TimeUpdated: s.TimeUpdated, Archived: s.Archived, RemoteID: s.RemoteID})
	}
	return out
}
