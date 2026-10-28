package codextransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestProtocolSkewDrainKeepsPromptsAndAbort(t *testing.T) {
	r := newRig(t)
	p := r.platform(t.Context())
	socket := filepath.Join(r.data, "old.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	calls := make(chan string, 16)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/hello":
			_ = json.NewEncoder(w).Encode(providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion + 7, PlatformID: "codex", CLIVersion: "test"})
		case "/events":
			if req.URL.Query().Get("consumer") != "headless" {
				t.Errorf("versioned stream during drain: %s", req.URL)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for i, event := range []string{
				`{"type":"permission.asked","properties":{"id":"p","sessionID":"thr","permission":"bash","patterns":["ls"]}}`,
				`{"type":"question.asked","properties":{"id":"q","sessionID":"thr","questions":[]}}`,
			} {
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i+1, event)
			}
			w.(http.Flusher).Flush()
			<-req.Context().Done()
		case "/permissions/p/reply", "/questions/q/reply", "/questions/q/reject", "/sessions/thr/abort":
			calls <- req.URL.Path
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("versioned route during drain: %s", req.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	events := make(chan string, 8)
	p.SetEventHandler(func(e providerprotocol.Event) { events <- e.Type })
	p.mgr.OnDrain(p.root, "unix://"+socket, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = p.WatchEvents(ctx) }()
	for range 2 {
		select {
		case <-events:
		case <-ctx.Done():
			t.Fatal("draining prompt events did not reach the handler")
		}
	}
	if prompts, err := p.ListPermissions(ctx, "thr"); err != nil || len(prompts) != 1 {
		t.Fatalf("permissions = %v %v", prompts, err)
	}
	if prompts, err := p.ListQuestions(ctx, "thr"); err != nil || len(prompts) != 1 {
		t.Fatalf("questions = %v %v", prompts, err)
	}
	if err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: "thr"}); !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("drain admitted a new turn: %v", err)
	}
	for name, err := range map[string]error{
		"permission": p.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: "thr", PermissionID: "p", Reply: "once"}),
		"question":   p.RespondQuestion(ctx, platforms.RespondQuestionRequest{SessionID: "thr", RequestID: "q"}),
		"reject":     p.RejectQuestion(ctx, platforms.RejectQuestionRequest{SessionID: "thr", RequestID: "q"}),
		"abort":      p.Abort(ctx, platforms.AbortRequest{SessionID: "thr"}),
	} {
		if err != nil {
			t.Fatalf("%s during drain: %v", name, err)
		}
	}
	for range 4 {
		select {
		case path := <-calls:
			if !strings.Contains(path, "thr") && !strings.Contains(path, "/q/") && !strings.Contains(path, "/p/") {
				t.Fatalf("unexpected route %s", path)
			}
		case <-ctx.Done():
			t.Fatal("prompt reply or abort never reached draining supervisor")
		}
	}
	// A listener update after Close must not restart recovery.
	p.Close()
	p.SetMCPAddr("127.0.0.1:9999")
}
