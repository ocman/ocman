package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

// webhookMaxAttempts bounds how often a failing delivery is retried before
// the poller gives up on it and says so in the Inbox.
const webhookMaxAttempts = 8

// AcceptWebhookDelivery logs a delivery with its relay identity. item_id marks
// it accepted, so a retry after a lost acknowledgement is not a duplicate.
// A delivery is only logged; the Inbox hears about failures, not traffic.
func (d *DB) AcceptWebhookDelivery(ctx context.Context, inboxID, deliveryID, title, body, headersJSON string, createdAt int64) (bool, error) {
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
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_delivery (inbox_id, delivery_id, item_id, title, body, headers_json, accepted_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		inboxID, deliveryID, inboxID+":"+deliveryID, title, body, headersJSON, time.Now().UnixMilli()); err != nil {
		return false, fmt.Errorf("recording webhook delivery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing webhook delivery: %w", err)
	}
	return true, nil
}

// RecordWebhookDeliveryError schedules a retry. Retries are quiet; the attempt
// that exhausts them posts one Inbox item.
func (d *DB) RecordWebhookDeliveryError(ctx context.Context, inboxID, deliveryID, message string, now time.Time) error {
	var attempts int
	err := d.db.QueryRowContext(ctx, `INSERT INTO webhook_delivery (inbox_id, delivery_id, item_id, attempts, last_error, next_retry_at, accepted_at)
		VALUES (?, ?, '', 1, ?, ?, ?) ON CONFLICT(inbox_id, delivery_id) DO UPDATE SET attempts=attempts+1,
		last_error=excluded.last_error, next_retry_at=excluded.next_retry_at RETURNING attempts`, inboxID, deliveryID, message,
		now.Add(webhookRetryDelay(1)).UnixMilli(), now.UnixMilli()).Scan(&attempts)
	if err != nil || attempts < webhookMaxAttempts {
		return err
	}
	return d.notifyWebhookFailure(ctx, "webhook-failed:"+inboxID+":"+deliveryID, inboxID,
		fmt.Sprintf("Delivery %s was given up after %d attempts: %s", deliveryID, attempts, message), now.UnixMilli())
}

// notifyWebhookFailure posts one routine-category Inbox item per id, so a
// failure that repeats does not repeat its notice.
func (d *DB) notifyWebhookFailure(ctx context.Context, id, inboxID, text string, now int64) error {
	body := strings.TrimSpace(text) + "\n\n[View webhook inboxes](/routines?tab=inboxes)"
	_, err := d.db.ExecContext(ctx, `INSERT INTO inbox_item (id, title, body, created_at, category)
		SELECT ?, 'Webhook ' || COALESCE(NULLIF(name, ''), id) || ': delivery failed', ?, ?, ?
		FROM webhook_inbox WHERE id = ? ON CONFLICT(id) DO NOTHING`, id, body, now, InboxRoutine, inboxID)
	if err != nil {
		return fmt.Errorf("notifying webhook failure: %w", err)
	}
	return nil
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
	return attempts < webhookMaxAttempts && next <= now.UnixMilli(), nil
}

func webhookRetryDelay(attempt int) time.Duration {
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<attempt) * time.Second
}

// WebhookDelivery is one received request as shown in the delivery log.
type WebhookDelivery struct {
	DeliveryID string `json:"deliveryId"`
	// Accepted is false for a delivery that never decrypted; it has no body
	// and cannot be redelivered.
	Accepted    bool                    `json:"accepted"`
	AcceptedAt  int64                   `json:"acceptedAt"`
	Attempts    int                     `json:"attempts"`
	LastError   string                  `json:"lastError"`
	HeadersJSON string                  `json:"headers"`
	Body        string                  `json:"body"`
	Dispatches  []WebhookDispatchResult `json:"dispatches"`
}

// WebhookDispatchResult is what one delivery did for one routine. An empty
// RoutineID with state "ignored" means no subscription matched.
type WebhookDispatchResult struct {
	RoutineID string `json:"routineId"`
	State     string `json:"state"`
	Error     string `json:"error"`
	// Platform and SessionID locate the run's session once it has one.
	Platform  string `json:"platform"`
	SessionID string `json:"sessionId"`
}

// WebhookOccurrence is the routine-run occurrence a delivery maps to, so a
// redelivery claims the same run and the log can find that run again.
func WebhookOccurrence(inboxID, deliveryID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(inboxID + ":" + deliveryID))
	if occurrence := int64(h.Sum64() & 0x7fffffffffffffff); occurrence != 0 {
		return occurrence
	}
	return 1
}

// ListWebhookDeliveries returns the newest deliveries for an inbox with their
// body and per-routine dispatch outcomes.
func (d *DB) ListWebhookDeliveries(ctx context.Context, inboxID string, limit int) ([]WebhookDelivery, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT w.delivery_id, w.item_id != '', w.accepted_at, w.attempts, w.last_error, w.headers_json, w.body
		FROM webhook_delivery w WHERE w.inbox_id=? ORDER BY w.accepted_at DESC, w.delivery_id DESC LIMIT ?`, inboxID, limit)
	if err != nil {
		return nil, err
	}
	result := []WebhookDelivery{}
	for rows.Next() {
		x := WebhookDelivery{Dispatches: []WebhookDispatchResult{}}
		if err := rows.Scan(&x.DeliveryID, &x.Accepted, &x.AcceptedAt, &x.Attempts, &x.LastError, &x.HeadersJSON, &x.Body); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result = append(result, x)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	// ponytail: one query per delivery; limit keeps it to a handful.
	for i := range result {
		drows, err := d.db.QueryContext(ctx, `SELECT x.routine_id, x.state, x.error, COALESCE(r.platform, ''), COALESCE(r.session_id, '')
			FROM webhook_dispatch x LEFT JOIN routine_run r ON r.routine_id = x.routine_id AND r.occurrence_at = ? AND x.routine_id != ''
			WHERE x.inbox_id=? AND x.delivery_id=? ORDER BY x.routine_id`, WebhookOccurrence(inboxID, result[i].DeliveryID), inboxID, result[i].DeliveryID)
		if err != nil {
			return nil, err
		}
		for drows.Next() {
			var r WebhookDispatchResult
			if err := drows.Scan(&r.RoutineID, &r.State, &r.Error, &r.Platform, &r.SessionID); err != nil {
				_ = drows.Close()
				return nil, err
			}
			result[i].Dispatches = append(result[i].Dispatches, r)
		}
		if err := errors.Join(drows.Err(), drows.Close()); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// GetWebhookDelivery returns one logged delivery's request title, headers and
// body, for replaying it. ErrNoRows when it is unknown or was never accepted.
func (d *DB) GetWebhookDelivery(ctx context.Context, inboxID, deliveryID string) (title, headersJSON, body string, err error) {
	err = d.db.QueryRowContext(ctx, `SELECT title, headers_json, body FROM webhook_delivery
		WHERE inbox_id = ? AND delivery_id = ? AND item_id != ''`, inboxID, deliveryID).Scan(&title, &headersJSON, &body)
	return title, headersJSON, body, err
}
