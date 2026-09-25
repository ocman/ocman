package db

import (
	"database/sql"
	"errors"
	"testing"
)

func TestGetSessionStatus(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       SessionStatus
	}{
		{"empty", "", StatusDone},
		{"user", `{"role":"user"}`, StatusDone},
		{"unfinished", `{"role":"assistant"}`, StatusBusy},
		{"done", `{"role":"assistant","finish":"stop"}`, StatusWaiting},
		{"error", `{"role":"assistant","error":{"name":"APIError"}}`, StatusError},
		{"aborted", `{"role":"assistant","error":{"name":"MessageAbortedError"}}`, StatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			if _, err := d.db.Exec(`INSERT INTO session(id) VALUES ('s')`); err != nil {
				t.Fatal(err)
			}
			if tc.data != "" {
				// These older blobs must not even be parsed. The last two
				// messages share a timestamp, requiring the ID tie-breaker.
				if _, err := d.db.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES
					('old','s',1,'invalid JSON'), ('a','s',2,'invalid JSON'), ('z','s',2,?)`, tc.data); err != nil {
					t.Fatal(err)
				}
			}
			got, err := d.GetSessionStatus(t.Context(), "s")
			if err != nil || got != tc.want {
				t.Fatalf("status = %q, err = %v; want %q", got, err, tc.want)
			}
			if _, err := d.GetSessionStatus(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("missing session error = %v", err)
			}
		})
	}
}
