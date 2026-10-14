package providersupervisor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func supervisor(t *testing.T) *Supervisor {
	t.Helper()
	s, err := New(t.TempDir(), Backend{Hello: providerprotocol.Hello{PlatformID: "fake", ProtocolVersion: providerprotocol.CurrentVersion, CLIVersion: "test"}, Snapshot: func(context.Context, string) ([]providerprotocol.Event, error) {
		return []providerprotocol.Event{{Type: "message.part.updated", Properties: json.RawMessage(`{"part":{"sessionID":"s","id":"p","text":"snapshot"}}`)}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func publish(t *testing.T, s *Supervisor, kind, props string) {
	t.Helper()
	if err := s.Publish(providerprotocol.Event{Type: kind, Properties: json.RawMessage(props)}, nil); err != nil {
		t.Fatal(err)
	}
}

func request(s *Supervisor, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestSpoolRequiresCompletedAckAndRetainsSnapshotGap(t *testing.T) {
	s := supervisor(t)
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"s","id":"p","text":""}}`)
	w := request(s, "GET", "/sessions/s/snapshot", "")
	var snapshot providerprotocol.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Cursor != 1 || snapshot.Lease == "" || len(snapshot.Events) != 1 {
		t.Fatal(snapshot)
	}
	publish(t, s, "message.part.delta", `{"sessionID":"s","delta":"gap"}`)
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	for _, body := range []string{`{"sequence":1}`, `{"sequence":4}`, `{"sequence":0}`, `bad`} {
		if w := request(s, "POST", "/sessions/s/ack", body); w.Code < 400 {
			t.Fatal(w.Code, body)
		}
	}
	if w := request(s, "POST", "/sessions/s/ack", `{"sequence":3}`); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.sessions["s"].records) != 3 {
		t.Fatal("pruned before snapshot claimed its cursor")
	}
	// Reconnect with the snapshot lease and consume the events after it. The
	// lease pins the initialization too, keeping the whole turn until applied.
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+fmt.Sprintf("/events?after=1&lease=%s", snapshot.Lease), nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(resp.Body)
	count := 0
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			count++
			if count == 1 && !strings.Contains(scanner.Text(), "gap") {
				t.Fatal(scanner.Text())
			}
			if count == 2 {
				break
			}
		}
	}
	_ = resp.Body.Close()
	if count != 2 {
		t.Fatal(count, scanner.Err())
	}
	// Closing the client body does not join server-side lease cleanup.
	server.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prune(); err != nil {
		t.Fatal(err)
	}
	if len(s.sessions["s"].records) != 0 {
		t.Fatal("completed acknowledged turn retained")
	}
	data, err := os.ReadFile(s.path("s"))
	if err != nil || len(data) != 0 {
		t.Fatal(string(data), err)
	}
	// Sequence remains monotonic after truncation.
	if err := s.publish(providerprotocol.Event{Type: "session.status", Properties: json.RawMessage(`{"sessionID":"s","status":{"type":"busy"}}`)}, nil); err != nil {
		t.Fatal(err)
	}
	if s.seq != 4 {
		t.Fatal(s.seq)
	}
}

func TestPendingPromptsBlockTruncationAndDrain(t *testing.T) {
	s := supervisor(t)
	publish(t, s, "permission.asked", `{"sessionID":"s","id":"p"}`)
	publish(t, s, "question.asked", `{"sessionID":"s","id":"q"}`)
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	if w := request(s, "POST", "/sessions/s/ack", `{"sequence":3}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if len(s.sessions["s"].records) != 3 {
		t.Fatal("lost unanswered prompt")
	}
	w := request(s, "GET", "/sessions/s/snapshot", "")
	if !strings.Contains(w.Body.String(), "permission.asked") || !strings.Contains(w.Body.String(), "question.asked") {
		t.Fatal(w.Body.String())
	}
	w = request(s, "POST", "/drain", "")
	if !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal(w.Body.String())
	}
	if w := request(s, "POST", "/sessions/s/send", ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/permissions/p/reply", ""); w.Code != 501 {
		t.Fatal("reply blocked by drain", w.Code)
	}
	publish(t, s, "permission.replied", `{"sessionID":"s","requestID":"p"}`)
	publish(t, s, "question.rejected", `{"sessionID":"s","requestID":"q"}`)
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestDrainWaitsForAdmittedTurnAndLimitedState(t *testing.T) {
	s := supervisor(t)
	entered, release := make(chan struct{}), make(chan struct{})
	s.backend.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })
	done := make(chan struct{})
	go func() { request(s, "POST", "/sessions/s/send", ""); close(done) }()
	<-entered
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal(w.Body.String())
	}
	publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"limited"}}`)
	close(release)
	<-done
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal("limited turn settled", w.Body.String())
	}
	publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestBeginTurnOwnsAsynchronousWorkBeforeHandlerReturns(t *testing.T) {
	s := supervisor(t)
	s.backend.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.BeginTurn("s", nil); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		// The child is scheduled later; the start response already succeeds.
		w.WriteHeader(http.StatusNoContent)
	})
	if w := request(s, "POST", "/sessions/s/send", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":false`) {
		t.Fatal("accepted asynchronous turn lost", w.Body.String())
	}
	if err := s.BeginTurn("other", nil); err == nil {
		t.Fatal("started during drain")
	}
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestLimitedIdleIsNotAnAcknowledgableBoundary(t *testing.T) {
	s := supervisor(t)
	publish(t, s, "message.part.updated", `{"part":{"sessionID":"s","id":"p","text":"unfinished"}}`)
	publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"limited"}}`)
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	if w := request(s, "POST", "/sessions/s/ack", `{"sequence":3}`); w.Code != 409 {
		t.Fatal("unfinished turn accepted", w.Code)
	}
	if len(s.sessions["s"].records) != 3 {
		t.Fatal("limited turn truncated")
	}
	publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
	publish(t, s, "session.idle", `{"sessionID":"s"}`)
	if w := request(s, "POST", "/sessions/s/ack", `{"sequence":5}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
}

func TestSnapshotAndCursorValidation(t *testing.T) {
	s := supervisor(t)
	for _, path := range []string{"/events?after=no", "/events?after=1", "/events?after=0&lease=unknown", "/foo/snapshot", "/sessions/a/b/snapshot"} {
		if w := request(s, "GET", path, ""); w.Code < 400 {
			t.Fatal(path, w.Code)
		}
	}
	s.mu.Lock()
	lease, err := s.newLease(0, time.Now().Add(-time.Second))
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if w := request(s, "GET", "/events?after=0&lease="+lease, ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	s.backend.Snapshot = nil
	if w := request(s, "GET", "/sessions/s/snapshot", ""); w.Code != 501 {
		t.Fatal(w.Code)
	}
	s.backend.Snapshot = func(context.Context, string) ([]providerprotocol.Event, error) {
		return nil, fmt.Errorf("snapshot failed")
	}
	if w := request(s, "GET", "/sessions/s/snapshot", ""); w.Code != 500 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/hello", ""); !strings.Contains(w.Body.String(), `"replay":true`) {
		t.Fatal(w.Body.String())
	}
}

func TestPublishValidationAndDiskFailure(t *testing.T) {
	s := supervisor(t)
	for _, props := range []string{`invalid`, `{}`, `{"sessionID":"s"}`} {
		if err := s.Publish(providerprotocol.Event{Type: "permission.asked", Properties: json.RawMessage(props)}, nil); err == nil {
			t.Fatal(props)
		}
	}
	if err := s.Publish(providerprotocol.Event{Type: "message.updated", Properties: json.RawMessage(`{"info":{"sessionID":"s"}}`)}, nil); err != nil {
		t.Fatal(err)
	}
	data := json.RawMessage(`{"sessionID":"s","delta":"` + strings.Repeat("a", providerprotocol.MaxEventBytes) + `"}`)
	if err := s.Publish(providerprotocol.Event{Type: "message.part.delta", Properties: data}, nil); err == nil {
		t.Fatal("oversized event")
	}
	s.seq = ^uint64(0)
	if err := s.Publish(providerprotocol.Event{Properties: json.RawMessage(`{"sessionID":"s"}`)}, nil); err == nil {
		t.Fatal("sequence wrap")
	}
	s.seq = 1
	if err := os.RemoveAll(s.dir); err != nil {
		t.Fatal(err)
	}
	applied := false
	if err := s.Publish(providerprotocol.Event{Properties: json.RawMessage(`{"sessionID":"s"}`)}, func() { applied = true }); err == nil || applied || s.seq != 1 {
		t.Fatal("disk failure exposed event", err)
	}
	if _, err := New("", Backend{}); err == nil {
		t.Fatal("empty spool")
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(path, "child"), Backend{}); err == nil {
		t.Fatal("invalid spool")
	}
}

func TestServePrivateSocketAndCancellation(t *testing.T) {
	s := supervisor(t)
	dir, err := os.MkdirTemp("", "sock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, socket) }()
	deadline := time.Now().Add(time.Second)
	for {
		info, err := os.Stat(socket)
		if err == nil {
			if info.Mode().Perm() == 0600 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("socket not ready")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.Serve(t.Context(), socket); err == nil {
		t.Fatal("live socket overwritten")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := s.Serve(t.Context(), filepath.Join(dir, "missing", "s")); err == nil {
		t.Fatal("invalid socket accepted")
	}
}

func TestSpoolCopiesPayloadAndPreservesOtherSession(t *testing.T) {
	s := supervisor(t)
	props := json.RawMessage(`{"sessionID":"s"}`)
	if err := s.Publish(providerprotocol.Event{Type: "session.idle", Properties: props}, nil); err != nil {
		t.Fatal(err)
	}
	copy(props, bytes.Repeat([]byte("x"), len(props)))
	publish(t, s, "session.status", `{"sessionID":"other","status":{"type":"busy"}}`)
	if w := request(s, "POST", "/sessions/s/ack", `{"sequence":1}`); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if len(s.sessions["s"].records) != 0 || len(s.sessions["other"].records) != 1 {
		t.Fatal(s.sessions)
	}
}

// A prompt that outlives its turn's acknowledged idle edge keeps its ask
// once resolved, so a consumer replaying the resolution (an ocman restart)
// has the prompt's metadata; a later acknowledged turn prunes both. A
// headless reconnect re-delivers the pending prompt without reopening the
// finished turn, so the resolution leaves the supervisor drainable.
func TestLateResolutionKeepsAskAndDoesNotReopenTurn(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		t.Run(fmt.Sprint("reconnect=", reconnect), func(t *testing.T) {
			s := supervisor(t)
			publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
			publish(t, s, "permission.asked", `{"sessionID":"s","id":"p","patterns":["make"]}`)
			publish(t, s, "session.idle", `{"sessionID":"s"}`)
			if w := request(s, "POST", "/sessions/s/ack", `{"sequence":3}`); w.Code != 204 {
				t.Fatal(w.Code)
			}
			if reconnect {
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events?consumer=headless&after=3", nil).WithContext(ctx))
				cancel()
				if !strings.Contains(w.Body.String(), "permission.asked") {
					t.Fatal("pending prompt not re-delivered:", w.Body.String())
				}
			}
			publish(t, s, "permission.replied", `{"sessionID":"s","requestID":"p","reply":"once"}`)
			if s.Active("s") {
				t.Fatal("the resolved prompt left the finished turn open")
			}
			if w := request(s, "POST", "/drain", ""); !strings.Contains(w.Body.String(), `"idle":true`) {
				t.Fatal(w.Body.String())
			}
			s.mu.Lock()
			err := s.prune()
			s.mu.Unlock()
			data, _ := os.ReadFile(s.path("s"))
			if err != nil || !strings.Contains(string(data), `"patterns":["make"]`) || !strings.Contains(string(data), "permission.replied") {
				t.Fatalf("late resolution lost its ask: %v %s", err, data)
			}
			s.draining = false
			publish(t, s, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
			publish(t, s, "session.idle", `{"sessionID":"s"}`)
			seq := s.seq
			if w := request(s, "POST", "/sessions/s/ack", fmt.Sprintf(`{"sequence":%d}`, seq)); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if data, _ := os.ReadFile(s.path("s")); len(data) != 0 {
				t.Fatalf("a later acknowledged turn kept: %s", data)
			}
		})
	}
}
