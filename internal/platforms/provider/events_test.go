package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func (f *fakeProvider) frames(frames ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		for i, frame := range frames {
			if i+1 > after {
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i+1, frame)
			}
		}
	}
}

func TestEventContractAndReplay(t *testing.T) {
	f := newFake(t, allCapabilities())
	frames := []string{
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"permission.asked","properties":{"id":"p","sessionID":"s","metadata":{"command":"pwd"}}}`,
		`{"type":"question.asked","properties":{"id":"q","sessionID":"s","questions":[{"question":"which?"}]}}`,
	}
	f.frames(frames...)
	a := f.adapter(t)
	var statuses []db.SessionStatus
	idles := 0
	a.SetSessionEventSink(platforms.SessionEventSink{
		OnSessionStatus: func(platform, id string, status db.SessionStatus, resets int64) {
			if platform != "fake" || a.LiveStatus(id).Status != string(status) {
				t.Error("sink ran before live update")
			}
			if id == "s" {
				statuses = append(statuses, status)
			}
			if status == db.StatusLimited && resets != 1234 {
				t.Error("lost reset time", resets)
			}
		},
		OnSessionIdle: func(_, _ string) { idles++ },
	})
	var output bytes.Buffer
	flushes := 0
	requireNoError(t, a.ProxyEvents(t.Context(), "s", &output, func() { flushes++ }))
	if flushes != 3 || !strings.Contains(output.String(), frames[1]) {
		t.Fatal(output.String())
	}
	permissions, err := a.ListPermissions(t.Context(), "s")
	requireNoError(t, err)
	questions, err := a.ListQuestions(t.Context(), "s")
	requireNoError(t, err)
	if len(permissions) != 1 || len(questions) != 1 || !a.Knows("s") {
		t.Fatal(permissions, questions)
	}
	permissions[0]["metadata"].(map[string]any)["command"] = "corrupted"
	again, _ := a.ListPermissions(t.Context(), "s")
	if again[0]["metadata"].(map[string]any)["command"] != "pwd" {
		t.Fatal("caller mutated cache")
	}
	live := a.LiveStatus("s")
	if !live.PendingPermission || !live.PendingQuestion {
		t.Fatal(live)
	}
	live.Status = "corrupted"
	if a.LiveStatus("s").Status != "busy" {
		t.Fatal("caller mutated live cache")
	}
	if a.LiveStatus("unknown") != nil {
		t.Fatal("unknown live status")
	}
	statuses = nil
	f.mu.Lock()
	callCount := len(f.calls)
	f.mu.Unlock()
	_, _ = a.ListPermissions(t.Context(), "s")
	_, _ = a.ListQuestions(t.Context(), "s")
	f.mu.Lock()
	if len(f.calls) != callCount {
		t.Error("pending lists used network")
	}
	f.mu.Unlock()
	frames = append(frames,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"limited","resetsAt":1234}}}`,
		`{"type":"session.idle","properties":{"sessionID":"s"}}`,
		`{"type":"permission.replied","properties":{"sessionID":"s","requestID":"p","reply":"reject","reason":"cancelled"}}`,
		`{"type":"question.replied","properties":{"sessionID":"s","requestID":"q"}}`,
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
		`{"type":"session.idle","properties":{"sessionID":"s"}}`,
		`{"type":"message.updated","properties":{"info":{"id":"m","sessionID":"s","role":"assistant"}}}`,
		`{"type":"message.part.updated","properties":{"part":{"id":"part","sessionID":"s","type":"text","text":"hi"}}}`,
		`{"type":"message.part.delta","properties":{"sessionID":"s","messageID":"m","partID":"part","field":"text","delta":"!"}}`,
		`{"type":"question.asked","properties":{"id":"q2","sessionID":"s"}}`,
		`{"type":"question.rejected","properties":{"sessionID":"s","requestID":"q2"}}`,
		`{"type":"session.status","properties":{"sessionID":"other","status":{"type":"retry"}}}`,
		// A failed turn's idle edge is delivered and keeps the error status.
		`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"error"}}}`,
		`{"type":"session.idle","properties":{"sessionID":"s"}}`,
	)
	f.frames(frames...)
	output.Reset()
	requireNoError(t, a.ProxyEvents(t.Context(), "s", &output, nil))
	if idles != 2 || !reflectStatuses(statuses, db.StatusLimited, db.StatusBusy, db.StatusDone, db.StatusError) || a.LiveStatus("s").Status != string(db.StatusError) {
		t.Fatal(idles, statuses)
	}
	if strings.Contains(output.String(), `"other"`) || !strings.Contains(output.String(), `"message.part.delta"`) {
		t.Fatal(output.String())
	}
	if strings.Count(output.String(), `"type":"session.idle"`) != 2 {
		t.Fatal("limited idle leaked to browser", output.String())
	}
	permissions, _ = a.ListPermissions(t.Context(), "s")
	questions, _ = a.ListQuestions(t.Context(), "s")
	if len(permissions) != 0 || len(questions) != 0 {
		t.Fatal(permissions, questions)
	}
	if a.LiveStatus("s").PendingPermission || a.LiveStatus("s").PendingQuestion {
		t.Fatal("pending flags not cleared")
	}
	requireNoError(t, a.ProxyEvents(t.Context(), "s", io.Discard, nil))
	if idles != 2 {
		t.Fatal("replay duplicated idle")
	}
}

func reflectStatuses(actual []db.SessionStatus, expected ...db.SessionStatus) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func TestProviderInstancesAreIsolated(t *testing.T) {
	f1, f2 := newFake(t, allCapabilities()), newFake(t, allCapabilities())
	f1.reply("GET", "/sessions", []db.Session{{ID: "only-first", Directory: "/first"}, {ID: "shared", Directory: "/first"}})
	f2.reply("GET", "/sessions", []db.Session{{ID: "only-second", Directory: "/second"}, {ID: "shared", Directory: "/second"}})
	f1.frames(`{"type":"session.status","properties":{"sessionID":"shared","status":{"type":"busy"}}}`, `{"type":"permission.asked","properties":{"sessionID":"shared","id":"p"}}`)
	f2.frames(`{"type":"session.status","properties":{"sessionID":"shared","status":{"type":"limited","resetsAt":456}}}`)
	a1, a2 := f1.adapter(t), f2.adapter(t)
	_, err := a1.Sessions(t.Context(), "", 0)
	requireNoError(t, err)
	_, err = a2.Sessions(t.Context(), "", 0)
	requireNoError(t, err)
	requireNoError(t, a1.ProxyEvents(t.Context(), "", io.Discard, nil))
	requireNoError(t, a2.ProxyEvents(t.Context(), "", io.Discard, nil))
	if a1.Knows("only-second") || a2.Knows("only-first") {
		t.Fatal("ownership leaked")
	}
	if a1.LiveStatus("shared").Status != "busy" || a2.LiveStatus("shared").Status != "limited" {
		t.Fatal("live cache or event cursor leaked")
	}
	if a2.LiveStatus("shared").ResetsAt != 456 {
		t.Fatal("limited reset time missing from event cache")
	}
	p1, _ := a1.ListPermissions(t.Context(), "shared")
	p2, _ := a2.ListPermissions(t.Context(), "shared")
	if len(p1) != 1 || len(p2) != 0 {
		t.Fatal("pending cache leaked")
	}
}

func TestWatchEventsReconnectsAndCancels(t *testing.T) {
	f := newFake(t, allCapabilities())
	replayed := make(chan string, 1)
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Query().Get("after") == "0" {
			_, _ = io.WriteString(w, "id: 1\ndata: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"s\",\"status\":{\"type\":\"busy\"}}}\n\n")
			return
		}
		replayed <- r.URL.Query().Get("after")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	a := f.adapter(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.WatchEvents(ctx) }()
	select {
	case after := <-replayed:
		if after != "1" {
			t.Fatal(after)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reconnect")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not cancel")
	}
}

func TestEventValidation(t *testing.T) {
	for _, frame := range []string{
		"id: nope\ndata: {}\n\n", "id: 0\ndata: {}\n\n", "data: {}\n\n",
		"id: 1\ndata: invalid\n\n", "id: 1\ndata: {\"type\":\"session.status\",\"properties\":null}\n\n",
		"id: 1\ndata: {\"type\":\"session.status\",\"properties\":\"bad\"}\n\n",
		"id: 1\ndata: {\"type\":\"permission.asked\",\"properties\":{}}\n\n",
		"id: 1\ndata: {\"type\":\"permission.replied\",\"properties\":{}}\n\n",
		"id: 1\ndata: {\"type\":\"session.idle\",\"properties\":{}}\n\n",
		"id: 1\ndata: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"s\",\"status\":{\"type\":\"unknown\"}}}\n\n",
		"id: 1\ndata: {}\n\nid: 1\ndata: {}\n\n",
	} {
		t.Run(frame, func(t *testing.T) {
			f := newFake(t, allCapabilities())
			f.events = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, frame)
			}
			a := f.adapter(t)
			if err := a.ProxyEvents(t.Context(), "", io.Discard, nil); err == nil {
				t.Fatal("accepted invalid event")
			}
		})
	}
	f := newFake(t, allCapabilities())
	f.frames(`{"type":"message.part.delta","properties":{"sessionID":"s","delta":"hi"}}`)
	a := f.adapter(t)
	if err := a.ProxyEvents(t.Context(), "s", failingWriter{}, nil); err == nil {
		t.Fatal("lost writer failure")
	}
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
	if err := a.ProxyEvents(t.Context(), "s", io.Discard, nil); err == nil {
		t.Fatal("accepted non-SSE response")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }

func TestSSEMultilineAndConcurrentConsumers(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Query().Get("after") != "0" {
			return
		}
		_, _ = io.WriteString(w, ": heartbeat\r\nid: 1\r\ndata: {\"type\":\"session.status\",\r\ndata: \"properties\":{\"sessionID\":\"s\",\"status\":{\"type\":\"done\"}}}\r\n\r\n")
	}
	a := f.adapter(t)
	count := 0
	a.SetSessionEventSink(platforms.SessionEventSink{OnSessionStatus: func(_, _ string, _ db.SessionStatus, _ int64) { count++ }})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.ProxyEvents(t.Context(), "s", io.Discard, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatal("duplicate sink notification", count)
	}
	f2 := newFake(t, allCapabilities())
	f2.events = f.events
	a2 := f2.adapter(t)
	var output bytes.Buffer
	requireNoError(t, a2.ProxyEvents(t.Context(), "s", &output, nil))
	data := strings.Split(strings.TrimSpace(output.String()), "data: ")
	if len(data) != 2 || !json.Valid([]byte(data[1])) {
		t.Fatal("invalid multiline output", output.String())
	}
}

func TestPromptReconciliationAndReplies(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.reply("GET", "/permissions", []platforms.LivePrompt{{"id": "p", "sessionID": "s"}, {"id": "p2", "sessionID": "s"}})
	f.reply("GET", "/questions", []platforms.LivePrompt{{"id": "q", "sessionID": "s"}})
	a := f.adapter(t)
	_, err := a.RefreshPermissions(t.Context(), "s")
	requireNoError(t, err)
	_, err = a.RefreshQuestions(t.Context(), "s")
	requireNoError(t, err)
	prompts, err := a.ListPermissions(t.Context(), "s")
	requireNoError(t, err)
	if len(prompts) != 2 || prompts[0]["id"] != "p" {
		t.Fatal(prompts)
	}
	requireNoError(t, a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p", Reply: "once"}))
	requireNoError(t, a.RespondQuestion(t.Context(), platforms.RespondQuestionRequest{SessionID: "s", RequestID: "q"}))
	f.reply("GET", "/questions", []platforms.LivePrompt{{"id": "q2", "sessionID": "s"}})
	_, err = a.RefreshQuestions(t.Context(), "s")
	requireNoError(t, err)
	requireNoError(t, a.RejectQuestion(t.Context(), platforms.RejectQuestionRequest{SessionID: "s", RequestID: "q2"}))
	f.reply("GET", "/permissions", []platforms.LivePrompt{})
	_, err = a.RefreshPermissions(t.Context(), "s")
	requireNoError(t, err)
	prompts, _ = a.ListPermissions(t.Context(), "s")
	if len(prompts) != 0 {
		t.Fatal("reconciliation left resolved prompt")
	}
	questions, _ := a.ListQuestions(t.Context(), "s")
	if len(questions) != 0 {
		t.Fatal(questions)
	}
	f.reply("GET", "/permissions", []platforms.LivePrompt{{"id": "bad", "sessionID": "other"}})
	if _, err := a.RefreshPermissions(t.Context(), "s"); err == nil {
		t.Fatal("accepted wrong session prompt")
	}
	f.reply("GET", "/permissions", []platforms.LivePrompt{{"sessionID": "s"}})
	if _, err := a.RefreshPermissions(t.Context(), "s"); err == nil {
		t.Fatal("accepted missing prompt id")
	}
	// Still a JSON object with arbitrary nested metadata on the public contract.
	if _, err := json.Marshal(prompts); err != nil {
		t.Fatal(err)
	}
}
