package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestAdapterForkSession_EndToEnd drives ForkSession through
// resolvePort against the OpenCode fake, asserting the /fork call and
// the returned new session ID.
func TestAdapterForkSession_EndToEnd(t *testing.T) {
	const sid = "ses_fork_src"
	const dir = "/tmp/proj-fork"

	var (
		gotPath   string
		gotMethod string
		gotBody   map[string]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"id":"ses_fork_new"}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	a := New(newTestDBWithSession(t, sid, dir), nil)

	resp, err := a.ForkSession(context.Background(), platforms.ForkSessionRequest{
		SessionID: sid,
		MessageID: "msg_abc",
	})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if resp.ID != "ses_fork_new" {
		t.Errorf("resp.ID = %q, want ses_fork_new", resp.ID)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/session/"+sid+"/fork" {
		t.Errorf("path = %q, want /session/%s/fork", gotPath, sid)
	}
	if gotBody["messageID"] != "msg_abc" {
		t.Errorf("body.messageID = %q, want msg_abc", gotBody["messageID"])
	}
}

// TestAdapterForkSession_NoMessageIDOmitsField verifies an empty
// MessageID is not sent (fork-from-HEAD).
func TestAdapterForkSession_NoMessageIDOmitsField(t *testing.T) {
	const sid = "ses_fork_head"
	const dir = "/tmp/proj-fork-head"

	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":"ses_head_new"}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	a := New(newTestDBWithSession(t, sid, dir), nil)

	if _, err := a.ForkSession(context.Background(), platforms.ForkSessionRequest{SessionID: sid}); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if strings.Contains(string(raw), "messageID") {
		t.Errorf("body = %s, should omit messageID when empty", raw)
	}
}

// TestAdapterForkSession_BadResponse errors when OpenCode returns no ID.
func TestAdapterForkSession_BadResponse(t *testing.T) {
	const sid = "ses_fork_bad"
	const dir = "/tmp/proj-fork-bad"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	a := New(newTestDBWithSession(t, sid, dir), nil)

	if _, err := a.ForkSession(context.Background(), platforms.ForkSessionRequest{SessionID: sid}); err == nil {
		t.Error("ForkSession: expected error for response with no id")
	}
}

// TestAdapterMoveSession_EndToEnd drives MoveSession through
// resolvePort, asserting the control-plane call and request body.
func TestAdapterMoveSession_EndToEnd(t *testing.T) {
	const sid = "ses_move_src"
	const dir = "/tmp/proj-move"

	var (
		gotPath   string
		gotMethod string
		gotBody   struct {
			SessionID   string            `json:"sessionID"`
			Destination map[string]string `json:"destination"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	a := New(newTestDBWithSession(t, sid, dir), nil)

	if err := a.MoveSession(context.Background(), platforms.MoveSessionRequest{
		SessionID: sid,
		Directory: "/tmp/dest",
	}); err != nil {
		t.Fatalf("MoveSession: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/experimental/control-plane/move-session" {
		t.Errorf("path = %q, want control-plane move-session", gotPath)
	}
	if gotBody.SessionID != sid {
		t.Errorf("body.sessionID = %q, want %s", gotBody.SessionID, sid)
	}
	if gotBody.Destination["directory"] != "/tmp/dest" {
		t.Errorf("body.destination.directory = %q, want /tmp/dest", gotBody.Destination["directory"])
	}
}

// TestAdapterMoveSession_UpstreamRejection maps a 4xx to
// ErrUpstreamRejected like the other mutations.
func TestAdapterMoveSession_UpstreamRejection(t *testing.T) {
	const sid = "ses_move_rej"
	const dir = "/tmp/proj-move-rej"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"BadRequest","data":{"message":"no such dir"}}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	withTestPort(t, dir, port)
	a := New(newTestDBWithSession(t, sid, dir), nil)

	err := a.MoveSession(context.Background(), platforms.MoveSessionRequest{
		SessionID: sid,
		Directory: "/nope",
	})
	if !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Errorf("err = %v, want ErrUpstreamRejected", err)
	}
}

// TestAdapterForkMove_UnknownSession maps an unknown session to a
// port-resolution error on both methods.
func TestAdapterForkMove_UnknownSession(t *testing.T) {
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{} })
	restoreServers := setDiscoverServersImplForTests(func() []openCodeServer { return nil })
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	t.Cleanup(func() {
		restore()
		restoreServers()
		resetPortCacheForTests()
		resetSessionPortAffinityForTests()
	})

	a := &Adapter{}
	if _, err := a.ForkSession(context.Background(), platforms.ForkSessionRequest{SessionID: "nope"}); err == nil {
		t.Error("ForkSession: expected error for unknown session")
	}
	if err := a.MoveSession(context.Background(), platforms.MoveSessionRequest{SessionID: "nope", Directory: "/x"}); err == nil {
		t.Error("MoveSession: expected error for unknown session")
	}
}
