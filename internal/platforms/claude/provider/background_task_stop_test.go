package claudeprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestStopIndividualBackgroundTask(t *testing.T) {
	l := &live{procs: map[string]*proc{}}
	p := &proc{busy: true, tasks: map[string]bool{"one": true, "two": true}, controls: map[string]chan controlResult{}, done: make(chan struct{})}
	l.procs["s1"] = p
	writes := 0
	p.stdin = sdkWriter{func(b []byte) (int, error) {
		writes++
		var req struct {
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
				TaskID  string `json:"task_id"`
			} `json:"request"`
		}
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatal(err)
		}
		if req.Request.Subtype != "stop_task" || req.Request.TaskID != "one" {
			t.Fatalf("wrong control: %s", b)
		}
		response, _ := json.Marshal(map[string]any{"response": map[string]any{"request_id": req.RequestID, "subtype": "success"}})
		l.receiveControl(p, response)
		return len(b), nil
	}}
	request := func(session, task string) error {
		return l.abortRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"TaskID":"`+task+`"}`)), session)
	}
	if err := request("s1", "one"); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || !p.busy || len(p.tasks) != 2 {
		t.Fatal("stop must await authoritative task events, not end the turn")
	}
	for _, session := range []string{"s1", "other"} {
		if err := request(session, "missing"); !errors.Is(err, platforms.ErrNotFound) {
			t.Fatal(err)
		}
	}
	p.busy = false
	if err := request("s1", "one"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatal("wrote an invalid stop")
	}
}

func TestBackgroundTaskDetailsSnapshot(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.sdkSystem(p, []byte(`{"subtype":"task_started","task_type":"local_bash","task_id":"one","description":"pnpm test"}`))
	details := backgroundDetails(p)
	if len(details) != 1 || details[0]["description"] != "pnpm test" {
		t.Fatal(details)
	}
	l.sdkSystem(p, []byte(`{"subtype":"task_notification","task_id":"one"}`))
	if len(backgroundDetails(p)) != 0 || len(p.taskDetails) != 0 {
		t.Fatal("stale task details")
	}
}
