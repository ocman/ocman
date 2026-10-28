package statev2

import (
	"context"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/textsearch"
)

type TextMatch struct {
	Key
	Platform, PartID, MessageID, Role, Snippet string
}

// SearchText searches owner-local retained user/assistant prose, never native
// databases, tool payloads, deleted records or another owner's history.
func (d *DB) SearchText(ctx context.Context, owner, query, directory string, since int64, limit int) ([]TextMatch, error) {
	if owner == "" || limit < 1 || limit > 500 {
		return nil, fmt.Errorf("statev2: invalid text search scope")
	}
	needle := textsearch.Lower(strings.TrimSpace(query))
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
		match.Snippet = textsearch.Snippet(text, needle)
		out = append(out, match)
	}
	return out, rows.Err()
}
