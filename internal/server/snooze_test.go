package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSnoozeSessionAndWakeBroadcast(t *testing.T) {
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{id: "r-box:opencode"})
	srv := &Server{stateDB: openTestStateDB(t), registry: registry, broadcastHub: newBroadcastHub()}
	sub, stop := srv.broadcastHub.subscribe()
	defer stop()
	until := time.Now().Add(time.Hour).UnixMilli()
	if err := srv.stateDB.PinSession(t.Context(), "r-box:opencode", "s1"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"platform": "r-box:opencode", "sessionId": "s1", "until": until})
	response := httptest.NewRecorder()
	mux := http.NewServeMux()
	srv.snoozeModule().Routes(moduleRouter{s: srv, mux: mux})
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/session/snooze", strings.NewReader(string(body))))
	if response.Code != http.StatusOK {
		t.Fatalf("snooze: %d %s", response.Code, response.Body.String())
	}
	assertSnoozeEvent(t, sub, true)
	rows := []db.Session{{ID: "s1", Platform: "r-box:opencode"}}
	if err := srv.applySessionState(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if !rows[0].Pinned || rows[0].SnoozedUntil != until {
		t.Fatalf("snoozed pin missing metadata: %+v", rows[0])
	}
	select {
	case <-sub.ch:
		t.Fatal("woke before deadline")
	default:
	}
	if _, err := srv.stateDB.SQL().Exec(`UPDATE archived_session SET snoozed_until = 1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); srv.snoozeModule().Run(ctx) }()
	defer func() { cancel(); <-done }()
	assertSnoozeEvent(t, sub, false)
	if err := srv.applySessionState(t.Context(), rows); err != nil || !rows[0].Pinned || rows[0].SnoozedUntil != 0 {
		t.Fatalf("wake lost pin or kept deadline: %+v, %v", rows[0], err)
	}
}

func assertSnoozeEvent(t *testing.T, sub *broadcastSub, archived bool) {
	t.Helper()
	select {
	case ev := <-sub.ch:
		var payload struct {
			SessionID string `json:"sessionID"`
			Platform  string `json:"platform"`
			Patch     struct {
				Archived     bool  `json:"archived"`
				SnoozedUntil int64 `json:"snoozedUntil"`
			} `json:"patch"`
		}
		if err := json.Unmarshal(ev.data, &payload); err != nil {
			t.Fatal(err)
		}
		if ev.event != "ocman.session.changed" || payload.SessionID != "s1" || payload.Platform != "r-box:opencode" || payload.Patch.Archived != archived || (payload.Patch.SnoozedUntil > 0) != archived {
			t.Fatalf("wrong event: %s %s", ev.event, ev.data)
		}
	case <-time.After(time.Second):
		t.Fatal("missing snooze broadcast")
	}
}

func TestSnoozeSessionValidation(t *testing.T) {
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{id: "opencode"})
	srv := &Server{stateDB: openTestStateDB(t), registry: registry}
	mux := http.NewServeMux()
	srv.snoozeModule().Routes(moduleRouter{s: srv, mux: mux})
	for _, body := range []string{
		`{`,
		`{"platform":"opencode","sessionId":"s1","until":1}`,
		`{"platform":"opencode","sessionId":"../bad","until":9999999999999}`,
		`{"platform":"missing","sessionId":"s1","until":9999999999999}`,
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/session/snooze", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, response.Code)
		}
	}
}

func TestSnoozedSessionsOutsideWindow(t *testing.T) {
	srv, registry := newSessionsTestServer(t)
	until := time.Now().Add(time.Hour).UnixMilli()
	for _, owner := range []string{"opencode", "r-box:opencode"} {
		registry.Register(&summaryPlatform{fakePlatform: fakePlatform{id: owner}, row: mkSession(owner, "old", owner, 1)})
		if err := srv.stateDB.SnoozeSession(t.Context(), owner, "old", until); err != nil {
			t.Fatal(err)
		}
	}
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?since=10000&limit=0", nil))
	var rows []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &rows)
	if rr.Code != http.StatusOK || len(rows) != 2 {
		t.Fatalf("missing old snoozed rows: %d %s", rr.Code, rr.Body)
	}
	for _, row := range rows {
		if row.SnoozedUntil != until || row.Pinned {
			t.Fatalf("wrong snooze metadata: %+v", row)
		}
	}
}

func TestSnoozeActivityDoesNotResurface(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t), registry: platforms.NewRegistry()}
	if err := srv.stateDB.SetSetting(t.Context(), archiveResurfaceKey, "activity"); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.SnoozeSession(t.Context(), "fake", "s1", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, apply := range []func(context.Context, []db.Session) error{srv.applySessionState, srv.applySessionStateReadOnly, srv.applyNotifySessionState} {
		sessions := []db.Session{{ID: "s1", Platform: "fake", TimeUpdated: time.Now().Add(24 * time.Hour).UnixMilli(), Status: db.StatusDone}}
		if err := apply(t.Context(), sessions); err != nil {
			t.Fatal(err)
		}
		archived, err := srv.stateDB.IsSessionArchived(t.Context(), "fake", "s1")
		if err != nil || !archived {
			t.Fatalf("activity cancelled snooze: %v %v", archived, err)
		}
	}
}
