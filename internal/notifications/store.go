package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/ocman/ocman/internal/state"
)

var Schema = state.ModuleSchema{
	Module: "notifications",
	Tables: []string{"push_keys", "push_subscription", "push_delivery"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE push_keys (id INTEGER PRIMARY KEY CHECK(id=1), private_key TEXT NOT NULL, public_key TEXT NOT NULL);
		CREATE TABLE push_subscription (endpoint TEXT PRIMARY KEY, subscription TEXT NOT NULL, created_at INTEGER NOT NULL);
		CREATE TABLE push_delivery (endpoint TEXT NOT NULL REFERENCES push_subscription(endpoint) ON DELETE CASCADE,
		 event TEXT NOT NULL, payload TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
		 next_at INTEGER NOT NULL, created_at INTEGER NOT NULL, done INTEGER NOT NULL DEFAULT 0,
		 PRIMARY KEY(endpoint,event));`)
		return err
	}},
}

type store struct {
	db *sql.DB
	mu sync.Mutex
}

func (s *store) keys(ctx context.Context) (private, public string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.QueryRowContext(ctx, `SELECT private_key, public_key FROM push_keys WHERE id=1`).Scan(&private, &public)
	if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	private, public, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO push_keys VALUES (1,?,?)`, private, public)
	if err == nil {
		err = s.db.QueryRowContext(ctx, `SELECT private_key, public_key FROM push_keys WHERE id=1`).Scan(&private, &public)
	}
	return
}

func (s *store) save(ctx context.Context, sub webpush.Subscription) error {
	data, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO push_subscription SELECT ?,?,?
	 WHERE (SELECT count(*) FROM push_subscription)<64 OR EXISTS(SELECT 1 FROM push_subscription WHERE endpoint=?)
	 ON CONFLICT(endpoint) DO UPDATE SET subscription=excluded.subscription`, sub.Endpoint, string(data), time.Now().UnixMilli(), sub.Endpoint)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return errors.New("push device limit reached")
	}
	return err
}

func (s *store) remove(ctx context.Context, endpoint string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Legacy state databases with pre-existing violations keep foreign keys
	// off. Removal must still erase queued alerts before a device can rejoin.
	for _, query := range []string{`DELETE FROM push_delivery WHERE endpoint=?`, `DELETE FROM push_subscription WHERE endpoint=?`} {
		if _, err := tx.ExecContext(ctx, query, endpoint); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *store) count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM push_subscription`).Scan(&n)
	return n, err
}

func (s *store) active(ctx context.Context, endpoint string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM push_subscription WHERE endpoint=?)`, endpoint).Scan(&active)
	return active, err
}

func (s *store) enqueue(ctx context.Context, c Candidate) error {
	data, err := json.Marshal(c.Notification)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO push_delivery(endpoint,event,payload,next_at,created_at)
	 SELECT endpoint,?,?,?,? FROM push_subscription WHERE created_at<=?`, c.Key, string(data), now, now, now)
	return err
}

type delivery struct {
	endpoint, event, payload, subscription string
	attempts                               int
}

func (s *store) due(ctx context.Context) ([]delivery, error) {
	now := time.Now().UnixMilli()
	// Keep receipts for a week so repeated snapshots cannot replay an alert.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM push_delivery WHERE created_at<?`, now-int64((7*24*time.Hour)/time.Millisecond)); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE push_delivery SET done=1 WHERE done=0 AND created_at<?`, now-int64(time.Hour/time.Millisecond)); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.endpoint,event,payload,subscription,attempts FROM push_delivery d
	 JOIN push_subscription s ON s.endpoint=d.endpoint WHERE done=0 AND next_at<=? ORDER BY d.created_at LIMIT 32`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []delivery
	for rows.Next() {
		var d delivery
		if err := rows.Scan(&d.endpoint, &d.event, &d.payload, &d.subscription, &d.attempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) finish(ctx context.Context, d delivery, success bool, delay time.Duration) error {
	_, err := s.db.ExecContext(ctx, `UPDATE push_delivery SET done=?,attempts=attempts+1,next_at=? WHERE endpoint=? AND event=?`,
		success || d.attempts >= 5, time.Now().Add(delay).UnixMilli(), d.endpoint, d.event)
	return err
}
