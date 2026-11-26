package server

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/pluginsmod"
)

// The idle edge and history ingestion arrive on separate provider streams, and
// ingestion batches its reads, so the handler can run while the stored
// transcript still trails the finished turn. The completion must be recorded
// on that edge, not left to the five-minute reconciliation sweep.
func TestPluginSessionIdleBeforeIngestionRecordsCompletion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seen, final []string
	}{{
		name: "first turn not yet ingested",
		seen: []string{
			`INSERT INTO message (id,session_id,time_created,data) VALUES ('m1','ses-plug',100,'{"role":"user","time":{"created":100}}')`,
			`INSERT INTO part (id,message_id,session_id,time_created,data) VALUES ('p1','m1','ses-plug',100,'{"type":"text","text":"prompt"}')`,
		},
		final: []string{
			`INSERT INTO message (id,session_id,time_created,data) VALUES ('m2','ses-plug',200,'{"role":"assistant","finish":"stop","time":{"created":200,"completed":300}}')`,
			`INSERT INTO part (id,message_id,session_id,time_created,data) VALUES ('p2','m2','ses-plug',200,'{"type":"text","text":"answer"}')`,
		},
	}, {
		name: "answer ingested while streaming",
		seen: []string{
			`INSERT INTO message (id,session_id,time_created,data) VALUES ('m1','ses-plug',100,'{"role":"user","time":{"created":100}}')`,
			`INSERT INTO part (id,message_id,session_id,time_created,data) VALUES ('p1','m1','ses-plug',100,'{"type":"text","text":"prompt"}')`,
			`INSERT INTO message (id,session_id,time_created,data) VALUES ('m2','ses-plug',200,'{"role":"assistant","time":{"created":200}}')`,
			`INSERT INTO part (id,message_id,session_id,time_created,data) VALUES ('p2','m2','ses-plug',200,'{"type":"text","text":"answ"}')`,
		},
		final: []string{
			`UPDATE message SET data='{"role":"assistant","finish":"stop","time":{"created":200,"completed":300}}' WHERE id='m2'`,
			`UPDATE part SET data='{"type":"text","text":"answer"}' WHERE id='p2'`,
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			srv, raw := testServerWithRawDB(t)
			defer raw.Close()
			execAll(t, raw, append([]string{`INSERT INTO session (id,title,directory,time_created,time_updated) VALUES ('ses-plug','t','/tmp',100,100)`}, tc.seen...))
			ingestRaw(t, srv, 1)
			id := canonicalID(t, srv, "ses-plug")
			ctx := t.Context()
			const pluginID = "org.example.stale"
			store := pluginsmod.NewStore(srv.stateDB)
			if err := store.LinkPluginSession(ctx, "opencode", id, pluginID); err != nil {
				t.Fatal(err)
			}
			// The provider finishes the turn; ingestion has not committed it.
			execAll(t, raw, tc.final)
			adapter, ok := srv.adapterForSession(ctx, "opencode", id)
			if !ok {
				t.Fatal("no adapter for the canonical session")
			}
			if lc, err := adapter.(platforms.LifecycleReader).SessionLifecycle(ctx, id); err != nil || lc.LatestMessageID != "m2" {
				t.Fatalf("live lifecycle is not terminal: %+v %v", lc, err)
			}
			ref, err := adapter.(platforms.PluginSessionCompatibility).PluginSessionReference(ctx, id)
			if err != nil {
				t.Fatal(err)
			}

			srv.pluginsModule().ReplyToSession(ctx, "opencode", id)

			has, err := store.HasPluginSessionCompletion(ctx, pluginID, ref+":m2")
			if err != nil || !has {
				t.Fatalf("completed turn waits for the reconciliation sweep: %v %v", has, err)
			}
		})
	}
}

// With history already current, the idle publication path (onSessionIdle,
// bus, plugin subscriber) records the completion under the canonical platform
// key and session ID.
func TestPluginSessionIdleWithCurrentHistoryRecordsCompletion(t *testing.T) {
	srv, raw := testServerWithRawDB(t)
	defer raw.Close()
	execAll(t, raw, []string{
		`INSERT INTO session (id,title,directory,time_created,time_updated) VALUES ('ses-plug','t','/tmp',100,100)`,
		`INSERT INTO message (id,session_id,time_created,data) VALUES ('m1','ses-plug',100,'{"role":"user","time":{"created":100}}')`,
		`INSERT INTO message (id,session_id,time_created,data) VALUES ('m2','ses-plug',200,'{"role":"assistant","finish":"stop","time":{"created":200,"completed":300}}')`,
		`INSERT INTO part (id,message_id,session_id,time_created,data) VALUES ('p2','m2','ses-plug',200,'{"type":"text","text":"answer"}')`,
	})
	ingestRaw(t, srv, 1)
	id := canonicalID(t, srv, "ses-plug")
	ctx := t.Context()
	const pluginID = "org.example.current"
	if err := pluginsmod.NewStore(srv.stateDB).LinkPluginSession(ctx, "opencode", id, pluginID); err != nil {
		t.Fatal(err)
	}
	srv.pluginsModule().Bind(ctx)
	srv.onSessionIdle("opencode", id)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var n int
		if err := srv.stateDB.SQL().QueryRowContext(ctx, `SELECT count(*) FROM plugin_session_outbox WHERE plugin_id=? AND session_id=?`, pluginID, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("idle edge did not record the completion")
		}
	}
}

func execAll(t *testing.T, raw *sql.DB, statements []string) {
	t.Helper()
	for _, q := range statements {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}
