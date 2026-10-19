package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NoUseFreak/ocman/internal/telemetry"
	"modernc.org/sqlite"
)

var historyDSNs sync.Map // DSN -> quoted JSON scope for its connection-local views

func init() {
	sqlite.RegisterConnectionHook(func(c sqlite.ExecQuerierContext, dsn string) error {
		scope, ok := historyDSNs.Load(dsn)
		if !ok {
			return nil
		}
		filter := " IN (SELECT value FROM json_each(" + scope.(string) + "))"
		for _, ddl := range []string{
			`CREATE TEMP VIEW session AS SELECT * FROM main.analytics_session WHERE source_deleted=0 AND instance_id` + filter,
			`CREATE TEMP VIEW message AS SELECT a.instance_id,a.id,a.session_id,a.time_created,a.settled,a.data
			FROM main.analytics_message a JOIN main.session s ON s.instance_id=a.instance_id AND s.id=a.session_id
			WHERE s.source_deleted=0 AND a.instance_id` + filter,
			`CREATE TEMP VIEW part AS SELECT a.id,a.message_id,a.session_id,a.data
			FROM main.analytics_part a JOIN main.analytics_message m ON m.instance_id=a.instance_id AND m.session_id=a.session_id AND m.id=a.message_id
			JOIN main.session s ON s.instance_id=a.instance_id AND s.id=a.session_id
			WHERE s.source_deleted=0 AND a.instance_id` + filter,
		} {
			if _, err := c.ExecContext(context.Background(), ddl, nil); err != nil {
				return err
			}
		}
		// Summary-backed reads never join retained payload tables. Keep the old
		// message/part views only for explicit diagnostic comparisons.
		for _, table := range []string{"fact", "hour", "question"} {
			if _, err := c.ExecContext(context.Background(), `CREATE TEMP VIEW history_`+table+` AS SELECT * FROM main.analytics_`+table+` WHERE instance_id`+filter, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// OpenHistoryAnalytics reuses the existing analytics calculations over the
// atomic statev2 projection. The file is opened read-only; TEMP views exist
// on every pooled connection and never modify retained conversation tables.
func OpenHistoryAnalytics(path string, instances []string) (*DB, error) {
	if path == "" || len(instances) == 0 {
		return nil, fmt.Errorf("history analytics requires a file and source instances")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(instances)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)"}, "ocman_history_scope": {string(raw)}}
	u.RawQuery = q.Encode()
	dsn := u.String()
	historyDSNs.Store(dsn, "'"+strings.ReplaceAll(string(raw), "'", "''")+"'")
	conn, err := telemetry.OpenSQL(dsn, "history-analytics")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(maxOpenReadConns)
	conn.SetMaxIdleConns(maxIdleReadConns)
	conn.SetConnMaxLifetime(connMaxLifetime)
	conn.SetConnMaxIdleTime(connMaxIdleTime)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	return &DB{db: conn, historyScope: &historyScopeValidation{}}, nil
}

// ValidateHistoryScope fails closed when legacy-shaped IDs would collide.
// Storage is instance-scoped, but the existing response types cannot yet
// express duplicate native IDs. Reject rather than merge unrelated sessions.
func (d *DB) ValidateHistoryScope(ctx context.Context) error {
	_, release, err := d.ScopedHistoryReader(ctx)
	if err == nil {
		release()
	}
	return err
}

// ScopedHistoryReader validates the scope in one read transaction and returns a
// reader bound to it. Every query building a response must use that reader:
// a commit after a separate validation could add a colliding identity that
// the response would then merge. release ends the transaction.
func (d *DB) ScopedHistoryReader(ctx context.Context) (*DB, func(), error) {
	if d.historyScope == nil {
		return nil, nil, fmt.Errorf("history scope validation requires a history reader")
	}
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	if err := d.validateHistoryScope(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return &DB{db: d.db, historyScope: d.historyScope, snapshot: tx}, func() { _ = tx.Rollback() }, nil
}

func (d *DB) validateHistoryScope(ctx context.Context, tx *sql.Tx) error {
	d.historyScope.mu.Lock()
	defer d.historyScope.mu.Unlock()
	// Every content change and purge appends an outbox row atomically. The
	// first read fixes the transaction's snapshot, so its high-water mark is
	// the revision every later read in the transaction observes.
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM main.change_outbox`).Scan(&revision); err != nil {
		return err
	}
	if d.historyScope.valid && d.historyScope.revision == revision {
		return nil
	}
	for _, kind := range []string{"session", "message"} {
		var duplicates int
		from := `session f ON f.id=c.id`
		if kind == "message" {
			from = `history_fact f ON f.id=c.id JOIN session s ON s.instance_id=f.instance_id AND s.id=f.session_id`
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT f.id FROM main.analytics_conflict c CROSS JOIN `+from+` WHERE c.kind=? GROUP BY f.id HAVING count(*)>1 LIMIT 1)`, kind).Scan(&duplicates); err != nil {
			return err
		}
		if duplicates > 0 {
			return fmt.Errorf("history analytics native %s IDs collide across sources", kind)
		}
	}
	d.historyScope.valid, d.historyScope.revision = true, revision
	return nil
}

type historyScopeValidation struct {
	mu       sync.Mutex
	valid    bool
	revision int64
}
