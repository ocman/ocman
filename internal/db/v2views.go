package db

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"sync"

	sqlite "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// OpenCode v2 stopped writing the v1 `session`, `message` and `part`
// tables: history lives in `session_v2` and `session_message`, with tool
// calls inside the assistant message. Rather than fork every query,
// connections to a v2 database get TEMP views with the v1 names. TEMP
// objects shadow `main` tables of the same name, need no write access
// to the file, and are rebuilt per connection. The JSON is produced by
// ocv2.ConvertMessage, the converter the HTTP layer uses, so ids agree
// between the database and the live API.
//
// ponytail: every query over these views re-converts message JSON in Go.
// Fine for typical databases; materialise into the analytics mirror if a
// large v2 history makes the list query slow.

var v2DSNs sync.Map // DSN -> true for databases opened with v2 views

const v2Visible = `('user','synthetic','assistant','shell','compaction')`

var v2ViewDDL = []string{
	`CREATE TEMP VIEW IF NOT EXISTS session AS SELECT id, project_id, workspace_id, parent_id, slug,
		directory, path, coalesce(title, '') AS title, version, share_url, summary_additions,
		summary_deletions, summary_files, summary_diffs, metadata, cost, tokens_input, tokens_output,
		tokens_reasoning, tokens_cache_read, tokens_cache_write, revert, permission, agent, model,
		time_created, time_updated, time_compacting, time_archived
		FROM main.session_v2`,
	`CREATE TEMP VIEW IF NOT EXISTS message AS SELECT m.id, m.session_id, m.time_created, m.time_updated,
		ocman_v1_message(m.id, m.session_id, m.type, m.data) AS data
		FROM main.session_message m WHERE m.type IN ` + v2Visible,
	`CREATE TEMP VIEW IF NOT EXISTS part AS SELECT json_extract(j.value, '$.id') AS id, m.id AS message_id,
		m.session_id, m.time_created, m.time_updated, j.value AS data
		FROM main.session_message m, json_each(ocman_v1_parts(m.id, m.session_id, m.type, m.data)) j
		WHERE m.type IN ` + v2Visible,
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("ocman_v1_message", 4, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		v1, ok := convertRow(args)
		if !ok {
			return nil, nil
		}
		info := v1.Info
		delete(info, "id")
		delete(info, "sessionID")
		b, err := json.Marshal(info)
		return string(b), err
	})
	sqlite.MustRegisterDeterministicScalarFunction("ocman_v1_parts", 4, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		v1, _ := convertRow(args)
		parts := v1.Parts
		if parts == nil {
			parts = []map[string]any{}
		}
		b, err := json.Marshal(parts)
		return string(b), err
	})
	sqlite.RegisterConnectionHook(func(c sqlite.ExecQuerierContext, dsn string) error {
		if _, ok := v2DSNs.Load(dsn); !ok {
			return nil
		}
		ok, err := hasTable(c, "session_v2")
		if err != nil || !ok {
			return err // not migrated yet: a later connection picks the views up
		}
		for _, ddl := range v2ViewDDL {
			if _, err := c.ExecContext(context.Background(), ddl, nil); err != nil {
				return fmt.Errorf("creating OpenCode v2 view: %w", err)
			}
		}
		return nil
	})
}

// convertRow decodes (id, session_id, type, data) into a v1 message.
func convertRow(args []driver.Value) (ocv2.V1Message, bool) {
	text := func(v driver.Value) string {
		switch s := v.(type) {
		case string:
			return s
		case []byte:
			return string(s)
		}
		return ""
	}
	var msg map[string]any
	if json.Unmarshal([]byte(text(args[3])), &msg) != nil || msg == nil {
		return ocv2.V1Message{}, false
	}
	msg["id"], msg["type"] = text(args[0]), text(args[2])
	return ocv2.ConvertMessage(text(args[1]), msg)
}

func hasTable(c sqlite.ExecQuerierContext, name string) (bool, error) {
	rows, err := c.QueryContext(context.Background(),
		`SELECT 1 FROM main.sqlite_master WHERE type = 'table' AND name = ?`, []driver.NamedValue{{Ordinal: 1, Value: name}})
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(make([]driver.Value, 1)) == nil, nil
}

// useV2Views marks dsn so its connections get the v1 views over the v2
// schema. Called by Open when the installed OpenCode is v2.
func useV2Views(dsn string) { v2DSNs.Store(dsn, true) }
