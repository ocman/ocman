package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Project represents an OpenCode project.
type Project struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Worktree string `json:"worktree"`
}

// Session represents an OpenCode session.
type Session struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"projectId"`
	Title             string  `json:"title"`
	Directory         string  `json:"directory"`
	TimeCreated       int64   `json:"timeCreated"`
	TimeUpdated       int64   `json:"timeUpdated"`
	SummaryAdditions  *int    `json:"summaryAdditions"`
	SummaryDeletions  *int    `json:"summaryDeletions"`
	SummaryFiles      *int    `json:"summaryFiles"`
	ShareURL          *string `json:"shareUrl"`
	MessageCount      int     `json:"messageCount"`
	DurationMs        int64   `json:"durationMs"`
	TotalInputTokens  int64   `json:"totalInputTokens"`
	TotalOutputTokens int64   `json:"totalOutputTokens"`
	TotalCost         float64 `json:"totalCost"`
	Status            string  `json:"status"`  // "waiting", "busy", or "done"
	HasPort           bool    `json:"hasPort"` // true if a running OpenCode instance with --port is detected
}

// Message represents an OpenCode message.
type Message struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"sessionId"`
	TimeCreated int64           `json:"timeCreated"`
	Data        json.RawMessage `json:"data"`
}

// MessageData is the parsed data from a message.
type MessageData struct {
	Role       string     `json:"role"`
	Agent      string     `json:"agent"`
	Mode       string     `json:"mode"`
	ModelID    string     `json:"modelID"`
	ProviderID string     `json:"providerID"`
	Cost       float64    `json:"cost"`
	Tokens     *TokenInfo `json:"tokens"`
	Time       *TimeInfo  `json:"time"`
	Model      *ModelRef  `json:"model"`
	Finish     string     `json:"finish"`
}

type TokenInfo struct {
	Input     int64      `json:"input"`
	Output    int64      `json:"output"`
	Reasoning int64      `json:"reasoning"`
	Cache     *CacheInfo `json:"cache"`
}

type CacheInfo struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}

type TimeInfo struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

type ModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

// Part represents a message part.
type Part struct {
	ID          string          `json:"id"`
	MessageID   string          `json:"messageId"`
	SessionID   string          `json:"sessionId"`
	TimeCreated int64           `json:"timeCreated"`
	Data        json.RawMessage `json:"data"`
}

// PartData is the parsed data from a part.
type PartData struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Tool string `json:"tool"`
}

// Stats holds aggregate statistics.
type Stats struct {
	TotalSessions  int     `json:"totalSessions"`
	TotalMessages  int     `json:"totalMessages"`
	TotalProjects  int     `json:"totalProjects"`
	TotalTokensIn  int64   `json:"totalTokensIn"`
	TotalTokensOut int64   `json:"totalTokensOut"`
	TotalCost      float64 `json:"totalCost"`
}

// ProjectStats holds per-directory aggregated data.
type ProjectStats struct {
	Directory     string  `json:"directory"`
	SessionCount  int     `json:"sessionCount"`
	MessageCount  int     `json:"messageCount"`
	LastUsed      int64   `json:"lastUsed"`
	TotalTokensIn int64   `json:"totalTokensIn"`
	TotalTokenOut int64   `json:"totalTokensOut"`
	TotalCost     float64 `json:"totalCost"`
}

// DailyActivity holds activity counts per day.
type DailyActivity struct {
	Date     string `json:"date"`
	Sessions int    `json:"sessions"`
	Messages int    `json:"messages"`
}

// ModelUsage holds per-model usage info.
type ModelUsage struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Count    int    `json:"count"`
	TokensIn int64  `json:"tokensIn"`
	TokenOut int64  `json:"tokensOut"`
}

// HourlyActivity holds activity counts per hour of day.
type HourlyActivity struct {
	Hour     int `json:"hour"`
	Sessions int `json:"sessions"`
}

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
}

// DefaultDBPath returns the default path to the OpenCode database.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// Open opens the OpenCode database in read-only mode.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// GetStats returns aggregate statistics.
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

	// Aggregate tokens and cost from messages
	rows, err := d.db.Query(`SELECT data FROM message WHERE json_extract(data, '$.role') = 'assistant'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			continue
		}
		s.TotalCost += md.Cost
		if md.Tokens != nil {
			s.TotalTokensIn += md.Tokens.Input
			s.TotalTokensOut += md.Tokens.Output
		}
	}
	return s, nil
}

// GetProjects returns all directories with aggregated stats.
func (d *DB) GetProjects() ([]ProjectStats, error) {
	rows, err := d.db.Query(`
		SELECT
			s.directory,
			count(s.id) as session_count,
			max(s.time_updated) as last_used
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
			&ps.SessionCount, &lastUsed,
		)
		if err != nil {
			continue
		}
		if lastUsed.Valid {
			ps.LastUsed = lastUsed.Int64
		}

		// Count user messages for this directory
		_ = d.db.QueryRow(`
			SELECT count(*) FROM message m
			JOIN session s ON m.session_id = s.id
			WHERE s.directory = ?
			AND json_extract(m.data, '$.role') = 'user'
		`, ps.Directory).Scan(&ps.MessageCount)

		// Get token stats from assistant messages
		msgRows, err := d.db.Query(`
			SELECT m.data FROM message m
			JOIN session s ON m.session_id = s.id
			WHERE s.directory = ?
			AND json_extract(m.data, '$.role') = 'assistant'
		`, ps.Directory)
		if err == nil {
			for msgRows.Next() {
				var raw string
				if err := msgRows.Scan(&raw); err != nil {
					continue
				}
				var md MessageData
				if err := json.Unmarshal([]byte(raw), &md); err != nil {
					continue
				}
				ps.TotalCost += md.Cost
				if md.Tokens != nil {
					ps.TotalTokensIn += md.Tokens.Input
					ps.TotalTokenOut += md.Tokens.Output
				}
			}
			msgRows.Close()
		}
		results = append(results, ps)
	}
	return results, nil
}

// GetSessions returns sessions, optionally filtered by directory and/or a minimum timestamp.
func (d *DB) GetSessions(directory string, since int64) ([]Session, error) {
	query := `
		SELECT
			s.id, s.project_id, s.title, s.directory,
			s.time_created, s.time_updated,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url
		FROM session s
	`
	var conditions []string
	var args []interface{}
	// Hide subagent sessions (titles ending with "(<something> subagent)")
	conditions = append(conditions, `s.title NOT LIKE '%(% subagent)'`)
	if directory != "" {
		conditions = append(conditions, `s.directory = ?`)
		args = append(args, directory)
	}
	if since > 0 {
		conditions = append(conditions, `s.time_updated >= ?`)
		args = append(args, since)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY s.time_updated DESC`

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Title, &s.Directory,
			&s.TimeCreated, &s.TimeUpdated,
			&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
			&s.ShareURL,
		)
		if err != nil {
			continue
		}
		s.DurationMs = s.TimeUpdated - s.TimeCreated

		// Count user messages for this session
		_ = d.db.QueryRow(`
			SELECT count(*) FROM message
			WHERE session_id = ? AND json_extract(data, '$.role') = 'user'
		`, s.ID).Scan(&s.MessageCount)

		// Get token stats from assistant messages
		msgRows, err := d.db.Query(`
			SELECT data FROM message
			WHERE session_id = ? AND json_extract(data, '$.role') = 'assistant'
		`, s.ID)
		if err == nil {
			for msgRows.Next() {
				var raw string
				if err := msgRows.Scan(&raw); err != nil {
					continue
				}
				var md MessageData
				if err := json.Unmarshal([]byte(raw), &md); err != nil {
					continue
				}
				s.TotalCost += md.Cost
				if md.Tokens != nil {
					s.TotalInputTokens += md.Tokens.Input
					s.TotalOutputTokens += md.Tokens.Output
				}
			}
			msgRows.Close()
		}

		// Determine session status based on the last message.
		// "waiting" = last assistant message finished with "stop" (needs user input)
		// "busy"    = last message is assistant but not finished (still processing)
		// "done"    = no messages or last message is from the user (session idle)
		var lastRole, lastFinish *string
		_ = d.db.QueryRow(`
			SELECT json_extract(data, '$.role'), json_extract(data, '$.finish')
			FROM message
			WHERE session_id = ?
			ORDER BY time_created DESC
			LIMIT 1
		`, s.ID).Scan(&lastRole, &lastFinish)

		if lastRole != nil && *lastRole == "assistant" {
			if lastFinish != nil && *lastFinish == "stop" {
				s.Status = "waiting"
			} else {
				s.Status = "busy"
			}
		} else {
			s.Status = "done"
		}

		sessions = append(sessions, s)
	}
	return sessions, nil
}

// GetSession returns a single session by ID.
func (d *DB) GetSession(sessionID string) (*Session, error) {
	var s Session
	err := d.db.QueryRow(`
		SELECT
			s.id, s.project_id, s.title, s.directory,
			s.time_created, s.time_updated,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url
		FROM session s WHERE s.id = ?
	`, sessionID).Scan(
		&s.ID, &s.ProjectID, &s.Title, &s.Directory,
		&s.TimeCreated, &s.TimeUpdated,
		&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
		&s.ShareURL,
	)
	if err != nil {
		return nil, err
	}
	s.DurationMs = s.TimeUpdated - s.TimeCreated
	return &s, nil
}

// GetSessionMessages returns all messages for a session.
func (d *DB) GetSessionMessages(sessionID string) ([]Message, error) {
	rows, err := d.db.Query(`
		SELECT id, session_id, time_created, data
		FROM message
		WHERE session_id = ?
		ORDER BY time_created
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var rawData string
		err := rows.Scan(&m.ID, &m.SessionID, &m.TimeCreated, &rawData)
		if err != nil {
			continue
		}
		m.Data = json.RawMessage(rawData)
		messages = append(messages, m)
	}
	return messages, nil
}

// GetSessionParts returns all parts for a session.
func (d *DB) GetSessionParts(sessionID string) ([]Part, error) {
	rows, err := d.db.Query(`
		SELECT id, message_id, session_id, time_created, data
		FROM part
		WHERE session_id = ?
		ORDER BY time_created
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parts []Part
	for rows.Next() {
		var p Part
		var rawData string
		err := rows.Scan(&p.ID, &p.MessageID, &p.SessionID, &p.TimeCreated, &rawData)
		if err != nil {
			continue
		}
		p.Data = json.RawMessage(rawData)
		parts = append(parts, p)
	}
	return parts, nil
}

// GetDailyActivity returns activity per day for the last N days.
func (d *DB) GetDailyActivity(days int) ([]DailyActivity, error) {
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()

	// Get session counts per day
	rows, err := d.db.Query(`
		SELECT
			date(time_created / 1000, 'unixepoch', 'localtime') as day,
			count(*) as sessions
		FROM session
		WHERE time_created >= ?
		GROUP BY day
		ORDER BY day
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dayMap := make(map[string]*DailyActivity)
	for rows.Next() {
		var da DailyActivity
		if err := rows.Scan(&da.Date, &da.Sessions); err != nil {
			continue
		}
		dayMap[da.Date] = &da
	}

	// Get message counts per day
	rows2, err := d.db.Query(`
		SELECT
			date(time_created / 1000, 'unixepoch', 'localtime') as day,
			count(*) as messages
		FROM message
		WHERE time_created >= ?
		GROUP BY day
		ORDER BY day
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()

	for rows2.Next() {
		var date string
		var messages int
		if err := rows2.Scan(&date, &messages); err != nil {
			continue
		}
		if da, ok := dayMap[date]; ok {
			da.Messages = messages
		} else {
			dayMap[date] = &DailyActivity{Date: date, Messages: messages}
		}
	}

	// Fill in gaps and sort
	var result []DailyActivity
	for i := days; i >= 0; i-- {
		d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		if da, ok := dayMap[d]; ok {
			result = append(result, *da)
		} else {
			result = append(result, DailyActivity{Date: d})
		}
	}
	return result, nil
}

// GetModelUsage returns usage stats per model.
func (d *DB) GetModelUsage() ([]ModelUsage, error) {
	rows, err := d.db.Query(`
		SELECT data FROM message
		WHERE json_extract(data, '$.role') = 'assistant'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	modelMap := make(map[string]*ModelUsage)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var md MessageData
		if err := json.Unmarshal([]byte(raw), &md); err != nil {
			continue
		}
		provider := md.ProviderID
		model := md.ModelID
		if provider == "" && md.Model != nil {
			provider = md.Model.ProviderID
			model = md.Model.ModelID
		}
		if model == "" {
			continue
		}
		key := provider + "/" + model
		mu, ok := modelMap[key]
		if !ok {
			mu = &ModelUsage{Provider: provider, Model: model}
			modelMap[key] = mu
		}
		mu.Count++
		if md.Tokens != nil {
			mu.TokensIn += md.Tokens.Input
			mu.TokenOut += md.Tokens.Output
		}
	}

	var result []ModelUsage
	for _, mu := range modelMap {
		result = append(result, *mu)
	}
	return result, nil
}

// GetHourlyActivity returns session counts per hour of day.
func (d *DB) GetHourlyActivity() ([]HourlyActivity, error) {
	rows, err := d.db.Query(`
		SELECT
			cast(strftime('%H', time_created / 1000, 'unixepoch', 'localtime') as integer) as hour,
			count(*) as sessions
		FROM session
		GROUP BY hour
		ORDER BY hour
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hourMap := make(map[int]int)
	for rows.Next() {
		var hour, count int
		if err := rows.Scan(&hour, &count); err != nil {
			continue
		}
		hourMap[hour] = count
	}

	var result []HourlyActivity
	for h := 0; h < 24; h++ {
		result = append(result, HourlyActivity{Hour: h, Sessions: hourMap[h]})
	}
	return result, nil
}
