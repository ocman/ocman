package db

import (
	"database/sql"
	"encoding/json"
	"strings"

	log "github.com/sirupsen/logrus"
)

// effectiveCost returns the cost figure the dashboard should headline
// for a single request: the platform-reported cost when it's non-zero,
// otherwise the token-derived estimate. Summing this per request keeps
// subscription-plan sessions (reported $0) and API-priced sessions on a
// single comparable scale.
func effectiveCost(reported, estimated float64) float64 {
	if reported > 0 {
		return reported
	}
	return estimated
}

// directoryWhere builds a SQL fragment that scopes a query to a project
// subtree. The returned fragment is intended to be AND'd into an existing
// WHERE clause; the args go into the same parameter list.
//
// Behaviour (see spec/stats-project-filter/architecture.md, AD-7):
//
//   - Empty `dir` returns `("", nil)`. Callers append the fragment only when
//     non-empty so the unfiltered query plan is unchanged.
//   - Non-empty `dir` returns the two-predicate form
//     `(s.directory = ? OR s.directory LIKE ?)` with args `[dir, dir+"/%"]`.
//
// The two-predicate form (rather than a single `LIKE dir||'%'`) is what
// prevents the sibling-prefix trap: scope `/repo/foo` must NOT match
// `/repo/foobar`. Tested explicitly in stats_dir_filter_test.go.
//
// The fragment uses the alias `s` for the session table; queries that don't
// already alias their session join `s` should do so before applying it.
func directoryWhere(dir string) (string, []interface{}) {
	if dir == "" {
		return "", nil
	}
	return "(s.directory = ? OR s.directory LIKE ?)", []interface{}{dir, dir + "/%"}
}

// extractModelProvider returns the provider and model from a MessageData,
// handling the fallback from top-level fields to nested Model fields.
func extractModelProvider(md MessageData) (provider, model string) {
	provider = md.ProviderID
	model = md.ModelID
	if provider == "" && md.Model != nil {
		provider = md.Model.ProviderID
		model = md.Model.ModelID
	}
	return provider, model
}

// modelKey builds the canonical "provider/model" key, falling back to
// the bare model when provider is empty (and to "" when model is empty).
func modelKey(provider, model string) string {
	if model == "" {
		return ""
	}
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

// GetStats returns aggregate statistics using SQL aggregation.
func (d *DB) GetStats() (*Stats, error) {
	s := &Stats{}

	err := d.db.QueryRow(`SELECT count(*) FROM session`).Scan(&s.TotalSessions)
	if err != nil {
		return nil, err
	}
	err = d.db.QueryRow(`SELECT count(*) FROM message WHERE json_extract(data, '$.role') = 'user'`).Scan(&s.TotalMessages)
	if err != nil {
		return nil, err
	}
	err = d.db.QueryRow(`SELECT count(DISTINCT directory) FROM session`).Scan(&s.TotalProjects)
	if err != nil {
		return nil, err
	}

	// Aggregate tokens and cost via SQL instead of deserializing every message in Go.
	err = d.db.QueryRow(`
		SELECT
			COALESCE(SUM(COALESCE(json_extract(data, '$.tokens.input'), 0)), 0),
			COALESCE(SUM(COALESCE(json_extract(data, '$.tokens.output'), 0)), 0),
			COALESCE(SUM(COALESCE(json_extract(data, '$.cost'), 0)), 0)
		FROM message
		WHERE json_extract(data, '$.role') = 'assistant'
	`).Scan(&s.TotalTokensIn, &s.TotalTokensOut, &s.TotalCost)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// GetProjects returns all directories with aggregated stats using SQL aggregation.
//
// session_count excludes subagent sessions (titles like "... (xxx subagent)")
// so the figure matches the sessions list, which also hides them. Message,
// token and cost totals intentionally still include subagent activity
// because those represent real spend against the project.
func (d *DB) GetProjects() ([]ProjectStats, error) {
	rows, err := d.db.Query(`
		SELECT
			s.directory,
			SUM(CASE WHEN s.title NOT LIKE '%(% subagent)' THEN 1 ELSE 0 END) AS session_count,
			max(s.time_updated) AS last_used,
			COALESCE((
				SELECT count(*) FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'user'
			), 0) AS message_count,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(m.data, '$.tokens.input'), 0))
				FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'assistant'
			), 0) AS total_tokens_in,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(m.data, '$.tokens.output'), 0))
				FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'assistant'
			), 0) AS total_tokens_out,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(m.data, '$.cost'), 0))
				FROM message m
				JOIN session s2 ON m.session_id = s2.id
				WHERE s2.directory = s.directory
				AND json_extract(m.data, '$.role') = 'assistant'
			), 0) AS total_cost
		FROM session s
		GROUP BY s.directory
		ORDER BY last_used DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ProjectStats
	for rows.Next() {
		var ps ProjectStats
		var lastUsed sql.NullInt64
		err := rows.Scan(
			&ps.Directory,
			&ps.SessionCount,
			&lastUsed,
			&ps.MessageCount,
			&ps.TotalTokensIn,
			&ps.TotalTokensOut,
			&ps.TotalCost,
		)
		if err != nil {
			log.WithError(err).Warn("failed to scan project row")
			continue
		}
		if lastUsed.Valid {
			ps.LastUsed = lastUsed.Int64
		}
		results = append(results, ps)
	}
	if err := rows.Err(); err != nil {
		return results, err
	}
	return results, nil
}

// GetNewAssistantMessages returns assistant messages created after `since`
// (exclusive), ordered by time_created ASC. It also returns the new
// high-water mark (the max time_created seen, or `since` if no rows).
//
// This is the data source for the LLM metrics scanner: it runs every
// collection interval, feeds each row into OTel counters/histograms,
// and advances the high-water mark so the next call only sees new data.
func (d *DB) GetNewAssistantMessages(since int64) ([]LLMMessageRow, int64, error) {
	rows, err := d.db.Query(`
		SELECT m.time_created, m.session_id, m.data
		FROM message m
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND m.time_created > ?
		ORDER BY m.time_created ASC
	`, since)
	if err != nil {
		return nil, since, err
	}
	defer rows.Close()

	var result []LLMMessageRow
	hwm := since
	for rows.Next() {
		var tc int64
		var sessionID, raw string
		if err := rows.Scan(&tc, &sessionID, &raw); err != nil {
			log.WithError(err).Warn("failed to scan LLM message row")
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			log.WithError(err).Warn("failed to unmarshal LLM message data")
			continue
		}

		provider, model := extractModelProvider(md)
		mkey := modelKey(provider, model)

		r := LLMMessageRow{
			TimeCreated: tc,
			SessionID:   sessionID,
			Model:       mkey,
			Cost:        md.Cost,
		}
		if md.Tokens != nil {
			r.InputTokens = md.Tokens.Input
			r.OutputTokens = md.Tokens.Output
			if md.Tokens.Cache != nil {
				r.CacheReadTokens = md.Tokens.Cache.Read
				r.CacheWriteTokens = md.Tokens.Cache.Write
			}
		}
		r.StopReason = strings.TrimSpace(md.Finish)
		if r.StopReason == "" {
			r.StopReason = "none"
		}
		if md.Time != nil && md.Time.Completed > md.Time.Created {
			r.DurationMs = md.Time.Completed - md.Time.Created
		}

		result = append(result, r)
		if tc > hwm {
			hwm = tc
		}
	}
	if err := rows.Err(); err != nil {
		return nil, since, err
	}
	return result, hwm, nil
}

// GetMaxMessageTime returns the maximum time_created across all messages,
// or 0 if the table is empty. Used to initialise the LLM metrics scanner's
// high-water mark so it only emits metrics for messages arriving after
// ocman starts.
func (d *DB) GetMaxMessageTime() (int64, error) {
	var maxTime int64
	err := d.db.QueryRow(`SELECT COALESCE(MAX(time_created), 0) FROM message`).Scan(&maxTime)
	return maxTime, err
}
