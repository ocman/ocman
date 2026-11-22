package statev2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type TextMatch struct {
	Key
	Platform, PartID, MessageID, Role, Snippet string
}

// SessionSearchIndexReady reports whether the background import has caught up.
func (d *DB) SessionSearchIndexReady(ctx context.Context) (bool, error) {
	var done bool
	err := d.reader().QueryRowContext(ctx, `SELECT done FROM session_search_progress WHERE id=1`).Scan(&done)
	return done, err
}

// SessionTextMatch is a session and one excerpt from its retained conversation.
type SessionTextMatch struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	UpdatedAt int64  `json:"timeUpdated"`
	Snippet   string `json:"snippet"`
}

// SearchSessionText returns at most 20 owner-local sessions, newest first.
// Limiting sessions instead of parts stops a long conversation crowding out others.
func (d *DB) SearchSessionText(ctx context.Context, owner, query string) ([]SessionTextMatch, error) {
	if owner == "" {
		return nil, fmt.Errorf("statev2: invalid session search scope")
	}
	needle := asciiLower(strings.TrimSpace(query))
	if utf8.RuneCountInString(needle) < 3 || strings.ContainsRune(needle, 0) {
		return []SessionTextMatch{}, nil
	}
	// Quote for FTS's query grammar as well as binding for SQL. "OR", quotes
	// and punctuation in user input are literal, not FTS operators.
	phrase := `"` + strings.ReplaceAll(needle, `"`, `""`) + `"`
	var broad bool
	if err := d.reader().QueryRowContext(ctx, `SELECT EXISTS(SELECT rowid FROM session_search_fts WHERE text MATCH ? LIMIT 1 OFFSET 256)`, phrase).Scan(&broad); err != nil {
		return nil, err
	}
	if broad {
		// A popular phrase may have thousands of postings. If the newest
		// sessions already fill the page, we don't need to rank older hits.
		// Bound the probe for unusually long sessions or a sparse recent window.
		probeCtx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
		matches, err := d.readSessionSearch(probeCtx, needle, recentSessionSearchSQL, owner, needle)
		timedOut := errors.Is(probeCtx.Err(), context.DeadlineExceeded)
		cancel()
		if err == nil && len(matches) == 20 {
			return matches, nil
		}
		if err != nil && !timedOut {
			return nil, err
		}
	}
	return d.readSessionSearch(ctx, needle, indexedSessionSearchSQL, phrase, owner)
}

const searchVisible = `p.source_deleted=0 AND p.kind='text' AND m.source_deleted=0
	AND m.conversation_visible=1 AND m.role IN ('user','assistant')`

const searchHitColumns = `d.instance_id,d.session_id,d.message_id,d.part_id,s.updated_at,
	row_number() OVER (PARTITION BY d.instance_id,d.session_id ORDER BY p.created_at DESC,d.message_id,d.part_id) AS n`

const searchPartJoins = `JOIN message m ON (m.instance_id,m.session_id,m.id)=(d.instance_id,d.session_id,d.message_id)
	JOIN part p ON (p.instance_id,p.session_id,p.message_id,p.id)=(d.instance_id,d.session_id,d.message_id,d.part_id)`

const indexedSessionSearchSQL = `WITH hits AS (
		SELECT ` + searchHitColumns + `
		FROM session_search_fts f
		CROSS JOIN session_search_doc d ON d.doc_id=f.rowid
		JOIN instance i ON i.id=d.instance_id
		JOIN session s ON (s.instance_id,s.id)=(d.instance_id,d.session_id)
		` + searchPartJoins + `
		WHERE f.text MATCH ? AND i.owner_id=? AND s.source_deleted=0 AND ` + searchVisible + `
	)` + searchResultsSQL

const recentSessionSearchSQL = `WITH recent AS MATERIALIZED (
		SELECT s.instance_id,s.id,s.updated_at FROM session s JOIN instance i ON i.id=s.instance_id
		WHERE i.owner_id=? AND s.source_deleted=0 ORDER BY s.updated_at DESC,s.id,s.instance_id LIMIT 32
	), hits AS (
		SELECT ` + searchHitColumns + ` FROM recent s
		CROSS JOIN session_search_doc d ON (d.instance_id,d.session_id)=(s.instance_id,s.id)
		` + searchPartJoins + `
		WHERE instr(lower(p.text),?)>0 AND ` + searchVisible + `
	)` + searchResultsSQL

const searchResultsSQL = `, best AS MATERIALIZED (
		SELECT * FROM hits WHERE n=1 ORDER BY updated_at DESC,session_id,instance_id LIMIT 20
	)
	SELECT b.session_id,s.title,s.directory,b.updated_at,p.text FROM best b
	JOIN session s ON (s.instance_id,s.id)=(b.instance_id,b.session_id)
	JOIN part p ON (p.instance_id,p.session_id,p.message_id,p.id)=(b.instance_id,b.session_id,b.message_id,b.part_id)
	ORDER BY b.updated_at DESC,b.session_id,b.instance_id`

func (d *DB) readSessionSearch(ctx context.Context, needle, query string, args ...any) ([]SessionTextMatch, error) {
	rows, err := d.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionTextMatch{}
	for rows.Next() {
		var match SessionTextMatch
		if err := rows.Scan(&match.ID, &match.Title, &match.Directory, &match.UpdatedAt, &match.Snippet); err != nil {
			return nil, err
		}
		match.Snippet = searchSnippet(match.Snippet, needle)
		out = append(out, match)
	}
	return out, rows.Err()
}

// SearchText searches owner-local retained user/assistant prose, never native
// databases, tool payloads, deleted records or another owner's history.
func (d *DB) SearchText(ctx context.Context, owner, query, directory string, since int64, limit int) ([]TextMatch, error) {
	if owner == "" || limit < 1 || limit > 500 {
		return nil, fmt.Errorf("statev2: invalid text search scope")
	}
	needle := asciiLower(strings.TrimSpace(query))
	if needle == "" {
		return []TextMatch{}, nil
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT i.id,i.platform,s.id,p.id,p.message_id,m.role,p.text
		FROM instance i JOIN session s ON s.instance_id=i.id
		JOIN message m ON m.instance_id=s.instance_id AND m.session_id=s.id
		JOIN part p ON p.instance_id=m.instance_id AND p.session_id=m.session_id AND p.message_id=m.id
		WHERE i.owner_id=? AND s.source_deleted=0 AND m.source_deleted=0 AND p.source_deleted=0
		AND m.role IN ('user','assistant') AND p.kind='text'
		AND s.updated_at>=? AND (?='' OR s.directory=?) AND instr(lower(p.text),?)>0
		ORDER BY s.updated_at DESC,p.created_at,i.id,s.id,m.position,p.position,p.id LIMIT ?`, owner, since, directory, directory, needle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TextMatch{}
	for rows.Next() {
		var match TextMatch
		var text string
		if err := rows.Scan(&match.InstanceID, &match.Platform, &match.SessionID, &match.PartID, &match.MessageID, &match.Role, &text); err != nil {
			return nil, err
		}
		match.Snippet = searchSnippet(text, needle)
		out = append(out, match)
	}
	return out, rows.Err()
}

// asciiLower mirrors SQLite lower: ASCII only, preserving byte offsets.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// searchSnippet bounds surrounding prose without splitting UTF-8 runes.
func searchSnippet(text, needle string) string {
	i := max(strings.Index(asciiLower(text), needle), 0)
	start, end := max(i-80, 0), min(i+len(needle)+80, len(text))
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	out := strings.Join(strings.Fields(text[start:end]), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}
