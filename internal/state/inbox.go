package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInboxTitleRequired = errors.New("inbox title is required")
	ErrInboxBodyRequired  = errors.New("inbox body is required")
	ErrInboxCategory      = errors.New("invalid inbox category")
)

const (
	InboxGeneral            = "general"
	InboxPermissionCategory = "permission"
	InboxFactory            = "factory"
	InboxRoutine            = "routine"
)

type InboxPermission struct {
	Platform     string         `json:"platform"`
	SessionID    string         `json:"sessionId"`
	PermissionID string         `json:"permissionId"`
	Permission   string         `json:"permission"`
	Patterns     []string       `json:"patterns"`
	Always       []string       `json:"always,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// InboxItem is one durable message owned by this ocman instance.
type InboxItem struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	Body       string           `json:"body"`
	CreatedAt  int64            `json:"createdAt"`
	ReadAt     int64            `json:"readAt,omitempty"`
	ArchivedAt int64            `json:"archivedAt,omitempty"`
	Category   string           `json:"category"`
	Permission *InboxPermission `json:"permission,omitempty"`
}

const inboxItemColumns = `id, title, body, created_at, read_at, archived_at, category, permission_json`

func scanInboxItem(scanner interface{ Scan(...any) error }) (InboxItem, error) {
	var item InboxItem
	var readAt, archivedAt sql.NullInt64
	var permissionJSON string
	err := scanner.Scan(&item.ID, &item.Title, &item.Body, &item.CreatedAt, &readAt, &archivedAt, &item.Category, &permissionJSON)
	if err == nil && permissionJSON != "" {
		err = json.Unmarshal([]byte(permissionJSON), &item.Permission)
	}
	if readAt.Valid {
		item.ReadAt = readAt.Int64
	}
	if archivedAt.Valid {
		item.ArchivedAt = archivedAt.Int64
	}
	return item, err
}

// CreateInboxItem validates and stores a new unread item.
func (d *DB) CreateInboxItem(ctx context.Context, title, body string) (InboxItem, error) {
	return d.CreateCategorizedInboxItem(ctx, title, body, InboxGeneral)
}

func (d *DB) CreateCategorizedInboxItem(ctx context.Context, title, body, category string) (InboxItem, error) {
	// Permission items can only be created from the permission lifecycle.
	if category != InboxGeneral && category != InboxFactory && category != InboxRoutine {
		return InboxItem{}, ErrInboxCategory
	}
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if title == "" {
		return InboxItem{}, ErrInboxTitleRequired
	}
	if body == "" {
		return InboxItem{}, ErrInboxBodyRequired
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return InboxItem{}, fmt.Errorf("generating Inbox item ID: %w", err)
	}
	item := InboxItem{ID: id.String(), Title: title, Body: body, CreatedAt: time.Now().UnixMilli(), Category: category}
	if _, err := d.db.ExecContext(ctx, `INSERT INTO inbox_item (`+inboxItemColumns+`) VALUES (?, ?, ?, ?, NULL, NULL, ?, '')`, item.ID, item.Title, item.Body, item.CreatedAt, category); err != nil {
		return InboxItem{}, fmt.Errorf("creating Inbox item: %w", err)
	}
	return item, nil
}

// NotifyInbox is CreateInboxItem for callers that only need the error, so
// packages like factory can assert the capability without importing state.
func (d *DB) NotifyInbox(ctx context.Context, title, body, category string) error {
	_, err := d.CreateCategorizedInboxItem(ctx, title, body, category)
	return err
}

func permissionInboxID(platform, sessionID, permissionID string) string {
	return "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(platform+"\x00"+sessionID+"\x00"+permissionID)).String()
}

// EnsurePermissionInboxItem never reopens a resolved or manually archived request.
func (d *DB) EnsurePermissionInboxItem(ctx context.Context, permission InboxPermission) error {
	if permission.Platform == "" || permission.SessionID == "" || permission.PermissionID == "" {
		return errors.New("permission platform, session and request ID are required")
	}
	if permission.Patterns == nil {
		permission.Patterns = []string{}
	}
	payload, err := json.Marshal(permission)
	if err != nil {
		return err
	}
	title := "Permission requested: " + permission.Permission
	body := "A session is waiting for your permission. Review the request below before responding."
	_, err = d.db.ExecContext(ctx, `INSERT INTO inbox_item (`+inboxItemColumns+`) VALUES (?, ?, ?, ?, NULL, NULL, 'permission', ?) ON CONFLICT(id) DO NOTHING`, permissionInboxID(permission.Platform, permission.SessionID, permission.PermissionID), title, body, time.Now().UnixMilli(), string(payload))
	return err
}

// A tombstone also handles a reply arriving before the corresponding asked event.
func (d *DB) ResolvePermissionInboxItem(ctx context.Context, platform, sessionID, permissionID string) error {
	now := time.Now().UnixMilli()
	_, err := d.db.ExecContext(ctx, `INSERT INTO inbox_item (`+inboxItemColumns+`) VALUES (?, 'Permission resolved', 'This permission request has been handled.', ?, NULL, ?, 'permission', '')
		ON CONFLICT(id) DO UPDATE SET archived_at = COALESCE(inbox_item.archived_at, excluded.archived_at)`, permissionInboxID(platform, sessionID, permissionID), now, now)
	return err
}

// ListInboxItems returns active items newest first.
func (d *DB) ListInboxItems(ctx context.Context) ([]InboxItem, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+inboxItemColumns+` FROM inbox_item WHERE archived_at IS NULL ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing Inbox items: %w", err)
	}
	defer rows.Close()
	var items []InboxItem
	for rows.Next() {
		item, err := scanInboxItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning Inbox item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading Inbox items: %w", err)
	}
	return items, nil
}

func (d *DB) CountUnreadInboxItems(ctx context.Context) (int, error) {
	var count int
	if err := d.db.QueryRowContext(ctx, `SELECT count(*) FROM inbox_item WHERE read_at IS NULL AND archived_at IS NULL`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting unread Inbox items: %w", err)
	}
	return count, nil
}

// MarkInboxItemUnread clears the read state of an active item. Missing IDs are no-ops.
func (d *DB) MarkInboxItemUnread(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE inbox_item SET read_at = NULL WHERE id = ? AND archived_at IS NULL`, id)
	return err
}

// MarkInboxItemRead marks an active item once. Missing or already-read IDs are no-ops.
func (d *DB) MarkInboxItemRead(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE inbox_item SET read_at = ? WHERE id = ? AND read_at IS NULL AND archived_at IS NULL`, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("marking Inbox item read: %w", err)
	}
	return nil
}

// RecallInboxItem archives an item once. Missing or already-archived IDs are no-ops.
func (d *DB) RecallInboxItem(ctx context.Context, id string) error {
	return d.archiveInboxItems(ctx, []string{id})
}

// ArchiveInboxItems archives the selected active items.
func (d *DB) ArchiveInboxItems(ctx context.Context, ids []string) error {
	return d.archiveInboxItems(ctx, ids)
}

func (d *DB) archiveInboxItems(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, time.Now().UnixMilli())
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := d.db.ExecContext(ctx, `UPDATE inbox_item SET archived_at = ? WHERE archived_at IS NULL AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return fmt.Errorf("archiving Inbox items: %w", err)
	}
	return nil
}

// ArchiveAllReadInboxItems archives every active item that has been read.
func (d *DB) ArchiveAllReadInboxItems(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `UPDATE inbox_item SET archived_at = ? WHERE archived_at IS NULL AND read_at IS NOT NULL`, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("archiving read Inbox items: %w", err)
	}
	return nil
}
