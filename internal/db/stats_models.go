package db

import (
	"context"
	"encoding/json"
	"sort"

	log "github.com/sirupsen/logrus"
)

// GetModelUsage returns usage stats per model, optionally filtered by time
// window (since) and directory prefix (dir; see directoryWhere).
func (d *DB) GetModelUsage(ctx context.Context, since int64, dir string) ([]ModelUsage, error) {
	dirFrag, dirArgs := directoryWhere(dir)
	query := `SELECT m.data FROM message m`
	if dirFrag != "" {
		// Only join when scoping; preserves the existing query plan
		// for the unfiltered case.
		query += `
		JOIN session s ON s.id = m.session_id`
	}
	query += `
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND (? <= 0 OR m.time_created >= ?)`
	args := []interface{}{since, since}
	if dirFrag != "" {
		query += "\n		  AND " + dirFrag
		args = append(args, dirArgs...)
	}
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	modelMap := make(map[string]*ModelUsage)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			log.WithError(err).Warn("failed to scan model usage row")
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			log.WithError(err).Warn("failed to unmarshal message data for model usage")
			continue
		}
		provider, model := extractModelProvider(md)
		if model == "" {
			continue
		}
		key := modelKey(provider, model)
		mu, ok := modelMap[key]
		if !ok {
			mu = &ModelUsage{Provider: provider, Model: model}
			modelMap[key] = mu
		}
		mu.Count++
		if md.Tokens != nil {
			mu.TokensIn += md.Tokens.Input
			mu.TokensOut += md.Tokens.Output
			if md.Tokens.Cache != nil {
				mu.CacheRead += md.Tokens.Cache.Read
				mu.CacheWrite += md.Tokens.Cache.Write
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]ModelUsage, 0, len(modelMap))
	for _, mu := range modelMap {
		result = append(result, *mu)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Model < result[j].Model
	})
	return result, nil
}

// RecentModel is one entry in the recent-models signal used by the composer
// model picker. `LastUsed` is a Unix-millis timestamp of the newest message
// that used this model.
type RecentModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	LastUsed int64  `json:"lastUsed"`
}

// GetRecentModels returns the distinct `provider/model` pairs most recently
// used across the N latest sessions, newest-message-first. This is a cheap
// alternative to the full usage aggregation (`GetModelUsage`): it limits work
// to recent sessions (index-backed on `time_updated`) and uses the compound
// session/time_created index when joining to messages.
//
// `sessionLimit` caps how many recent sessions to sample (typical: 50);
// `maxResults` caps the final list size (typical: 5–10).
func (d *DB) GetRecentModels(ctx context.Context, sessionLimit, maxResults int) ([]RecentModel, error) {
	if sessionLimit <= 0 {
		sessionLimit = 50
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	// Per-model: max timestamp of an assistant message, scoped to the N most
	// recently-updated sessions. Rely on SQL for the grouping/sort — way
	// cheaper than decoding N JSON blobs in Go just to pluck out a string.
	rows, err := d.db.QueryContext(ctx, `
		SELECT
			COALESCE(
				NULLIF(json_extract(m.data, '$.providerID'), ''),
				NULLIF(json_extract(m.data, '$.model.providerID'), ''),
				''
			) AS provider,
			COALESCE(
				NULLIF(json_extract(m.data, '$.modelID'), ''),
				NULLIF(json_extract(m.data, '$.model.modelID'), ''),
				''
			) AS model,
			MAX(m.time_created) AS last_used
		FROM session s
		JOIN message m ON m.session_id = s.id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND s.id IN (SELECT id FROM session ORDER BY time_updated DESC LIMIT ?)
		GROUP BY provider, model
		HAVING model != ''
		ORDER BY last_used DESC
		LIMIT ?
	`, sessionLimit, maxResults)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]RecentModel, 0, maxResults)
	for rows.Next() {
		var rm RecentModel
		if err := rows.Scan(&rm.Provider, &rm.Model, &rm.LastUsed); err != nil {
			log.WithError(err).Warn("failed to scan recent model row")
			continue
		}
		result = append(result, rm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
