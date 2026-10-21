package db

import (
	"context"
	"testing"
)

// summaryRow reads one session's runtime inventory row through GetSessions,
// the projection identity migration and reload safety use.
func summaryRow(t *testing.T, d *DB, ctx context.Context, id string) Session {
	t.Helper()
	rows, err := d.GetSessions(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("session %s not listed", id)
	return Session{}
}
