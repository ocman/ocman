package snooze

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestWorkerWakesAndStops(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SnoozeSession(t.Context(), "opencode", "s1", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	events := make(chan json.RawMessage, 2)
	m := NewModule(Deps{Store: db, Broadcast: func(_ string, data []byte) { events <- data }})
	m.wake(t.Context())
	select {
	case <-events:
		t.Fatal("woke early")
	default:
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.run(ctx, time.Millisecond) }()
	defer func() { cancel(); <-done }()
	if _, err := db.SQL().Exec(`UPDATE archived_session SET snoozed_until = 1`); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-events:
		if !strings.Contains(string(data), `"archived":false`) {
			t.Fatalf("event: %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not wake session")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestUnavailableModule(t *testing.T) {
	m := NewModule(Deps{})
	if m.Name() != "snooze" {
		t.Fatal(m.Name())
	}
	m.Run(t.Context())
	mux := moduletest.Mount(m)
	for _, tc := range []struct {
		method string
		status int
	}{{http.MethodPost, 503}, {http.MethodGet, 405}} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(tc.method, "/api/session/snooze", nil))
		if response.Code != tc.status {
			t.Fatalf("%s: %d", tc.method, response.Code)
		}
	}
}

func TestSnoozeRoute(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var events []string
	m := NewModule(Deps{
		Store: db,
		Validate: func(w http.ResponseWriter, _ *http.Request, id, platform string) (string, bool) {
			if id != "s1" {
				http.Error(w, "invalid session", http.StatusBadRequest)
				return "", false
			}
			return platform, true
		},
		Broadcast: func(event string, data []byte) { events = append(events, event+" "+string(data)) },
	})
	mux := moduletest.Mount(m)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{`, 400},
		{`{"sessionId":"s1","until":1}`, 400},
		{`{"sessionId":"bad","until":9999999999999}`, 400},
		{`{"platform":"r-box:opencode","sessionId":"s1","until":9999999999999}`, 200},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/session/snooze", strings.NewReader(tc.body)))
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, response.Code, response.Body.String())
		}
	}
	if len(events) != 1 || !strings.Contains(events[0], `ocman.session.changed`) || !strings.Contains(events[0], `"platform":"r-box:opencode"`) || !strings.Contains(events[0], `"archived":true`) {
		t.Fatalf("events: %v", events)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/session/snooze", strings.NewReader(`{"sessionId":"s1","until":9999999999999}`)))
	if response.Code != 500 || len(events) != 1 {
		t.Fatalf("failed snooze: %d %v", response.Code, events)
	}
	m.wake(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.Run(ctx)
	m.wake(ctx)
}
