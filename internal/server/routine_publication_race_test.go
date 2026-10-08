package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/routines"
)

func TestRoutineUpstreamCreationCannotPublishUntaggedList(t *testing.T) {
	for _, blockedPhase := range []string{"creation", "permissions"} {
		t.Run(blockedPhase, func(t *testing.T) {
			srv, _, _ := routineHTTPServer(t)
			blocked, release, listed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			block := func() { close(blocked); <-release }
			srv.registry.Register(&fakePlatform{
				id: "opencode",
				createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					// OpenCode announces the row before returning its creation response.
					srv.broadcastSessionChanged("session-1")
					if blockedPhase == "creation" {
						block()
					}
					return &platforms.CreateSessionResponse{ID: "session-1"}, nil
				},
				setPermissionRulesFn: func(platforms.SetPermissionRulesRequest) error {
					if blockedPhase == "permissions" {
						block()
					}
					return nil
				},
				sessionsHook: func(context.Context, string, int64) ([]db.Session, error) {
					close(listed)
					return []db.Session{{ID: "session-1", Platform: "opencode", Directory: "/repo"}}, nil
				},
			})
			routine, err := srv.routineSvc.Create(t.Context(), routines.Input{Name: "Check", Prompt: "inspect", Directory: "/repo", Enabled: true, Schedule: routines.Schedule{Kind: routines.ScheduleNone}})
			if err != nil {
				t.Fatal(err)
			}
			runDone := make(chan error, 1)
			go func() { _, err := srv.routineSvc.RunNow(t.Context(), routine.ID); runDone <- err }()
			<-blocked
			sub, unsubscribe := srv.broadcastHub.subscribe()
			defer unsubscribe()
			srv.broadcastSessionChanged("session-1")
			<-sub.ch
			response := httptest.NewRecorder()
			listDone := make(chan struct{})
			go func() {
				srv.handleSessions(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
				close(listDone)
			}()
			<-listed
			select {
			case <-listDone:
				t.Errorf("list published while routine %s was still blocked: %s", blockedPhase, response.Body.String())
			case <-time.After(25 * time.Millisecond):
			}
			once.Do(func() { close(release) })
			if err := <-runDone; err != nil {
				t.Fatal(err)
			}
			<-listDone
			var sessions []db.Session
			if err := json.Unmarshal(response.Body.Bytes(), &sessions); err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].RoutineID != routine.ID {
				t.Fatalf("untagged routine list: %s", response.Body.String())
			}
		})
	}
}
