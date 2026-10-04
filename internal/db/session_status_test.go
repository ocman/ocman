package db

import (
	"database/sql"
	"errors"
	"slices"
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

func TestGetSessionLifecycle(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	if _, err := d.db.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'), ('empty','/e')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES
		('old','s',1,'invalid JSON'), ('a','s',2,'invalid JSON'), ('z','s',2,'{"role":"assistant","finish":"stop"}')`); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetSessionLifecycle(t.Context(), "s")
	want := SessionLifecycle{Directory: "/repo", Status: StatusWaiting, LatestMessageID: "z", LatestMessageCreated: 2, LatestMessageRole: "assistant"}
	if err != nil || got != want {
		t.Fatalf("lifecycle = %+v, %v; want %+v", got, err, want)
	}
	if got, err := d.GetSessionLifecycle(t.Context(), "empty"); err != nil || got != (SessionLifecycle{Directory: "/e", Status: StatusDone}) {
		t.Fatalf("empty = %+v, %v", got, err)
	}
}

func TestStatusCandidateDirectories(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	if _, err := d.db.Exec(`INSERT INTO session(id,directory,time_updated) VALUES
		('a','/new',200), ('b','/new',300), ('c','/old',50),
		('d','/r/.worktrees/p/tools',10), ('e','/r/.worktrees/p/stop',10),
		('f','/r/root-tools',10), ('g','/r/.worktrees/p/later',10)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES
		('m1','d',1,'{"role":"assistant","finish":"tool-calls"}'),
		('m2','e',1,'{"role":"assistant","finish":"stop"}'),
		('m3','f',1,'{"role":"assistant","finish":"tool-calls"}'),
		('m4','g',1,'{"role":"assistant","finish":"tool-calls"}'),
		('m5','g',2,'{"role":"assistant","finish":"stop"}')`); err != nil {
		t.Fatal(err)
	}
	got, err := d.StatusCandidateDirectories(t.Context(), 100)
	slices.Sort(got)
	want := []string{"/new", "/r/.worktrees/p/tools"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("StatusCandidateDirectories = %v, %v; want %v", got, err, want)
	}
}
