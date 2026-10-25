package ocv2

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the actual HTTP stream/status boundary used by the watcher: it
// gets stream headers, seeds status independently, then reads buffered events.
func TestBackgroundExitBetweenReconnectSnapshots(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"}]`)
	f.events = []string{fakeEv("shell.exited", 1, "/repo", `{"id":"sh_1","status":"exited"}`)}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	var running atomic.Bool
	running.Store(true)
	f.on("GET /api/shell", func(fakeCall) (int, any) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release // pause the translator's ownership snapshot
			if !running.Load() {
				return http.StatusOK, map[string]any{"data": []any{}}
			}
		} else {
			// The watcher sees the running shell. It exits immediately
			// afterward, before a paused translator can finish its read.
			running.Store(false)
			unblock()
		}
		return http.StatusOK, map[string]any{"data": []any{
			map[string]any{"id": "sh_1", "status": "running", "metadata": map[string]any{"sessionID": "s1"}},
		}}
	})
	client := &http.Client{Transport: Wrap(http.DefaultTransport), Timeout: 5 * time.Second}
	type result struct {
		response *http.Response
		err      error
	}
	responses := make(chan result, 1)
	readDone := make(chan struct{})
	defer close(readDone)
	go func() {
		resp, err := client.Get(f.URL + "/global/event")
		if err != nil {
			responses <- result{nil, err}
			return
		}
		defer resp.Body.Close()
		responses <- result{resp, err}
		<-readDone
	}()
	<-entered
	var stream result
	select {
	case stream = <-responses:
		// The old implementation exposes headers while its snapshot is
		// paused, allowing watcher seeding to overtake it.
	case <-time.After(100 * time.Millisecond):
		// Ordered bootstrap cannot expose headers yet. Finish ownership
		// first, then allow the watcher to take its later snapshot.
		unblock()
		stream = <-responses
	}
	if stream.err != nil {
		t.Fatal(stream.err)
	}
	cmpJSON(t, "watcher seed", cmpOK(t, http.MethodGet, f.URL+"/session/status", ""), `{}`)
	state, idle := "idle", false
	body, err := io.ReadAll(stream.response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range strings.Split(string(body), "\n\n") {
		var ev map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &ev) != nil {
			continue
		}
		ev = obj(ev, "payload")
		if str(ev, "type") == "session.status" {
			state = str(obj(obj(ev, "properties"), "status"), "type")
		}
		idle = idle || str(ev, "type") == "session.idle"
	}
	if state != "idle" || idle {
		t.Fatalf("shell exit changed foreground status to %q, idle edge = %v", state, idle)
	}
}
