package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type WebhookInbox struct {
	ID                  string `json:"id"`
	RoutineID           string `json:"routineId"`
	RelayURL            string `json:"relayUrl"`
	ManagementToken     string `json:"managementToken"`
	FetchToken          string `json:"fetchToken"`
	AcknowledgmentToken string `json:"acknowledgmentToken"`
	Identity            string `json:"identity"`
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
		(id, routine_id, relay_url, management_token, fetch_token, acknowledgment_token, identity, key_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(routine_id) DO UPDATE SET id=excluded.id, relay_url=excluded.relay_url,
		management_token=excluded.management_token, fetch_token=excluded.fetch_token,
		acknowledgment_token=excluded.acknowledgment_token, identity=excluded.identity,
		key_version=excluded.key_version`, inbox.ID, inbox.RoutineID, inbox.RelayURL,
		inbox.ManagementToken, inbox.FetchToken, inbox.AcknowledgmentToken, inbox.Identity,
		inbox.KeyVersion, inbox.CreatedAt)
	if err != nil {
		return fmt.Errorf("saving webhook inbox: %w", err)
	}
	return nil
}

func (d *DB) GetWebhookInbox(ctx context.Context, routineID string) (WebhookInbox, error) {
	var inbox WebhookInbox
	err := d.db.QueryRowContext(ctx, `SELECT id, routine_id, relay_url, management_token, fetch_token,
		acknowledgment_token, identity, key_version, created_at FROM webhook_inbox WHERE routine_id = ?`, routineID).
		Scan(&inbox.ID, &inbox.RoutineID, &inbox.RelayURL, &inbox.ManagementToken, &inbox.FetchToken,
			&inbox.AcknowledgmentToken, &inbox.Identity, &inbox.KeyVersion, &inbox.CreatedAt)
	if err != nil {
		return WebhookInbox{}, err
	}
	return inbox, nil
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
