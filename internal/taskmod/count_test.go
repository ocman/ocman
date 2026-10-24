package taskmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func TestActiveCounterIncludesQueuedAndRunningWithoutListLimit(t *testing.T) {
	d := statetest.Open(t, tasksvc.Schema)
	m := New(tasksvc.New(d, 1))
	// The counter reads scalar status, not task JSON or a capped task list.
	_, err := d.SQL().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<205)
	 INSERT INTO background_task SELECT printf('queued-%d',x),'test','','queued',x,'{}' FROM n;
	 INSERT INTO background_task VALUES('running','test','','running',1,'{}'),('done','test','','succeeded',1,'{}'),('failed','test','','failed',1,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: mux})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks?view=active-count", nil))
	var got struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Count != 206 {
		t.Fatalf("counter=%s status=%d err=%v", w.Body, w.Code, err)
	}
}
