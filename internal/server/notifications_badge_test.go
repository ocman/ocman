package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestNotificationBadgeExcludesRoutineSessions(t *testing.T) {
	s := testServer(t)
	now := time.Now().UnixMilli()
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{
		{ID: "normal", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: now},
		{ID: "routine", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: now},
	}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "message"}}
	s.registry.Register(p)
	if err := s.stateDB.CreateRoutine(t.Context(), state.Routine{ID: "rt", Name: "Review", Prompt: "review", Directory: "/repo", RemoteID: "local", SessionMode: "new", ScheduleKind: "none", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.stateDB.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "rt", Trigger: "manual", State: "running", OccurrenceAt: 1, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.LinkRoutineRun(t.Context(), "run", "fake", "routine", 2, false); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []bool{false, true} {
		p.sessions[1].PendingPermission = prompt
		p.prompts = []platforms.LivePrompt{{"id": "request", "sessionID": "routine"}}
		w := httptest.NewRecorder()
		s.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify?includeDone=1", nil))
		var entries []notifyEntry
		if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil || len(entries) != 1 || entries[0].ID != "normal" {
			t.Fatalf("routine counted in browser snapshot: %s, %v", w.Body.String(), err)
		}
		rows, err := s.pushSnapshot(t.Context())
		if err != nil || len(rows) != 1 || rows[0].Notification.BadgeCount != 1 {
			t.Fatalf("routine counted or alerted by push: %+v, %v", rows, err)
		}
	}
}

func TestPushBadgeIncludesSelectedInboxCategories(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{{ID: "normal", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()}}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "message"}}
	s.registry.Register(p)
	useInboxRemotes(s, []string{"local", "remote"}, func(_ context.Context, source string) ([]state.InboxItem, error) {
		if source == "remote" {
			return []state.InboxItem{{ID: "same", Category: state.InboxGeneral}}, nil
		}
		return []state.InboxItem{
			{ID: "same", Category: state.InboxGeneral}, {ID: "factory", Category: state.InboxFactory},
			{ID: "routine", Category: state.InboxRoutine}, {ID: "permission", Category: state.InboxPermissionCategory},
			{ID: "legacy-permission", Permission: &state.InboxPermission{}},
			{ID: "read", Category: state.InboxGeneral, ReadAt: 1}, {ID: "archived", Category: state.InboxFactory, ArchivedAt: 1},
		}, nil
	})
	for _, tc := range []struct {
		settings string
		want     int
	}{
		{"", 4},
		{`{"sessions":false,"primary":false,"factory":true,"routines":true}`, 2},
		{`{"sessions":false,"primary":false,"factory":false,"routines":false}`, 0},
	} {
		if tc.settings != "" {
			if err := s.stateDB.SetSetting(t.Context(), "notifications.badge", tc.settings); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := s.pushSnapshot(t.Context())
		if err != nil || len(rows) != 1 || rows[0].Notification.BadgeCount != tc.want {
			t.Fatalf("badge with %s = %+v, %v; want %d", tc.settings, rows, err, tc.want)
		}
	}
}

func TestPushBadgeRejectsFailedSettingsAndLocalInboxReads(t *testing.T) {
	for _, failure := range []string{"settings", "inbox"} {
		t.Run(failure, func(t *testing.T) {
			s := testServer(t)
			p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{{ID: "normal", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()}}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "message"}}
			s.registry.Register(p)
			if failure == "settings" {
				if err := s.stateDB.SetSetting(t.Context(), "notifications.badge", "invalid"); err != nil {
					t.Fatal(err)
				}
			} else {
				useInboxRemotes(s, []string{"local"}, func(context.Context, string) ([]state.InboxItem, error) { return nil, errors.New("read failed") })
			}
			if _, err := s.pushSnapshot(t.Context()); err == nil {
				t.Fatal("failed badge read committed as a complete count")
			}
		})
	}
}
