package statev2

import (
	"context"
	"testing"
	"time"
)

func TestConversationReadsDoNotQueueBehindIngestionWriter(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Messages[0].ConversationVisible = true
	commit(t, d, b)
	tx, err := d.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE session SET title='uncommitted import' WHERE instance_id='first' AND id='session'`); err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func(context.Context) error{
		"conversation": func(ctx context.Context) error {
			page, err := d.ReadConversation(ctx, b.Session.Key, 30, 0)
			if err == nil && page.Session.Title != "retained title" {
				t.Fatal("read uncommitted import")
			}
			return err
		},
		"metadata": func(ctx context.Context) error { _, _, err := d.SessionState(ctx, b.Session.Key); return err },
		"instance": func(ctx context.Context) error {
			_, err := d.FindInstance(ctx, "owner", "opencode", "first")
			return err
		},
		"unread": func(ctx context.Context) error {
			_, err := d.UnreadCounts(ctx, "first", map[string]int64{"session": 0})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			if err := read(ctx); err != nil {
				t.Fatalf("interactive read waited for background writer: %v", err)
			}
		})
	}
}

func TestHistoryReadPoolCannotWrite(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.reader().ExecContext(t.Context(), `DELETE FROM instance`); err == nil {
		t.Fatal("history reader can write")
	}
}
