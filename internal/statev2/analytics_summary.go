package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/NoUseFreak/ocman/internal/analyticsdata"
)

// rebuildAnalyticsSummary replaces only this session's derived rows inside the
// ingestion transaction. Part-only updates also refresh timings; retries cannot
// double-count a bucket, and purges cascade through the session foreign keys.
func rebuildAnalyticsSummary(ctx context.Context, tx *sql.Tx, key Key) error {
	return refreshAnalyticsSummary(ctx, tx, key, nil)
}

func refreshAnalyticsSummary(ctx context.Context, tx *sql.Tx, key Key, ids []string) error {
	scope, err := analyticsScope(ctx, tx, key, ids)
	if err != nil {
		return err
	}
	for _, table := range []struct {
		name, filter string
		args         []any
	}{
		{"analytics_fact", scope.facts, scope.args}, {"analytics_question", scope.parts, scope.args}, {"analytics_hour", scope.hours, scope.hourArgs},
	} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table.name+` WHERE instance_id=? AND session_id=?`+table.filter, table.args...); err != nil {
			return err
		}
	}
	tools, err := summarizeScopedTools(ctx, tx, key, scope)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,time_created,data FROM analytics_message WHERE instance_id=? AND session_id=?`+scope.facts+` ORDER BY time_created,id`, scope.args...)
	if err != nil {
		return err
	}
	// Buffer the compact projection before writing on the same connection.
	type message struct {
		id   string
		at   int64
		data []byte
	}
	var messages []message
	for rows.Next() {
		var m message
		if err := rows.Scan(&m.id, &m.at, &m.data); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO analytics_fact VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range messages {
		var md analyticsdata.MessageData
		valid := json.Unmarshal(m.data, &md) == nil
		// Counts use the SQL role even when a different field makes the legacy
		// request decoder reject a row. Typed metrics/model readers use valid=1.
		role := md.Role
		provider, model := analyticsdata.ModelProvider(md)
		var in, out, read, write, started, completed, duration int64
		if md.Tokens != nil {
			in, out = md.Tokens.Input, md.Tokens.Output
			if md.Tokens.Cache != nil {
				read, write = md.Tokens.Cache.Read, md.Tokens.Cache.Write
			}
		}
		if md.Time != nil {
			started, completed = md.Time.Created, md.Time.Completed
			if completed > started {
				duration = completed - started
			}
		}
		finish := strings.TrimSpace(md.Finish)
		errorText := strings.TrimSpace(string(md.Error))
		isError := finish == "error" || (errorText != "" && errorText != "null")
		timingStart := int64(0)
		if duration > 0 {
			timingStart = started
		}
		timing := analyticsdata.SummarizeTiming(timingStart, duration, out, finish, tools[m.id])
		if _, err := stmt.ExecContext(ctx, key.InstanceID, key.SessionID, m.id, m.at, role, provider, model, md.Agent, md.Cost,
			in, out, read, write, started, completed, duration, timing.AgentMs, timing.ToolMs, timing.UnknownMs, timing.TokensPerSecond, finish, isError, finish != "" || isError, valid); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_hour
		SELECT instance_id,session_id,(time_created/3600000)*3600000,role,provider,model,valid,
		min(time_created),max(time_created),count(*),sum(input_tokens),sum(output_tokens),sum(cache_read),sum(cache_write),sum(cost)
		FROM analytics_fact WHERE instance_id=? AND session_id=?`+strings.ReplaceAll(scope.hours, "bucket", "(time_created/3600000)*3600000")+`
		GROUP BY instance_id,session_id,3,role,provider,model,valid`, scope.hourArgs...); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO analytics_session
		(instance_id,id,parent_id,directory,title,time_created,time_updated,project_id,source_deleted,user_messages,input_tokens,output_tokens,cost,is_subagent)
		SELECT s.instance_id,s.id,s.parent_id,s.directory,s.title,s.created_at,s.updated_at,s.project_id,s.source_deleted,
		COALESCE(sum(f.role='user'),0),COALESCE(sum(CASE WHEN f.role='assistant' THEN f.input_tokens ELSE 0 END),0),
		COALESCE(sum(CASE WHEN f.role='assistant' THEN f.output_tokens ELSE 0 END),0),
		COALESCE(sum(CASE WHEN f.role='assistant' THEN f.cost ELSE 0 END),0),s.is_subagent
		FROM session s LEFT JOIN analytics_fact f ON f.instance_id=s.instance_id AND f.session_id=s.id
		`+strings.ReplaceAll(scope.facts, " AND id", " AND f.id")+`
		WHERE s.instance_id=? AND s.id=? GROUP BY s.instance_id,s.id
		ON CONFLICT(instance_id,id) DO UPDATE SET parent_id=excluded.parent_id,directory=excluded.directory,title=excluded.title,
		time_created=excluded.time_created,time_updated=excluded.time_updated,project_id=excluded.project_id,source_deleted=excluded.source_deleted,
		user_messages=excluded.user_messages+?,input_tokens=excluded.input_tokens+?,output_tokens=excluded.output_tokens+?,cost=excluded.cost+?,is_subagent=excluded.is_subagent`,
		append(append([]any{}, scope.args[2:]...), key.InstanceID, key.SessionID, scope.users, scope.input, scope.output, scope.cost)...)
	return err
}
