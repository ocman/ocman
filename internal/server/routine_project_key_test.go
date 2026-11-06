package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/state"
)

func TestRoutineLaunchUsesProjectAPIKey(t *testing.T) {
	_, handler, sent := routineHTTPServer(t)
	listed := doRoutineRequest(t, handler, http.MethodGet, "/api/projects", "")
	var projects []db.ProjectStats
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &projects) != nil || len(projects) != 1 {
		t.Fatalf("projects: %d %s", listed.Code, listed.Body.String())
	}
	if projects[0].ProjectKey != "dir:local:/repo" {
		t.Fatalf("key = %q", projects[0].ProjectKey)
	}
	created := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", strings.Replace(validRoutineBody, `"directory":"/repo"`, `"directory":""`, 1))
	var routine state.Routine
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &routine) != nil {
		t.Fatalf("create: %s", created.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"project": projects[0].ProjectKey})
	launched := doRoutineRequest(t, handler, http.MethodPost, "/api/routines/"+routine.ID+"/run", string(body))
	var run state.RoutineRun
	if launched.Code != http.StatusOK || json.Unmarshal(launched.Body.Bytes(), &run) != nil || run.Directory != "/repo" || sent.Load() != 1 {
		t.Fatalf("launch: %d %s", launched.Code, launched.Body.String())
	}
}
