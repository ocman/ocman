package claudeprovider

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion bumps wipe the index; it is disposable and has no migrations.
const schemaVersion = 3

const schema = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE session (
	path TEXT PRIMARY KEY, id TEXT NOT NULL UNIQUE, root_id TEXT NOT NULL,
	parent_id TEXT NOT NULL DEFAULT '', parent_tool_use_id TEXT NOT NULL,
	mtime INTEGER NOT NULL, size INTEGER NOT NULL, end_offset INTEGER NOT NULL,
	title TEXT NOT NULL, directory TEXT NOT NULL, model TEXT NOT NULL, inferred TEXT NOT NULL,
	time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
	input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
	latest_id TEXT NOT NULL, latest_role TEXT NOT NULL, latest_created INTEGER NOT NULL,
	message_count INTEGER NOT NULL);
CREATE INDEX session_root ON session(root_id);
CREATE TABLE message (session_id TEXT NOT NULL, idx INTEGER NOT NULL, offset INTEGER NOT NULL,
	PRIMARY KEY (session_id, idx)) WITHOUT ROWID;
CREATE TABLE task_call (tool_use_id TEXT PRIMARY KEY, session_id TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE usage (session_id TEXT NOT NULL, id TEXT NOT NULL, time_created INTEGER NOT NULL,
	settled INTEGER NOT NULL, data TEXT NOT NULL, tools TEXT NOT NULL, indexed INTEGER NOT NULL,
	PRIMARY KEY (session_id, id)) WITHOUT ROWID;
CREATE INDEX usage_time ON usage(time_created);
CREATE INDEX usage_indexed ON usage(indexed);`

const entryColumns = `path, id, root_id, parent_id, parent_tool_use_id, mtime, size, end_offset,
	title, directory, model, inferred, time_created, time_updated, input_tokens, output_tokens,
	latest_id, latest_role, latest_created, message_count`

// Index is the disposable transcript index (claude-index.db). Rows are keyed
// by transcript path and invalidated by (mtime, size).
type Index struct {
	db       *sql.DB
	projects string
	mu       sync.Mutex // serializes writers
	watch    func(dir string)

	parsed    atomic.Int64 // files parsed, for the warm-start test
	bytesRead atomic.Int64 // transcript bytes read by paging, for its test
}

// OpenIndex opens or rebuilds the index for configDir (~/.claude or
// CLAUDE_CONFIG_DIR). A schema change or another config dir wipes it.
func OpenIndex(path, configDir string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	var version int
	var have string
	_ = conn.QueryRow(`PRAGMA user_version`).Scan(&version)
	_ = conn.QueryRow(`SELECT value FROM meta WHERE key = 'config_dir'`).Scan(&have)
	if version != schemaVersion || have != configDir {
		_, err = conn.Exec(`DROP TABLE IF EXISTS meta; DROP TABLE IF EXISTS session; DROP TABLE IF EXISTS message; DROP TABLE IF EXISTS task_call; DROP TABLE IF EXISTS usage;` +
			schema + fmt.Sprintf(`PRAGMA user_version = %d;`, schemaVersion))
		if err == nil {
			_, err = conn.Exec(`INSERT INTO meta (key, value) VALUES ('config_dir', ?)`, configDir)
		}
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("rebuilding claude index: %w", err)
		}
	}
	return &Index{db: conn, projects: filepath.Join(configDir, "projects"), watch: func(string) {}}, nil
}

func (x *Index) Close() error { return x.db.Close() }

// Sync brings the index up to date with the transcripts on disk. Unchanged
// files (same mtime and size) are not opened.
// ponytail: a changed file is reparsed whole; resume from end_offset if
// long, actively growing transcripts make sweeps measurably slow.
func (x *Index) Sync(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.sync(ctx)
}

// sync is Sync with x.mu held.
func (x *Index) sync(ctx context.Context) error {
	known := map[string][2]int64{}
	rows, err := x.db.QueryContext(ctx, `SELECT path, mtime, size FROM session`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var path string
		var mtime, size int64
		if err := rows.Scan(&path, &mtime, &size); err != nil {
			rows.Close()
			return err
		}
		known[path] = [2]int64{mtime, size}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	paths, err := x.transcripts()
	if err != nil {
		return err
	}
	changed := false
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		have, ok := known[path]
		delete(known, path)
		if ok && have == [2]int64{st.ModTime().UnixNano(), st.Size()} {
			continue
		}
		if err := x.index(ctx, path); err != nil {
			return err
		}
		changed = true
	}
	for path := range known {
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := x.remove(ctx, path); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return x.link(ctx)
}

func (x *Index) index(ctx context.Context, path string) error {
	e, err := scanFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return x.remove(ctx, path)
		}
		return err
	}
	x.parsed.Add(1)
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := clearRows(ctx, tx, path, e.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session (`+entryColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.Path, e.ID, e.RootID, e.ParentID, e.ParentToolUseID, e.Mtime, e.Size, e.End,
		e.Title, e.Directory, e.Model, e.Inferred, e.Created, e.Updated, e.Input, e.Output,
		e.LatestID, e.LatestRole, e.LatestCreated, e.Messages)
	if err != nil {
		return err
	}
	for i, off := range e.offsets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message (session_id, idx, offset) VALUES (?, ?, ?)`, e.ID, i, off); err != nil {
			return err
		}
	}
	indexed := time.Now().UnixMilli() // lets analytics pick up old rows indexed late
	for _, u := range e.usage {
		tools, _ := json.Marshal(u.ToolTimes)
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO usage (session_id, id, time_created, settled, data, tools, indexed) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.ID, u.ID, u.TimeCreated, u.Settled, u.Data, string(tools), indexed); err != nil {
			return err
		}
	}
	for _, id := range e.tasks {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO task_call (tool_use_id, session_id) VALUES (?, ?)`, id, e.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// clearRows drops a path's rows and any other path claiming the same session id.
func clearRows(ctx context.Context, tx *sql.Tx, path, id string) error {
	for _, q := range []string{
		`DELETE FROM message WHERE session_id IN (SELECT id FROM session WHERE path = ? OR id = ?)`,
		`DELETE FROM task_call WHERE session_id IN (SELECT id FROM session WHERE path = ? OR id = ?)`,
		`DELETE FROM usage WHERE session_id IN (SELECT id FROM session WHERE path = ? OR id = ?)`,
		`DELETE FROM session WHERE path = ? OR id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, path, id); err != nil {
			return err
		}
	}
	return nil
}

func (x *Index) remove(ctx context.Context, path string) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := clearRows(ctx, tx, path, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// link resolves subagent parents: the session that made the Task/Agent call
// named in the subagent's meta file (nested subagents), else the top-level
// session the subagent directory belongs to.
func (x *Index) link(ctx context.Context) error {
	_, err := x.db.ExecContext(ctx, `UPDATE session SET parent_id = COALESCE(
		(SELECT t.session_id FROM task_call t WHERE t.tool_use_id = session.parent_tool_use_id AND t.session_id != session.id),
		root_id) WHERE id != root_id`)
	return err
}

// refresh reindexes one transcript if it changed since it was indexed, so a
// detail read never pages with stale offsets.
func (x *Index) refresh(ctx context.Context, e *entry) (*entry, error) {
	st, err := os.Stat(e.Path)
	if err == nil && st.ModTime().UnixNano() == e.Mtime && st.Size() == e.Size {
		return e, nil
	}
	x.mu.Lock()
	err = x.index(ctx, e.Path)
	if err == nil {
		err = x.link(ctx)
	}
	x.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return x.get(ctx, e.ID)
}

func scanEntry(scan func(...any) error) (*entry, error) {
	e := &entry{}
	err := scan(&e.Path, &e.ID, &e.RootID, &e.ParentID, &e.ParentToolUseID, &e.Mtime, &e.Size, &e.End,
		&e.Title, &e.Directory, &e.Model, &e.Inferred, &e.Created, &e.Updated, &e.Input, &e.Output,
		&e.LatestID, &e.LatestRole, &e.LatestCreated, &e.Messages)
	return e, err
}

// get returns nil, nil for an unknown session.
func (x *Index) get(ctx context.Context, id string) (*entry, error) {
	e, err := scanEntry(x.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM session WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

func (x *Index) query(ctx context.Context, where string, args ...any) ([]*entry, error) {
	rows, err := x.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM session WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entry
	for rows.Next() {
		e, err := scanEntry(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (x *Index) offsets(ctx context.Context, id string) ([]int64, error) {
	rows, err := x.db.QueryContext(ctx, `SELECT offset FROM message WHERE session_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var off int64
		if err := rows.Scan(&off); err != nil {
			return nil, err
		}
		out = append(out, off)
	}
	return out, rows.Err()
}

// children maps Task/Agent tool_use ids to the subagent sessions they spawned.
func (x *Index) children(ctx context.Context, parent string) (map[string]string, error) {
	rows, err := x.db.QueryContext(ctx, `SELECT parent_tool_use_id, id FROM session WHERE parent_id = ? AND parent_tool_use_id != ''`, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tool, id string
		if err := rows.Scan(&tool, &id); err != nil {
			return nil, err
		}
		out[tool] = id
	}
	return out, rows.Err()
}

// convert reads only bytes [start, end) of a transcript.
func (x *Index) convert(path string, start, end int64, c *Converter) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(io.NewSectionReader(f, start, end-start), 64<<10)
	for {
		raw, err := r.ReadBytes('\n')
		x.bytesRead.Add(int64(len(raw)))
		if len(raw) > 0 {
			c.Add(raw)
		}
		if err == io.EOF {
			c.Finish()
			return nil
		}
		if err != nil {
			return err
		}
	}
}
