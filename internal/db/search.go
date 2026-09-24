package db

import (
	"context"
	"strings"
	"unicode/utf8"
)

// TextMatch is one text part whose prose contains a searched string.
type TextMatch struct {
	Platform  string `json:"-"` // set by the caller; the DB doesn't know its adapter ID
	SessionID string `json:"-"`
	PartID    string `json:"partId"`
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
	Snippet   string `json:"snippet"`
}

const snippetContext = 80

// SearchSessionText finds user and assistant text parts containing query
// (ASCII case-insensitive) in sessions updated at or after since (unix ms).
// Tool input/output is deliberately excluded.
//
// ponytail: full scan of the window's parts with no index; ~45s for a week
// on a 27 GB database. Add an FTS5 index in state.db if this must be fast.
func (d *DB) SearchSessionText(ctx context.Context, query, directory string, since int64, limit int) ([]TextMatch, error) {
	needle := asciiLower(strings.TrimSpace(query))
	rows, err := d.db.QueryContext(ctx, `
		SELECT p.session_id, p.id, p.message_id, COALESCE(json_extract(m.data, '$.role'), ''), json_extract(p.data, '$.text')
		FROM session s
		JOIN part p ON p.session_id = s.id
		JOIN message m ON m.id = p.message_id
		WHERE s.time_updated >= ? AND (? = '' OR s.directory = ?)
		  AND json_extract(p.data, '$.type') = 'text'
		  AND instr(lower(json_extract(p.data, '$.text')), ?) > 0
		ORDER BY s.time_updated DESC, p.time_created
		LIMIT ?
	`, since, directory, directory, needle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []TextMatch
	for rows.Next() {
		var m TextMatch
		var text string
		if err := rows.Scan(&m.SessionID, &m.PartID, &m.MessageID, &m.Role, &text); err != nil {
			return nil, err
		}
		m.Snippet = snippet(text, needle)
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// snippet returns the text around the first occurrence of needle, trimmed to
// rune boundaries, with ellipses where it was cut.
func snippet(text, needle string) string {
	i := strings.Index(asciiLower(text), needle)
	if i < 0 {
		i = 0
	}
	start, end := max(i-snippetContext, 0), min(i+len(needle)+snippetContext, len(text))
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

// asciiLower mirrors SQLite's lower(): ASCII only, so byte offsets match.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
