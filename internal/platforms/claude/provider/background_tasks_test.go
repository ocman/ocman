package claudeprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
)

func TestBackgroundTaskCountSnapshot(t *testing.T) {
	l, p := sdkLifecycle(t)
	dir := t.TempDir()
	var err error
	l.sup, err = providersupervisor.New(dir, providersupervisor.Backend{})
	if err != nil {
		t.Fatal(err)
	}
	count := func(want int) {
		t.Helper()
		for _, event := range l.snapshot(p.id) {
			if event.Type != "ocman.session.background" {
				continue
			}
			var props struct {
				SessionID string `json:"sessionID"`
				Count     int    `json:"count"`
			}
			if err := json.Unmarshal(event.Properties, &props); err != nil {
				t.Fatal(err)
			}
			if props.SessionID != p.id || props.Count != want {
				t.Fatalf("background snapshot = %+v, want %d", props, want)
			}
			return
		}
		t.Fatal("missing background task snapshot")
	}
	count(0)
	l.sdkSystem(p, []byte(`{"subtype":"task_started","task_type":"local_agent","task_id":"one"}`))
	count(1)
	l.sdkSystem(p, []byte(`{"subtype":"task_started","task_type":"local_bash","task_id":"two"}`))
	count(2)
	l.sdkSystem(p, []byte(`{"subtype":"task_started","task_type":"in_process_teammate","task_id":"teammate"}`))
	count(2)
	l.sdkSystem(p, []byte(`{"subtype":"task_notification","task_id":"one"}`))
	count(1)
	l.sdkSystem(p, []byte(`{"subtype":"task_updated","task_id":"two","patch":{"status":"failed"}}`))
	count(0)
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("spool files = %v, %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	counts := []int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record struct {
			Event struct {
				Type       string `json:"type"`
				Properties struct {
					Count int `json:"count"`
				} `json:"properties"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record.Event.Type == "ocman.session.background" {
			counts = append(counts, record.Event.Properties.Count)
		}
	}
	if got, _ := json.Marshal(counts); string(got) != `[1,2,1,0]` {
		t.Fatalf("published counts = %s", got)
	}
	// An idle or replaced process cannot publish a stale nonzero count.
	p.tasks["stale"] = true
	p.busy = false
	l.publishBackgroundCount(p, 1)
	count(0)
	delete(l.procs, p.id)
	l.publishBackgroundCount(p, 1)
	count(0)
	after, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil || string(after) != string(data) {
		t.Fatalf("stale task update changed the spool: %v", err)
	}
}

func TestBackgroundTaskSpoolFailureFailsTurn(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := startLive(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"system","subtype":"task_started","task_type":"local_agent","task_id":"one"}}
{"direction":"stdin","message":{"type":"user"}}
`, gate)), nil)
	id := h.create(t)
	h.send(t, id, "hi")
	waitFor(t, "child running", func() bool { return h.procs() == 1 })
	h.spoolDown(t, id)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "child stopped", func() bool { return h.procs() == 0 })
	if life, err := h.adapter.SessionLifecycle(t.Context(), id); err != nil || life.Status != db.StatusError {
		t.Fatalf("lifecycle = %+v %v, want error", life, err)
	}
}
