package statev2

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/rawhistory"
)

func TestRawHistoryRetention(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	old := now.Add(-31 * 24 * time.Hour).UnixMilli()
	recent := now.Add(-29 * 24 * time.Hour).UnixMilli()
	b := fixture("first")
	b.Checkpoint = nil
	b.Messages[0].CreatedAt = old
	b.Messages[0].ConversationVisible = true
	b.Messages[0].Extensions = json.RawMessage(fmt.Sprintf(`{"metadata":{"role":"assistant"},"native":{"Type":"assistant","Sequence":2,"UpdatedAt":%d,"Data":{"content":[]}}}`, old))
	newer := b.Messages[0]
	newer.ID, newer.Position = "recent", 1
	newer.Extensions = json.RawMessage(fmt.Sprintf(`{"metadata":{"role":"assistant"},"native":{"Type":"assistant","UpdatedAt":%d,"Data":{"content":[]}}}`, recent))
	b.Messages = append(b.Messages, newer)
	commit(t, d, b)
	check := func(wantOld bool) {
		t.Helper()
		page, err := d.ReadConversation(t.Context(), b.Session.Key, 10, 0)
		if err != nil || len(page.Messages) != 2 || len(page.Parts) != 1 || page.Parts[0].Text != "retained content" {
			t.Fatalf("stored transcript changed: %+v, %v", page, err)
		}
		for _, msg := range page.Messages {
			var fields struct {
				Metadata json.RawMessage
				Native   struct {
					Type string
					Data json.RawMessage
				}
			}
			if err := json.Unmarshal(msg.Extensions, &fields); err != nil || fields.Native.Type != "assistant" || string(fields.Metadata) != `{"role":"assistant"}` ||
				(len(fields.Native.Data) > 0) != (msg.ID == "recent" || wantOld) {
				t.Fatalf("unexpected retention for %s: %s (%v)", msg.ID, msg.Extensions, err)
			}
		}
	}
	d.SetRawHistoryRetentionMode(RawHistoryRetentionReport)
	report, err := d.SweepRawHistory(t.Context(), now)
	if err != nil || report.Messages != 1 || report.Bytes <= 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	check(true)
	d.SetRawHistoryRetentionMode(RawHistoryRetentionApply)
	result, err := d.SweepRawHistory(t.Context(), now)
	if err != nil || result != report {
		t.Fatalf("apply = %+v, %v; report = %+v", result, err, report)
	}
	check(false)
	result, err = d.SweepRawHistory(t.Context(), now)
	if err != nil || result.Messages != 0 {
		t.Fatalf("repeat sweep = %+v, %v", result, err)
	}
	b.ExpectedRevision = 1
	commit(t, d, b)
	check(false)
	_, err = d.ApplyStream(t.Context(), StreamUpdate{InstanceID: b.Session.InstanceID,
		Checkpoint: CheckpointUpdate{Stream: "raw-history", Cursor: "1"},
		Batches:    []StreamBatch{{Key: b.Session.Key, Messages: b.Messages}}})
	if err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestReplayArchiveExpiryLeavesConversation(t *testing.T) {
	now := time.Now()
	for _, format := range []string{"claude-jsonl-v1", "codex-appserver-v1"} {
		t.Run(format, func(t *testing.T) {
			d := openTestDB(t)
			d.SetRawHistoryRetentionMode(RawHistoryRetentionApply)
			b := fixture("first")
			b.Messages[0].ConversationVisible = true
			archive, err := rawhistory.EncodeJSON(format, now.UnixMilli(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			b.RawReplay = archive
			commit(t, d, b)
			if _, err := d.ReadRawReplay(t.Context(), b.Session.Key); err != nil {
				t.Fatal(err)
			}
			result, err := d.SweepRawHistory(t.Context(), now.Add(31*24*time.Hour))
			if err != nil || result.Snapshots != 1 || result.Bytes != int64(len(archive.GZIP)) {
				t.Fatalf("expiry: %+v, %v", result, err)
			}
			if _, err := d.ReadRawReplay(t.Context(), b.Session.Key); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("archive after expiry: %v", err)
			}
			if page, err := d.ReadConversation(t.Context(), b.Session.Key, 10, 0); err != nil || len(page.Parts) != 1 || page.Parts[0].Text != "retained content" {
				t.Fatalf("transcript after expiry: %+v, %v", page, err)
			}
		})
	}
}

func TestRawHistorySweepResumesAfterReopen(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	old := now.Add(-31 * 24 * time.Hour).UnixMilli()
	b := fixture("first")
	b.Checkpoint = nil
	m := b.Messages[0]
	m.CreatedAt = old
	m.Extensions = json.RawMessage(`{"metadata":{"role":"assistant"},"native":{"Type":"assistant","Data":{"content":[]}}}`)
	b.Messages = nil
	for i := range 257 {
		m.ID, m.Position = fmt.Sprint(i), int64(i)
		b.Messages = append(b.Messages, m)
	}
	b.Parts = nil
	commit(t, d, b)
	d.SetRawHistoryRetentionMode(RawHistoryRetentionApply)
	first, err := d.sweepRawHistory(t.Context(), now, 1)
	if err != nil || !first.Pending || first.Messages != 256 {
		t.Fatalf("first slice: %+v, %v", first, err)
	}
	path := d.FilePath()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	d.SetRawHistoryRetentionMode(RawHistoryRetentionApply)
	second, err := d.sweepRawHistory(t.Context(), now, 1)
	if err != nil || second.Messages != 1 {
		t.Fatalf("resumed slice: %+v, %v", second, err)
	}
}

func TestRawHistorySweepYieldsBetweenLargeMessages(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	b := fixture("first")
	b.Checkpoint = nil
	m := b.Messages[0]
	m.CreatedAt = now.Add(-31 * 24 * time.Hour).UnixMilli()
	m.Extensions = json.RawMessage(`{"metadata":{"role":"assistant"},"native":{"Type":"assistant","Data":{"text":"` + strings.Repeat("x", 2<<20) + `"}}}`)
	b.Messages[0] = m
	m.ID, m.Position = "second", 1
	b.Messages = append(b.Messages, m)
	commit(t, d, b)
	d.SetRawHistoryRetentionMode(RawHistoryRetentionApply)
	for range 2 {
		page, err := d.sweepRawHistory(t.Context(), now, 1)
		if err != nil || !page.Pending || page.Messages != 1 || page.Bytes < 2<<20 {
			t.Fatalf("expected one oversized record to make progress on its own: %+v, %v", page, err)
		}
	}
}
