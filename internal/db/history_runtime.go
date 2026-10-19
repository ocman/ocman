package db

func (d *DB) runIntervalsQuery() string {
	if d.historyScope != nil {
		return `SELECT m.session_id,m.started_at,m.completed_at FROM history_fact m
			JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
			WHERE m.role='assistant' AND m.started_at>0 AND m.completed_at>m.started_at
			AND m.completed_at>? AND m.started_at<?`
	}
	return `SELECT m.session_id,json_extract(m.data,'$.time.created'),json_extract(m.data,'$.time.completed')
		FROM message m JOIN session s ON s.id=m.session_id
		WHERE json_extract(m.data,'$.role')='assistant' AND json_extract(m.data,'$.time.created')>0
		AND json_extract(m.data,'$.time.completed')>json_extract(m.data,'$.time.created')
		AND json_extract(m.data,'$.time.completed')>? AND json_extract(m.data,'$.time.created')<?`
}

func (d *DB) questionIntervalsQuery(table, timeExpr, toolExpr string) string {
	if d.historyScope != nil {
		return `SELECT m.session_id,p.start,p.end FROM history_fact m
			JOIN session s ON s.instance_id=m.instance_id AND s.id=m.session_id
			JOIN history_question p ON p.instance_id=m.instance_id AND p.session_id=m.session_id AND p.message_id=m.id
			WHERE m.completed_at>? AND m.started_at<?`
	}
	return `SELECT m.session_id,json_extract(` + timeExpr + `,'$.start'),json_extract(` + timeExpr + `,'$.end')
		FROM ` + table + ` p JOIN message m ON m.id=p.message_id JOIN session s ON s.id=m.session_id
		WHERE ` + toolExpr + `='question' AND json_extract(m.data,'$.time.completed')>?
		AND json_extract(m.data,'$.time.created')<?`
}
