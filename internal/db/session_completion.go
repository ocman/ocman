package db

// Walk a session's message index backwards to its last terminal assistant
// message. Tool steps and compaction summaries are not completed turns.
// Error envelopes can lack time.completed, so use their durable creation time.
const lastTurnCompletedAtSQL = `COALESCE((
	SELECT COALESCE(json_extract(m.data, '$.time.completed'), m.time_created)
	FROM message m
	WHERE m.session_id = s.id
		AND json_extract(m.data, '$.role') = 'assistant'
		AND COALESCE(json_extract(m.data, '$.summary'), 0) = 0
		AND (
			(json_extract(m.data, '$.time.completed') > 0
				AND json_extract(m.data, '$.finish') NOT IN ('', 'tool-calls', 'unknown'))
			OR json_extract(m.data, '$.error') IS NOT NULL
		)
	ORDER BY m.time_created DESC, m.id DESC
	LIMIT 1
), 0)`
