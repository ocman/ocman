package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

type namedRemoteFlagsPlatform struct{ *remoteCompactTestPlatform }

func (*namedRemoteFlagsPlatform) NativeKind() platforms.ID { return "claudecode" }
func (*namedRemoteFlagsPlatform) CanonicalSources() (string, string, []string) {
	return "remote", "claudecode", []string{"source"}
}
func (p *namedRemoteFlagsPlatform) RunningSessionCount(_ context.Context, q platforms.SidebarQuery) (int, error) {
	p.query = q
	return len(q.IncludeIDs), nil
}

func TestRemoteConnectionSidebarFlagsAndCountsUseDurableIdentity(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	id := eventCatalogueRow(t, s, "remote", "source")
	const route, durable = "r-remote:cnx_work", "r-remote:claudecode"
	p := &namedRemoteFlagsPlatform{&remoteCompactTestPlatform{fakePlatform: &fakePlatform{PlatformID: route}, rows: []db.Session{{ID: id, Platform: route, RemoteID: "remote", Directory: "/repo", TimeUpdated: 20, Status: db.StatusDone}}}}
	if err := reg.RegisterConnection(route, p); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.PinSession(t.Context(), durable, id); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.MarkSessionSeen(t.Context(), durable, id, 20); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.ArchiveSession(t.Context(), durable, id, 30); err != nil {
		t.Fatal(err)
	}
	rows, err := s.sidebarSessionRows(t.Context(), platforms.SidebarQuery{Since: 100}, false)
	if err != nil || len(rows) != 1 || !rows[0].Pinned || !rows[0].Seen || !rows[0].Archived || rows[0].Platform != route {
		t.Fatalf("remote flags=%+v %v", rows, err)
	}
	if len(p.query.IncludeIDs) != 1 || p.query.IncludeIDs[0] != id {
		t.Fatalf("native pin did not reach owner query: %+v", p.query)
	}
	if n, err := s.runningSessionCount(t.Context(), platforms.SidebarQuery{}); err != nil || n != 1 {
		t.Fatalf("remote count lost pin: %d %v", n, err)
	}
	w := httptest.NewRecorder()
	s.handlePinSession(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(fmt.Sprintf(`{"platform":%q,"sessionId":%q,"pinned":false}`, route, id))))
	if w.Code != http.StatusOK {
		t.Fatalf("unpin: %d %s", w.Code, w.Body.String())
	}
	if n, err := s.runningSessionCount(t.Context(), platforms.SidebarQuery{}); err != nil || n != 0 {
		t.Fatalf("remote count retained native pin: %d %v", n, err)
	}
}

func TestConnectionFlagsUseNativeDurableIdentity(t *testing.T) {
	for _, owner := range []string{"local", "remote"} {
		t.Run(owner, func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			public, durable := "cnx_work", "claudecode"
			if owner == "remote" {
				public, durable = "r-remote:cnx_work", "r-remote:claudecode"
			}
			eventConnection(t, reg, public, owner, "source")
			id := eventCatalogueRow(t, s, owner, "source")
			if err := s.stateDB.PinSession(t.Context(), durable, id); err != nil {
				t.Fatal(err)
			}
			if err := s.stateDB.MarkSessionSeen(t.Context(), durable, id, 20); err != nil {
				t.Fatal(err)
			}
			if err := s.stateDB.ArchiveSession(t.Context(), durable, id, 30); err != nil {
				t.Fatal(err)
			}
			rows := []db.Session{{ID: id, Platform: public, TimeUpdated: 20, Status: db.StatusDone}}
			if err := s.applySessionStateReadOnly(t.Context(), rows); err != nil {
				t.Fatal(err)
			}
			if !rows[0].Pinned || !rows[0].Seen || !rows[0].Archived || rows[0].Platform != public {
				t.Fatalf("migrated flags disappeared: %+v", rows[0])
			}
			for _, mutation := range []struct {
				handle  http.HandlerFunc
				payload string
			}{
				{s.handlePinSession, `"pinned":false`},
				{s.handleSeenSession, `"timeUpdated":40`},
				{s.handleArchiveSession, `"archived":false`},
			} {
				w := httptest.NewRecorder()
				body := fmt.Sprintf(`{"platform":%q,"sessionId":%q,%s}`, public, id, mutation.payload)
				mutation.handle(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
				if w.Code != http.StatusOK {
					t.Fatalf("mutation: %d %s", w.Code, w.Body.String())
				}
			}
			pins, err := s.stateDB.PinnedSessions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			archives, err := s.stateDB.ArchivedSessions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			seen, err := s.stateDB.SeenSessionStates(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			key := state.Key{Platform: durable, SessionID: id}
			if _, ok := pins[key]; ok {
				t.Fatal("connection-key unpin left native pin")
			}
			if _, ok := archives[key]; ok {
				t.Fatal("connection-key unarchive left native archive")
			}
			if seen[key].TimeUpdated != 40 {
				t.Fatalf("seen watermark stored under routing key: %+v", seen)
			}
			if _, ok := seen[state.Key{Platform: public, SessionID: id}]; ok {
				t.Fatal("persisted wire routing identity")
			}
			rows = []db.Session{{ID: id, Platform: public, TimeUpdated: 40, Status: db.StatusDone}}
			if err := s.applyNotifySessionState(t.Context(), rows); err != nil || !rows[0].Seen || rows[0].Archived {
				t.Fatalf("notify differs: %+v %v", rows, err)
			}
			encoded, _ := json.Marshal(rows)
			if !strings.Contains(string(encoded), `"platform":"`+public+`"`) {
				t.Fatal(string(encoded))
			}
		})
	}
}

func TestConnectionStateMutationRejectsWrongSourceAccount(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	eventConnection(t, reg, "cnx_work", "local", "work")
	eventConnection(t, reg, "cnx_personal", "local", "personal")
	id := eventCatalogueRow(t, s, "local", "work")
	w := httptest.NewRecorder()
	s.handlePinSession(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(fmt.Sprintf(`{"platform":"cnx_personal","sessionId":%q,"pinned":true}`, id))))
	if w.Code != http.StatusNotFound {
		t.Fatalf("wrong account changed state: %d %s", w.Code, w.Body.String())
	}
}
