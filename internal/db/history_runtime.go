package db

func (d *DB) runIntervalsQuery() string {
	return `SELECT m.session_id,m.started_at,m.completed_at FROM history_fact m
			JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
			WHERE m.role='assistant' AND m.started_at>0 AND m.completed_at>m.started_at
			AND m.completed_at>? AND m.started_at<?`
}

func (d *DB) questionIntervalsQuery() string {
	return `SELECT m.session_id,p.start,p.end FROM history_fact m
			JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
			JOIN history_question p ON p.instance_id=m.instance_id AND p.session_id=m.session_id AND p.message_id=m.id
			WHERE m.completed_at>? AND m.started_at<?`
}
