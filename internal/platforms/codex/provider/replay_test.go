package codexprovider

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type wire struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// recording is a recorded transcript split into what the fake replays: the
// result of each request ocman makes and the server traffic after each
// turn-starting request, up to the next one.
type recording struct {
	dir     string
	results map[string][]json.RawMessage // by method, in order
	turns   [][]wire
}

// turnStarts are the requests Codex answers with a turn.
var turnStarts = map[string]bool{codexappserver.MethodTurnStart: true, codexappserver.MethodShellCommand: true,
	codexappserver.MethodThreadCompact: true, codexappserver.MethodReviewStart: true}

func loadRecording(t *testing.T, dir string) *recording {
	t.Helper()
	f, err := os.Open(dir + "transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rec := &recording{dir: dir, results: map[string][]json.RawMessage{}}
	sent := map[string]string{}
	turn := -1
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 16<<20)
	for scanner.Scan() {
		var line struct {
			Dir string `json:"dir"`
			Msg wire   `json:"msg"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		m := line.Msg
		switch {
		case line.Dir == "send" && m.Method != "":
			sent[string(m.ID)] = m.Method
			if turnStarts[m.Method] {
				turn++
				rec.turns = append(rec.turns, nil)
			}
		case line.Dir == "recv" && m.Method == "" && sent[string(m.ID)] != "":
			method := sent[string(m.ID)]
			rec.results[method] = append(rec.results[method], m.Result)
		case line.Dir == "recv" && m.Method != "" && turn >= 0:
			rec.turns[turn] = append(rec.turns[turn], m)
		}
	}
	return rec
}

// replayer serves a recording: it answers requests with recorded results and
// replays each turn's traffic after its turn/start, waiting for ocman's
// answer to every recorded server request.
type replayer struct {
	t   *testing.T
	rec *recording

	mu        sync.Mutex
	calls     map[string]int
	decisions []json.RawMessage
}

func (r *replayer) setup(s *codextest.Server) {
	for method := range r.rec.results {
		if turnStarts[method] {
			s.Handle(method, r.turn(s, method))
			continue
		}
		s.Handle(method, r.result(method))
	}
	for _, f := range []struct{ method, file string }{
		{codexappserver.MethodThreadRead, "thread-read.json"}, {codexappserver.MethodThreadTurns, "turns.json"},
		{codexappserver.MethodThreadItems, "items.json"}} {
		data, err := os.ReadFile(r.rec.dir + f.file)
		if err != nil {
			r.t.Fatal(err)
		}
		s.Handle(f.method, func(json.RawMessage) (any, error) { return json.RawMessage(data), nil })
	}
}

// turn answers a turn-starting request with its recorded result and replays
// the traffic recorded after the next turn start, in recording order.
func (r *replayer) turn(s *codextest.Server, method string) codextest.Method {
	return func(json.RawMessage) (any, error) {
		r.mu.Lock()
		n, k := r.calls["turns"], r.calls[method]
		r.calls["turns"]++
		r.calls[method]++
		r.mu.Unlock()
		go r.play(s, r.rec.turns[n])
		return r.rec.results[method][k], nil
	}
}

func (r *replayer) result(method string) codextest.Method {
	return func(json.RawMessage) (any, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		results := r.rec.results[method]
		n := min(r.calls[method], len(results)-1)
		r.calls[method]++
		return results[n], nil
	}
}

func (r *replayer) play(s *codextest.Server, traffic []wire) {
	time.Sleep(10 * time.Millisecond) // let the turn/start response go first, as recorded
	for _, m := range traffic {
		if len(m.ID) == 0 {
			_ = s.Notify(m.Method, m.Params)
			continue
		}
		reply, err := s.RequestID(m.ID, m.Method, m.Params)
		if err != nil {
			return
		}
		select {
		case got := <-reply:
			r.mu.Lock()
			r.decisions = append(r.decisions, got.Result)
			r.mu.Unlock()
		case <-time.After(10 * time.Second):
			r.t.Errorf("ocman did not answer %s", m.Method)
			return
		}
	}
}

func (r *replayer) answered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, d := range r.decisions {
		out = append(out, string(d))
	}
	return out
}

// TestRecordedSession drives the recorded session end to end over the
// protocol socket: create, two turns with approvals decided through ocman's
// prompt replies, the interrupt the second turn ends with, and the stored
// detail read back.
func TestRecordedSession(t *testing.T) {
	r := &replayer{t: t, rec: loadRecording(t, "testdata/0.164.0/"), calls: map[string]int{}}
	h := newHarness(t, r.setup)

	var created platforms.CreateSessionResponse
	if code := h.call(http.MethodPost, "/sessions", platforms.CreateSessionRequest{Directory: "/fixture/repo"}, &created); code != 200 {
		t.Fatalf("create = %d", code)
	}
	thread := loadRecordingThread(t)
	if created.ID != thread {
		t.Fatalf("created %q, recorded %q", created.ID, thread)
	}
	if code := h.call(http.MethodPost, "/sessions/"+thread+"/send", platforms.SendMessageRequest{Message: "go", Model: "openai/gpt-6.1-sol"}, nil); code != 204 {
		t.Fatalf("send = %d", code)
	}
	h.next("session.status", func(p map[string]any) bool { return p["status"].(map[string]any)["type"] == "busy" })
	user := h.next("message.updated", func(p map[string]any) bool { return info(p)["role"] == "user" })
	if !strings.HasPrefix(info(user)["id"].(string), "msg_") {
		t.Fatalf("user message = %v", user)
	}
	// The ls approval reaches ocman as an exact bash pattern.
	asked := h.next("permission.asked", nil)
	if asked["permission"] != "bash" || asked["patterns"].([]any)[0] != "ls" || asked["sessionID"] != thread {
		t.Fatalf("ls prompt = %v", asked)
	}
	var pending []map[string]any
	h.call(http.MethodGet, "/permissions?sessionID="+thread, nil, &pending)
	if len(pending) != 1 || pending[0]["id"] != asked["id"] {
		t.Fatalf("pending = %v", pending)
	}
	if code := h.call(http.MethodPost, "/permissions/"+asked["id"].(string)+"/reply", platforms.RespondPermissionRequest{Reply: "once"}, nil); code != 204 {
		t.Fatalf("reply = %d", code)
	}
	if replied := h.next("permission.replied", nil); replied["requestID"] != asked["id"] || replied["reply"] != "once" {
		t.Fatalf("replied = %v", replied)
	}
	// A second answer finds nothing.
	if code := h.call(http.MethodPost, "/permissions/"+asked["id"].(string)+"/reply", platforms.RespondPermissionRequest{Reply: "once"}, nil); code != 404 {
		t.Fatalf("second reply = %d", code)
	}
	edit := h.next("permission.asked", nil)
	if edit["permission"] != "edit" || edit["patterns"].([]any)[0] != "/fixture/repo/hello.txt" {
		t.Fatalf("edit prompt = %v", edit)
	}
	h.call(http.MethodPost, "/permissions/"+edit["id"].(string)+"/reply", platforms.RespondPermissionRequest{Reply: "always"}, nil)
	final := h.next("message.updated", func(p map[string]any) bool { return info(p)["finish"] == "stop" })
	if i := info(final); i["providerID"] != "openai" || i["modelID"] != "gpt-6.1-sol" {
		t.Fatalf("live reply attribution = %v", i)
	}
	if tokens := info(final)["tokens"].(map[string]any); tokens["input"].(float64) <= 0 || tokens["cache"].(map[string]any)["read"].(float64) <= 0 {
		t.Fatalf("final reply tokens = %v", tokens)
	}
	h.next("session.idle", nil)

	// Second turn: rejecting the approval; Codex interrupts the turn.
	h.call(http.MethodPost, "/sessions/"+thread+"/send", platforms.SendMessageRequest{Message: "sleep"}, nil)
	sleep := h.next("permission.asked", nil)
	if sleep["patterns"].([]any)[0] != "sleep 30" {
		t.Fatalf("sleep prompt = %v", sleep)
	}
	h.call(http.MethodPost, "/permissions/"+sleep["id"].(string)+"/reply", platforms.RespondPermissionRequest{Reply: "reject"}, nil)
	h.next("session.status", func(p map[string]any) bool { return p["status"].(map[string]any)["type"] == "interrupted" })
	h.next("session.idle", nil)

	want := []string{`{"decision":"accept"}`, `{"decision":"accept"}`, `{"decision":"decline"}`}
	if got := r.answered(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("decisions = %q", got)
	}
	var turnParams codexappserver.TurnStartParams
	for _, c := range h.fake().Calls() {
		if c.Method == codexappserver.MethodTurnStart {
			_ = json.Unmarshal(c.Params, &turnParams)
			break
		}
	}
	if turnParams.Model != "gpt-6.1-sol" || turnParams.Input[0].Text != "go" {
		t.Fatalf("turn/start = %+v", turnParams)
	}

	var detail platforms.SessionDetail
	if code := h.call(http.MethodGet, "/sessions/"+thread+"?limit=2&offset=0", nil, &detail); code != 200 {
		t.Fatalf("detail = %d", code)
	}
	if detail.TotalMessages != 4 || len(detail.Messages) != 2 || detail.Session.Status != "interrupted" || !detail.Session.LiveConnection {
		t.Fatalf("detail = %d messages of %d, status %q", len(detail.Messages), detail.TotalMessages, detail.Session.Status)
	}
	var life platforms.SessionLifecycle
	h.call(http.MethodGet, "/sessions/"+thread+"/lifecycle", nil, &life)
	if life.LatestMessageRole != "assistant" || life.Status != "interrupted" {
		t.Fatalf("lifecycle = %+v", life)
	}
	var live []map[string]any
	h.call(http.MethodGet, "/sessions?live=1", nil, &live)
	if len(live) != 1 || live[0]["status"] != "interrupted" {
		t.Fatalf("live = %v", live)
	}
	var snap providerprotocol.Snapshot
	if code := h.call(http.MethodGet, "/sessions/"+thread+"/snapshot", nil, &snap); code != 200 {
		t.Fatalf("snapshot = %d", code)
	}
}

func loadRecordingThread(t *testing.T) string {
	t.Helper()
	return load[codexappserver.ThreadReadResponse](t, "testdata/0.164.0/thread-read.json").Thread.ID
}
