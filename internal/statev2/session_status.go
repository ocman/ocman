package statev2

import "context"

// SessionStatusSummary reads only the persisted inputs to status inference.
// It deliberately leaves accounting, models and conversation content unset.
// Only the latest visible message envelope is visited, regardless of history size.
func (d *DB) SessionStatusSummary(ctx context.Context, key Key) (SessionSummary, error) {
	s := SessionSummary{Session: Session{Key: key}}
	var ext string
	err := d.reader().QueryRowContext(ctx, `SELECT s.directory,s.extensions,COALESCE(m.role,''),COALESCE(m.finish,''),
		CASE WHEN m.has_error=1 THEN COALESCE(NULLIF(m.error_name,''),'true') ELSE '' END
		FROM session s LEFT JOIN message m ON m.instance_id=s.instance_id AND m.session_id=s.id AND m.id=(
			SELECT id FROM message WHERE instance_id=s.instance_id AND session_id=s.id AND source_deleted=0 AND conversation_visible=1 ORDER BY position DESC,id DESC LIMIT 1)
		WHERE s.instance_id=? AND s.id=?`, key.InstanceID, key.SessionID).
		Scan(&s.Directory, &ext, &s.Totals.Role, &s.Totals.Finish, &s.Totals.Error)
	s.Extensions = []byte(ext)
	return s, notFound(err)
}
