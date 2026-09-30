package state

import (
	"context"
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
	if _, err = d.db.ExecContext(ctx, `DELETE FROM webhook_delivery WHERE accepted_at > 0 AND accepted_at < ?`, before); err != nil {
		return err
	}
	return d.cleanupWebhookFiles(before)
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

// FinishWebhookDispatch settles a claimed dispatch. A routine that could not
// be started is the one dispatch outcome worth an Inbox item.
func (d *DB) FinishWebhookDispatch(ctx context.Context, inboxID, deliveryID, routineID, state, message string, now int64) error {
	updated, err := d.db.ExecContext(ctx, `UPDATE webhook_dispatch SET state=?,error=?,finished_at=? WHERE inbox_id=? AND delivery_id=? AND routine_id=? AND state='queued'`, state, message, now, inboxID, deliveryID, routineID)
	if err != nil || state != "failure" {
		return err
	}
	if n, err := updated.RowsAffected(); err != nil || n == 0 {
		return err
	}
	// A run that started and failed posts its own notice; only a routine that
	// never got a run needs one from here.
	var hasRun bool
	if err := d.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM routine_run WHERE routine_id = ? AND occurrence_at = ?)`,
		routineID, WebhookOccurrence(inboxID, deliveryID)).Scan(&hasRun); err != nil || hasRun {
		return err
	}
	var name string
	_ = d.db.QueryRowContext(ctx, `SELECT name FROM routine WHERE id = ?`, routineID).Scan(&name)
	if name == "" {
		name = routineID
	}
	return d.notifyWebhookFailure(ctx, "webhook-failed:"+inboxID+":"+deliveryID+":"+routineID, inboxID,
		fmt.Sprintf("Routine %s did not start for delivery %s: %s", name, deliveryID, message), now)
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
	// Secret is the shared secret deliveries must carry, kept so the UI can
	// show it back. Empty either means none, or an inbox older than v109 whose
	// secret only the relay knows (as a hash); SecretHeader tells them apart.
	Secret string `json:"secret"`
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
		(id, name, routine_id, relay_url, management_token, fetch_token, acknowledgment_token, identity, ingestion_url, key_version, created_at, secret_header, secret)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(routine_id) DO UPDATE SET id=excluded.id, name=excluded.name, relay_url=excluded.relay_url,
		management_token=excluded.management_token, fetch_token=excluded.fetch_token,
		acknowledgment_token=excluded.acknowledgment_token, identity=excluded.identity,
		key_version=excluded.key_version, ingestion_url=excluded.ingestion_url, secret_header=excluded.secret_header, secret=excluded.secret`, inbox.ID, inbox.Name, inbox.RoutineID, inbox.RelayURL,
		inbox.ManagementToken, inbox.FetchToken, inbox.AcknowledgmentToken, inbox.Identity, inbox.IngestionURL,
		inbox.KeyVersion, inbox.CreatedAt, inbox.SecretHeader, inbox.Secret)
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
		acknowledgment_token, identity, ingestion_url, key_version, created_at, secret_header, secret FROM webhook_inbox WHERE `+column+` = ?`, value).
		Scan(&inbox.ID, &inbox.Name, &inbox.RoutineID, &inbox.RelayURL, &inbox.ManagementToken, &inbox.FetchToken,
			&inbox.AcknowledgmentToken, &inbox.Identity, &inbox.IngestionURL, &inbox.KeyVersion, &inbox.CreatedAt, &inbox.SecretHeader, &inbox.Secret)
	if err != nil {
		return WebhookInbox{}, err
	}
	return inbox, nil
}

func (d *DB) ListWebhookInboxes(ctx context.Context) ([]WebhookInbox, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, name, routine_id, relay_url, management_token, fetch_token,
		acknowledgment_token, identity, ingestion_url, key_version, created_at, secret_header, secret FROM webhook_inbox ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []WebhookInbox
	for rows.Next() {
		var inbox WebhookInbox
		if err := rows.Scan(&inbox.ID, &inbox.Name, &inbox.RoutineID, &inbox.RelayURL, &inbox.ManagementToken, &inbox.FetchToken,
			&inbox.AcknowledgmentToken, &inbox.Identity, &inbox.IngestionURL, &inbox.KeyVersion, &inbox.CreatedAt, &inbox.SecretHeader, &inbox.Secret); err != nil {
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

// RenameWebhookInbox and UpdateWebhookInboxSecret each change only their own
// columns, so neither can overwrite a key rotated concurrently, a secret or
// name changed meanwhile, or resurrect a revoked inbox.
func (d *DB) RenameWebhookInbox(ctx context.Context, id, name string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE webhook_inbox SET name = ? WHERE id = ?`, name, id)
	return err
}

func (d *DB) UpdateWebhookInboxSecret(ctx context.Context, id, secretHeader, secret string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE webhook_inbox SET secret_header = ?, secret = ? WHERE id = ?`, secretHeader, secret, id)
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
