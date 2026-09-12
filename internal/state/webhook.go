package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type WebhookSubscription struct {
	ID                   string `json:"id"`
	InboxID              string `json:"inboxId"`
	RoutineID            string `json:"routineId"`
	HeaderPredicatesJSON string `json:"headerPredicates"`
	JSONPredicatesJSON   string `json:"jsonPredicates"`
	CreatedAt            int64  `json:"createdAt"`
}

type WebhookDispatch struct {
	InboxID    string
	DeliveryID string
	RoutineID  string
	State      string
	Error      string
}

const WebhookHistoryRetention = 30 * 24 * time.Hour

func (d *DB) CleanupWebhookHistory(ctx context.Context, before int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM webhook_dispatch WHERE finished_at > 0 AND finished_at < ?`, before)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `DELETE FROM webhook_delivery WHERE accepted_at > 0 AND accepted_at < ?`, before)
	return err
}

func (d *DB) SaveWebhookSubscription(ctx context.Context, sub WebhookSubscription) error {
	if sub.ID == "" || sub.InboxID == "" || sub.RoutineID == "" {
		return fmt.Errorf("webhook subscription requires id, inbox, and routine")
	}
	if sub.HeaderPredicatesJSON == "" {
		sub.HeaderPredicatesJSON = "{}"
	}
	if sub.JSONPredicatesJSON == "" {
		sub.JSONPredicatesJSON = "{}"
	}
	if sub.CreatedAt == 0 {
		sub.CreatedAt = time.Now().UnixMilli()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO webhook_subscription (id,inbox_id,routine_id,header_predicates_json,json_predicates_json,created_at) VALUES (?,?,?,?,?,?) ON CONFLICT(inbox_id,routine_id) DO UPDATE SET header_predicates_json=excluded.header_predicates_json,json_predicates_json=excluded.json_predicates_json`, sub.ID, sub.InboxID, sub.RoutineID, sub.HeaderPredicatesJSON, sub.JSONPredicatesJSON, sub.CreatedAt)
	return err
}

func (d *DB) ListWebhookSubscriptions(ctx context.Context, inboxID string) ([]WebhookSubscription, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id,inbox_id,routine_id,header_predicates_json,json_predicates_json,created_at FROM webhook_subscription WHERE inbox_id=? ORDER BY id`, inboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []WebhookSubscription
	for rows.Next() {
		var s WebhookSubscription
		if err := rows.Scan(&s.ID, &s.InboxID, &s.RoutineID, &s.HeaderPredicatesJSON, &s.JSONPredicatesJSON, &s.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (d *DB) DeleteWebhookSubscription(ctx context.Context, inboxID, routineID string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM webhook_subscription WHERE inbox_id=? AND routine_id=?`, inboxID, routineID)
	return err
}

// ClaimWebhookDispatch is the durable deduplication boundary for a delivery.
func (d *DB) ClaimWebhookDispatch(ctx context.Context, inboxID, deliveryID, routineID string, now int64) (bool, error) {
	r, err := d.db.ExecContext(ctx, `INSERT INTO webhook_dispatch (inbox_id,delivery_id,routine_id,state,created_at) VALUES (?,?,?,'queued',?) ON CONFLICT DO NOTHING`, inboxID, deliveryID, routineID, now)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (d *DB) FinishWebhookDispatch(ctx context.Context, inboxID, deliveryID, routineID, state, message string, now int64) error {
	_, err := d.db.ExecContext(ctx, `UPDATE webhook_dispatch SET state=?,error=?,finished_at=? WHERE inbox_id=? AND delivery_id=? AND routine_id=? AND state='queued'`, state, message, now, inboxID, deliveryID, routineID)
	return err
}

func (d *DB) RecordWebhookIgnored(ctx context.Context, inboxID, deliveryID string, now int64) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO webhook_dispatch (inbox_id,delivery_id,routine_id,state,created_at,finished_at) VALUES (?,?,?,'ignored',?,?) ON CONFLICT DO NOTHING`, inboxID, deliveryID, "", now, now)
	return err
}

type WebhookInbox struct {
	ID                  string `json:"id"`
	RoutineID           string `json:"routineId"`
	RelayURL            string `json:"relayUrl"`
	ManagementToken     string `json:"managementToken"`
	FetchToken          string `json:"fetchToken"`
	AcknowledgmentToken string `json:"acknowledgmentToken"`
	Identity            string `json:"identity"`
	IngestionURL        string `json:"ingestionUrl"`
	KeyVersion          int    `json:"keyVersion"`
	CreatedAt           int64  `json:"createdAt"`
}

func (d *DB) SaveWebhookInbox(ctx context.Context, inbox WebhookInbox) error {
	if inbox.ID == "" || inbox.RoutineID == "" || inbox.RelayURL == "" || inbox.Identity == "" {
		return fmt.Errorf("webhook inbox requires id, routine, relay URL, and identity")
	}
	if inbox.KeyVersion < 1 {
		inbox.KeyVersion = 1
	}
	if inbox.CreatedAt == 0 {
		inbox.CreatedAt = time.Now().UnixMilli()
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO webhook_inbox
		(id, routine_id, relay_url, management_token, fetch_token, acknowledgment_token, identity, ingestion_url, key_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(routine_id) DO UPDATE SET id=excluded.id, relay_url=excluded.relay_url,
		management_token=excluded.management_token, fetch_token=excluded.fetch_token,
		acknowledgment_token=excluded.acknowledgment_token, identity=excluded.identity,
		key_version=excluded.key_version, ingestion_url=excluded.ingestion_url`, inbox.ID, inbox.RoutineID, inbox.RelayURL,
		inbox.ManagementToken, inbox.FetchToken, inbox.AcknowledgmentToken, inbox.Identity, inbox.IngestionURL,
		inbox.KeyVersion, inbox.CreatedAt)
	if err != nil {
		return fmt.Errorf("saving webhook inbox: %w", err)
	}
	return nil
}

func (d *DB) GetWebhookInbox(ctx context.Context, routineID string) (WebhookInbox, error) {
	var inbox WebhookInbox
	err := d.db.QueryRowContext(ctx, `SELECT id, routine_id, relay_url, management_token, fetch_token,
		acknowledgment_token, identity, ingestion_url, key_version, created_at FROM webhook_inbox WHERE routine_id = ?`, routineID).
		Scan(&inbox.ID, &inbox.RoutineID, &inbox.RelayURL, &inbox.ManagementToken, &inbox.FetchToken,
			&inbox.AcknowledgmentToken, &inbox.Identity, &inbox.IngestionURL, &inbox.KeyVersion, &inbox.CreatedAt)
	if err != nil {
		return WebhookInbox{}, err
	}
	return inbox, nil
}

func (d *DB) DeleteWebhookInbox(ctx context.Context, routineID string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM webhook_inbox WHERE routine_id = ?`, routineID)
	return err
}

func (d *DB) WebhookDispatchCounts(ctx context.Context, inboxID string) (map[string]int, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM webhook_dispatch WHERE inbox_id=? GROUP BY state`, inboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
	}
	return counts, rows.Err()
}

// AcceptWebhookDelivery commits the inbox item and relay identity together.
// A retry after a lost acknowledgement therefore cannot create a duplicate.
func (d *DB) AcceptWebhookDelivery(ctx context.Context, inboxID, deliveryID, title, body string, createdAt int64) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT item_id FROM webhook_delivery WHERE inbox_id = ? AND delivery_id = ?`, inboxID, deliveryID).Scan(&existing)
	if err == nil {
		if existing != "" {
			return false, nil
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM webhook_delivery WHERE inbox_id = ? AND delivery_id = ?`, inboxID, deliveryID); err != nil {
			return false, err
		}
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	itemID := inboxID + ":" + deliveryID
	if _, err = tx.ExecContext(ctx, `INSERT INTO inbox_item (id, title, body, created_at) VALUES (?, ?, ?, ?)`, itemID, title, body, createdAt); err != nil {
		return false, fmt.Errorf("creating webhook inbox item: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_delivery (inbox_id, delivery_id, item_id, accepted_at) VALUES (?, ?, ?, ?)`, inboxID, deliveryID, itemID, time.Now().UnixMilli()); err != nil {
		return false, fmt.Errorf("recording webhook delivery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing webhook delivery: %w", err)
	}
	return true, nil
}

func (d *DB) RecordWebhookDeliveryError(ctx context.Context, inboxID, deliveryID, message string, now time.Time) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO webhook_delivery (inbox_id, delivery_id, item_id, attempts, last_error, next_retry_at, accepted_at)
		VALUES (?, ?, '', 1, ?, ?, ?) ON CONFLICT(inbox_id, delivery_id) DO UPDATE SET attempts=attempts+1,
		last_error=excluded.last_error, next_retry_at=excluded.next_retry_at`, inboxID, deliveryID, message,
		now.Add(webhookRetryDelay(1)).UnixMilli(), now.UnixMilli())
	return err
}

func (d *DB) WebhookDeliveryRetryAllowed(ctx context.Context, inboxID, deliveryID string, now time.Time) (bool, error) {
	var attempts int
	var next int64
	err := d.db.QueryRowContext(ctx, `SELECT attempts, next_retry_at FROM webhook_delivery WHERE inbox_id = ? AND delivery_id = ?`, inboxID, deliveryID).Scan(&attempts, &next)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return attempts < 8 && next <= now.UnixMilli(), nil
}

func webhookRetryDelay(attempt int) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<attempt) * time.Second
}
