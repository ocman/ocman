package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/convstream"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type descendantPlatform struct {
	fakePlatform
	rows map[string]db.Session
}

func (p *descendantPlatform) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	row, ok := p.rows[id]
	if !ok {
		return nil, platforms.ErrNotFound
	}
	return &row, nil
}

func TestConversationDescendantUsesRemoteOwner(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&descendantPlatform{fakePlatform: fakePlatform{id: "r-box:opencode"}, rows: map[string]db.Session{
		"child": {ID: "child", ParentID: "middle"}, "middle": {ID: "middle", ParentID: "root"},
	}})
	if !srv.isDescendantSession(t.Context(), convstream.Key{Platform: "r-box:opencode", SessionID: "root"}, "child") {
		t.Fatal("remote descendant absent from local DB was rejected")
	}
	for _, child := range []string{"unknown", "middle", "child"} {
		if srv.isDescendantSession(t.Context(), convstream.Key{Platform: "r-box:opencode", SessionID: "other"}, child) {
			t.Fatalf("unrelated %s accepted", child)
		}
	}
}

func TestRemoteDescendantPromptsReachParentStream(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	live := make(chan string, 1)
	p := &descendantPlatform{fakePlatform: fakePlatform{id: "r-box:opencode", sessions: []db.Session{{ID: "root"}},
		listPermissionsFn: func(string) ([]platforms.LivePrompt, error) {
			return []platforms.LivePrompt{{"id": "permission", "sessionID": "child", "permission": "bash"}}, nil
		},
		proxyEventsFn: func(ctx context.Context, _ string, w io.Writer, _ func()) error {
			_, _ = io.WriteString(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case frame := <-live:
					_, _ = io.WriteString(w, frame)
				}
			}
		}}, rows: map[string]db.Session{"child": {ID: "child", ParentID: "root"}}}
	reg.Register(p)
	t.Cleanup(srv.convStreamModule().Close)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(func() { hs.CloseClientConnections(); hs.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/api/session/root/stream?platform=r-box:opencode", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	hello := readOCE(t, reader).Event.(convevents.StreamHello)
	if hello.Overlay == nil || len(hello.Overlay.Prompts) != 1 || hello.Overlay.Prompts[0].ID != "permission" {
		t.Fatalf("remote seed filtered: %+v", hello.Overlay)
	}
	live <- "data: {\"type\":\"question.asked\",\"properties\":{\"id\":\"question\",\"sessionID\":\"child\",\"questions\":[{\"question\":\"Continue?\",\"header\":\"Next\",\"options\":[]}]}}\n\n"
	for {
		event := readOCE(t, reader).Event
		if asked, ok := event.(convevents.PromptAsked); ok {
			if asked.Prompt.ID != "question" || asked.Prompt.SessionID != "child" {
				t.Fatalf("wrong prompt: %+v", asked)
			}
			break
		}
	}
}
