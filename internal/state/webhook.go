package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
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
	return d.listWebhookSubscriptions(ctx, "inbox_id", inboxID)
}

func (d *DB) listWebhookSubscriptions(ctx context.Context, column, value string) ([]WebhookSubscription, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id,inbox_id,routine_id,header_predicates_json,json_predicates_json,created_at FROM webhook_subscription WHERE `+column+`=? ORDER BY id`, value)
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
	updated, err := d.db.ExecContext(ctx, `UPDATE webhook_dispatch SET state='queued', error='', finished_at=0, created_at=?
		WHERE inbox_id=? AND delivery_id=? AND routine_id=? AND state='failure'`, now, inboxID, deliveryID, routineID)
	if err != nil {
		return false, err
	}
	if count, err := updated.RowsAffected(); err != nil {
		return false, err
	} else if count == 1 {
		return true, nil
	}
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

// RecordWebhookIgnored notes that a delivery did not run routineID, with the
// reason, so the delivery log can explain why a subscriber stayed quiet.
func (d *DB) RecordWebhookIgnored(ctx context.Context, inboxID, deliveryID, routineID, reason string, now int64) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO webhook_dispatch (inbox_id,delivery_id,routine_id,state,error,created_at,finished_at) VALUES (?,?,?,'ignored',?,?,?) ON CONFLICT DO NOTHING`, inboxID, deliveryID, routineID, reason, now, now)
	return err
}

type WebhookInbox struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// RoutineID is the legacy owning routine. Standalone inboxes store their
	// own ID here so the column's UNIQUE NOT NULL constraint still holds.
	// ponytail: reuses the column instead of a table rebuild; rebuild when legacy inboxes are gone.
	RoutineID           string `json:"routineId"`
	RelayURL            string `json:"relayUrl"`
	ManagementToken     string `json:"managementToken"`
	FetchToken          string `json:"fetchToken"`
	AcknowledgmentToken string `json:"acknowledgmentToken"`
	Identity            string `json:"identity"`
	IngestionURL        string `json:"ingestionUrl"`
	KeyVersion          int    `json:"keyVersion"`
	CreatedAt           int64  `json:"createdAt"`
	SecretHeader        string `json:"secretHeader"`
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
		(id, name, routine_id, relay_url, management_token, fetch_token, acknowledgment_token, identity, ingestion_url, key_version, created_at, secret_header)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(routine_id) DO UPDATE SET id=excluded.id, name=excluded.name, relay_url=excluded.relay_url,
		management_token=excluded.management_token, fetch_token=excluded.fetch_token,
		acknowledgment_token=excluded.acknowledgment_token, identity=excluded.identity,
		key_version=excluded.key_version, ingestion_url=excluded.ingestion_url, secret_header=excluded.secret_header`, inbox.ID, inbox.Name, inbox.RoutineID, inbox.RelayURL,
		inbox.ManagementToken, inbox.FetchToken, inbox.AcknowledgmentToken, inbox.Identity, inbox.IngestionURL,
		inbox.KeyVersion, inbox.CreatedAt, inbox.SecretHeader)
	if err != nil {
		return fmt.Errorf("saving webhook inbox: %w", err)
	}
	return nil
}

func (d *DB) GetWebhookInbox(ctx context.Context, routineID string) (WebhookInbox, error) {
	return d.getWebhookInbox(ctx, "routine_id", routineID)
}

// GetWebhookInboxByID loads an inbox by its relay-assigned ID.
func (d *DB) GetWebhookInboxByID(ctx context.Context, id string) (WebhookInbox, error) {
	return d.getWebhookInbox(ctx, "id", id)
}

func (d *DB) getWebhookInbox(ctx context.Context, column, value string) (WebhookInbox, error) {
	var inbox WebhookInbox
	err := d.db.QueryRowContext(ctx, `SELECT id, name, routine_id, relay_url, management_token, fetch_token,
		acknowledgment_token, identity, ingestion_url, key_version, created_at, secret_header FROM webhook_inbox WHERE `+column+` = ?`, value).
		Scan(&inbox.ID, &inbox.Name, &inbox.RoutineID, &inbox.RelayURL, &inbox.ManagementToken, &inbox.FetchToken,
			&inbox.AcknowledgmentToken, &inbox.Identity, &inbox.IngestionURL, &inbox.KeyVersion, &inbox.CreatedAt, &inbox.SecretHeader)
	if err != nil {
		return WebhookInbox{}, err
	}
	return inbox, nil
}

func (d *DB) ListWebhookInboxes(ctx context.Context) ([]WebhookInbox, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, name, routine_id, relay_url, management_token, fetch_token,
		acknowledgment_token, identity, ingestion_url, key_version, created_at, secret_header FROM webhook_inbox ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []WebhookInbox
	for rows.Next() {
		var inbox WebhookInbox
		if err := rows.Scan(&inbox.ID, &inbox.Name, &inbox.RoutineID, &inbox.RelayURL, &inbox.ManagementToken, &inbox.FetchToken,
			&inbox.AcknowledgmentToken, &inbox.Identity, &inbox.IngestionURL, &inbox.KeyVersion, &inbox.CreatedAt, &inbox.SecretHeader); err != nil {
			return nil, err
		}
		result = append(result, inbox)
	}
	return result, rows.Err()
}

func (d *DB) DeleteWebhookInbox(ctx context.Context, routineID string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM webhook_inbox WHERE routine_id = ?`, routineID)
	return err
}

// UpdateWebhookInboxMeta changes only the name and secret header, so it can't
// overwrite a key rotated concurrently or resurrect a revoked inbox.
func (d *DB) UpdateWebhookInboxMeta(ctx context.Context, id, name, secretHeader string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE webhook_inbox SET name = ?, secret_header = ? WHERE id = ?`, name, secretHeader, id)
	return err
}

// UpdateWebhookInboxKey records a rotated identity and key version only.
func (d *DB) UpdateWebhookInboxKey(ctx context.Context, id, identity string, keyVersion int) error {
	_, err := d.db.ExecContext(ctx, `UPDATE webhook_inbox SET identity = ?, key_version = ? WHERE id = ?`, identity, keyVersion, id)
	return err
}

// DeleteWebhookInboxByID removes an inbox and its subscriptions. Deletion is
// explicit because foreign-key enforcement can be off on legacy databases.
func (d *DB) DeleteWebhookInboxByID(ctx context.Context, id string) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM webhook_subscription WHERE inbox_id = ?`, id); err != nil {
		return err
	}
	_, err := d.db.ExecContext(ctx, `DELETE FROM webhook_inbox WHERE id = ?`, id)
	return err
}

// ListRoutineWebhookSubscriptions returns every inbox subscription of one routine.
func (d *DB) ListRoutineWebhookSubscriptions(ctx context.Context, routineID string) ([]WebhookSubscription, error) {
	return d.listWebhookSubscriptions(ctx, "routine_id", routineID)
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
	itemID := inboxID + ":" + deliveryID
	if _, err = tx.ExecContext(ctx, `INSERT INTO inbox_item (id, title, body, created_at) VALUES (?, ?, ?, ?)`, itemID, title, body, createdAt); err != nil {
		return false, fmt.Errorf("creating webhook inbox item: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_delivery (inbox_id, delivery_id, item_id, headers_json, accepted_at) VALUES (?, ?, ?, ?, ?)`, inboxID, deliveryID, itemID, headersJSON, time.Now().UnixMilli()); err != nil {
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

// WebhookDelivery is one received request as shown in the delivery log.
type WebhookDelivery struct {
	DeliveryID  string                  `json:"deliveryId"`
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
// body (kept on the Inbox item) and per-routine dispatch outcomes.
func (d *DB) ListWebhookDeliveries(ctx context.Context, inboxID string, limit int) ([]WebhookDelivery, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT w.delivery_id, w.accepted_at, w.attempts, w.last_error, w.headers_json, COALESCE(i.body, '')
		FROM webhook_delivery w LEFT JOIN inbox_item i ON i.id = w.item_id AND w.item_id != ''
		WHERE w.inbox_id=? ORDER BY w.accepted_at DESC, w.delivery_id DESC LIMIT ?`, inboxID, limit)
	if err != nil {
		return nil, err
	}
	result := []WebhookDelivery{}
	for rows.Next() {
		x := WebhookDelivery{Dispatches: []WebhookDispatchResult{}}
		if err := rows.Scan(&x.DeliveryID, &x.AcceptedAt, &x.Attempts, &x.LastError, &x.HeadersJSON, &x.Body); err != nil {
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
