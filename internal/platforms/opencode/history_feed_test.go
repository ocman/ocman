package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Frames captured from OpenCode v2.0.32's /api/event, trimmed to their keys.
var capturedFrames = []string{
	`{"type":"server.connected","data":{}}`,
	`{"type":"session.created","durable":{"aggregateID":"ses_a","seq":0,"version":1},"data":{"sessionID":"ses_a"}}`,
	`{"type":"provider.updated","data":{}}`,
	`{"type":"session.inbox.delivered","durable":{"aggregateID":"ses_a","seq":4,"version":1},"data":{"sessionID":"ses_a","inboxID":"msg_user"}}`,
	`{"type":"session.text.delta","durable":null,"data":{"sessionID":"ses_a","assistantMessageID":"msg_reply","delta":"Hel"}}`,
	`{"type":"session.text.ended","durable":{"aggregateID":"ses_a","seq":7,"version":1},"data":{"sessionID":"ses_a","assistantMessageID":"msg_reply","text":"Hello"}}`,
	`{"type":"session.compaction.ended","durable":{"aggregateID":"ses_a","seq":9,"version":1},"data":{"sessionID":"ses_a"}}`,
	`{"type":"permission.asked","durable":{"aggregateID":"per_1","seq":0,"version":1},"data":{"sessionID":"ses_a"}}`,
	`{"type":"session.deleted","durable":{"aggregateID":"ses_a","seq":10,"version":1},"data":{"sessionID":"ses_a"}}`,
}

var wantChanges = []platforms.HistoryChange{
	{SessionID: "ses_a", Seq: 0},
	{SessionID: "ses_a", Seq: 4, MessageIDs: []string{"msg_user"}},
	{SessionID: "ses_a", Seq: 7, MessageIDs: []string{"msg_reply"}},
	{SessionID: "ses_a", Seq: 9, Resync: true},
	{SessionID: "ses_a", Seq: 10, Deleted: true},
}

func TestHistoryChangeKeepsOnlyDurableSessionEvents(t *testing.T) {
	var got []platforms.HistoryChange
	for _, frame := range capturedFrames {
		if c, ok := historyChange([]byte(frame)); ok {
			got = append(got, c)
		}
	}
	if !reflect.DeepEqual(got, wantChanges) {
		t.Fatalf("changes = %+v\nwant %+v", got, wantChanges)
	}
	if _, ok := historyChange([]byte(`{broken`)); ok {
		t.Fatal("accepted malformed frame")
	}
}

func v2HistoryDB(t *testing.T) *db.DB {
	t.Helper()
	restore := ocv2.SetInstalledV2(true)
	t.Cleanup(restore)
	path := filepath.Join(t.TempDir(), "opencode.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`
CREATE TABLE session_v2 (
	id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', workspace_id TEXT, parent_id TEXT,
	slug TEXT, directory TEXT NOT NULL DEFAULT '', path TEXT, title TEXT, version TEXT,
	share_url TEXT, summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER,
	summary_diffs TEXT, metadata TEXT, cost REAL, tokens_input INTEGER, tokens_output INTEGER,
	tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER,
	revert TEXT, permission TEXT, agent TEXT, model TEXT,
	time_created INTEGER NOT NULL DEFAULT 0, time_updated INTEGER NOT NULL DEFAULT 0,
	time_compacting INTEGER, time_archived INTEGER);
CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT);
CREATE TABLE event_sequence (aggregate_id TEXT PRIMARY KEY, seq INTEGER NOT NULL);
INSERT INTO session_v2 (id, directory, title, time_created, time_updated) VALUES ('ses_a', '/repo', 'A', 1, 2);
INSERT INTO session_message VALUES ('msg_user', 'ses_a', 'user', 0, 1, 1, '{"text":"hi","time":{"created":1}}');
INSERT INTO event_sequence VALUES ('ses_a', 4);`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestWatchHistoryStreamsMachineServerFirehose(t *testing.T) {
	a := New(v2HistoryDB(t), nil, nil, ocapi.Auth{})
	if seqs, err := a.HistorySequences(t.Context()); err != nil || seqs["ses_a"] != 4 {
		t.Fatalf("sequences = %v, %v", seqs, err)
	}
	if snap, err := a.HistoryMessages(t.Context(), "ses_a", []string{"msg_user"}); err != nil || len(snap.Messages) != 1 {
		t.Fatalf("messages = %+v, %v", snap, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/event" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range capturedFrames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
		}
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	withMachineServer(t, port)
	var mu sync.Mutex
	var events []string
	var got []platforms.HistoryChange
	err := a.WatchHistory(t.Context(), func() {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, "connected")
	}, func(c platforms.HistoryChange) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, "change")
		got = append(got, c)
	})
	if err == nil || errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("stream end = %v, want a reconnectable error", err)
	}
	if len(events) == 0 || events[0] != "connected" || !reflect.DeepEqual(got, wantChanges) {
		t.Fatalf("events = %v, changes = %+v", events, got)
	}
}

func TestWatchHistoryRequiresV2MachineServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	v1 := New(newTestDBWithSession(t, "s", "/repo"), nil, nil, ocapi.Auth{})
	if err := v1.WatchHistory(ctx, func() {}, func(platforms.HistoryChange) {}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("v1 watch = %v", err)
	}
	if _, err := v1.HistorySequences(ctx); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("v1 sequences = %v", err)
	}
	withMachineServer(t, "")
	if err := New(v2HistoryDB(t), nil, nil, ocapi.Auth{}).WatchHistory(ctx, func() {}, func(platforms.HistoryChange) {}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("no machine server = %v", err)
	}
}
