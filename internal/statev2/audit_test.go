package statev2

import (
	"testing"
)

// change is one change_outbox row, read directly for assertions.
type change struct {
	Sequence int64
	Key
	Revision int64
	Kind     string
}

// read returns every retained row of a session, including source-deleted and
// conversation-hidden records, ordered like the production reader.
func read(t *testing.T, d *DB, key Key) *Detail {
	t.Helper()
	detail := &Detail{Messages: []Message{}, Parts: []Part{}}
	s := &detail.Session
	s.Key = key
	var extensions string
	if err := d.db.QueryRowContext(t.Context(), `SELECT title,directory,parent_id,project_id,created_at,updated_at,source_deleted,revision,extensions,read_format,is_subagent
		FROM session WHERE instance_id=? AND id=?`, key.InstanceID, key.SessionID).
		Scan(&s.Title, &s.Directory, &s.ParentID, &s.ProjectID, &s.CreatedAt, &s.UpdatedAt, &s.SourceDeleted, &s.Revision, &extensions, &s.ReadFormat, &s.Subagent); err != nil {
		t.Fatal(err)
	}
	s.Extensions = []byte(extensions)
	rows, err := d.db.QueryContext(t.Context(), `SELECT id,position,role,provider,model,created_at,completed_at,cost,tokens_input,tokens_output,
		tokens_reasoning,tokens_cache_read,tokens_cache_write,finish,has_error,error_name,agent,source_deleted,extensions,conversation_visible
		FROM message WHERE instance_id=? AND session_id=? ORDER BY position,id`, key.InstanceID, key.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var m Message
		var raw string
		if err := rows.Scan(&m.ID, &m.Position, &m.Role, &m.Provider, &m.Model, &m.CreatedAt, &m.CompletedAt, &m.Cost,
			&m.TokensInput, &m.TokensOutput, &m.TokensReasoning, &m.TokensCacheRead, &m.TokensCacheWrite, &m.Finish,
			&m.HasError, &m.ErrorName, &m.Agent, &m.SourceDeleted, &raw, &m.ConversationVisible); err != nil {
			t.Fatal(err)
		}
		m.Extensions = []byte(raw)
		detail.Messages = append(detail.Messages, m)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	detail.TotalMessages = len(detail.Messages)
	rows, err = d.db.QueryContext(t.Context(), `SELECT p.id,p.message_id,p.position,p.kind,p.text,p.source_deleted,p.extensions,p.created_at,p.text_present
		FROM part p JOIN message m ON m.instance_id=p.instance_id AND m.session_id=p.session_id AND m.id=p.message_id
		WHERE p.instance_id=? AND p.session_id=? ORDER BY m.position,m.id,p.position,p.id`, key.InstanceID, key.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p Part
		var raw string
		if err := rows.Scan(&p.ID, &p.MessageID, &p.Position, &p.Kind, &p.Text, &p.SourceDeleted, &raw, &p.CreatedAt, &p.TextPresent); err != nil {
			t.Fatal(err)
		}
		p.Extensions = []byte(raw)
		detail.Parts = append(detail.Parts, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return detail
}

func changes(t *testing.T, d *DB) []change {
	t.Helper()
	rows, err := d.db.QueryContext(t.Context(), `SELECT sequence,instance_id,session_id,revision,kind FROM change_outbox ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []change{}
	for rows.Next() {
		var c change
		if err := rows.Scan(&c.Sequence, &c.InstanceID, &c.SessionID, &c.Revision, &c.Kind); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
