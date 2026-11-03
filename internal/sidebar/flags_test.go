package sidebar

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestApplyFlags(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		row                    db.Session
		flags                  Flags
		seen, pinned, archived bool
		unread                 int
	}{
		{name: "unseen", row: db.Session{TimeUpdated: 10, UnreadCount: 2}, unread: 2},
		{name: "seen current version", row: db.Session{TimeUpdated: 10, SeenTimeUpdated: 10, UnreadCount: 2}, flags: Flags{Seen: true}, seen: true},
		{name: "seen older version", row: db.Session{TimeUpdated: 11, SeenTimeUpdated: 10, UnreadCount: 2}, flags: Flags{Seen: true}, unread: 2},
		{name: "interruption not seen", row: db.Session{Status: db.StatusInterrupted, TimeUpdated: 10, SeenTimeUpdated: 10}, flags: Flags{Seen: true}},
		{name: "interruption seen", row: db.Session{Status: db.StatusInterrupted, TimeUpdated: 10, SeenTimeUpdated: 10}, flags: Flags{Seen: true, SeenInterrupted: true}, seen: true},
		{name: "pinned", row: db.Session{PinnedAt: 5}, pinned: true},
		{name: "archived", row: db.Session{TimeUpdated: 10}, flags: Flags{Archived: true, ArchivedAt: 10}, archived: true},
		{name: "archived then halted", row: db.Session{Status: db.StatusDone, TimeUpdated: 11}, flags: Flags{Archived: true, ArchivedAt: 10, Mode: "halt"}},
		{name: "archived while busy", row: db.Session{Status: db.StatusBusy, TimeUpdated: 11}, flags: Flags{Archived: true, ArchivedAt: 10, Mode: "halt"}, archived: true},
		{name: "archived busy with activity mode", row: db.Session{Status: db.StatusBusy, TimeUpdated: 11}, flags: Flags{Archived: true, ArchivedAt: 10, Mode: "activity"}},
		{name: "project archived", row: db.Session{Status: db.StatusDone, TimeUpdated: 10}, flags: Flags{ProjectArchivedAt: 10}, archived: true},
		{name: "project archive predates update", row: db.Session{Status: db.StatusDone, TimeUpdated: 11}, flags: Flags{ProjectArchivedAt: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := tc.row
			ApplyFlags(&row, tc.flags)
			if row.Seen != tc.seen || row.Pinned != tc.pinned || row.Archived != tc.archived || row.UnreadCount != tc.unread {
				t.Fatalf("seen=%t pinned=%t archived=%t unread=%d", row.Seen, row.Pinned, row.Archived, row.UnreadCount)
			}
		})
	}
}
