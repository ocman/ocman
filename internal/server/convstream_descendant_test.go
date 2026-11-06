package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/convevents/v1map"
	"github.com/ocman/ocman/internal/convstream"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type descendantPlatform struct {
	fakePlatform
	rows         map[string]db.Session
	summaryCalls int
}

func (p *descendantPlatform) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	p.summaryCalls++
	return p.SessionMetadata(context.Background(), id)
}

func (p *descendantPlatform) SessionMetadata(_ context.Context, id string) (*db.Session, error) {
	row, ok := p.rows[id]
	if !ok {
		return nil, platforms.ErrNotFound
	}
	return &row, nil
}

func TestConversationDescendantDoesNotReadAccounting(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := &descendantPlatform{fakePlatform: fakePlatform{PlatformID: "r-box:opencode"}, rows: map[string]db.Session{
		"child": {ID: "child", ParentID: "middle"}, "middle": {ID: "middle", ParentID: "root"},
	}}
	reg.Register(p)
	if !srv.isDescendantSession(t.Context(), convstream.Key{Platform: "r-box:opencode", SessionID: "root"}, "child") {
		t.Fatal("missing descendant")
	}
	if p.summaryCalls != 0 {
		t.Fatalf("ancestor checks performed %d accounting reads", p.summaryCalls)
	}
}

func TestChildStatusMapperDoesNotReadAccounting(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := &descendantPlatform{fakePlatform: fakePlatform{PlatformID: "opencode"}, rows: map[string]db.Session{
		"child": {ID: "child", ParentID: "middle"}, "middle": {ID: "middle", ParentID: "root"},
	}}
	reg.Register(p)
	mapper := v1map.New(v1map.Options{SessionID: "root", Descendant: func(id string) bool {
		return srv.isDescendantSession(t.Context(), convstream.Key{Platform: "opencode", SessionID: "root"}, id)
	}})
	// Match the hot profiling path: status -> isChild -> owner ancestor walk,
	// repeatedly for a child that was not announced by session.created.
	for range 20 {
		for _, status := range []string{"busy", "idle"} {
			result := mapper.Map(v1map.Frame{Data: []byte(fmt.Sprintf(`{"type":"session.status","properties":{"sessionID":"child","status":{"type":%q}}}`, status))})
			if len(result.Events) != 1 {
				t.Fatalf("lost child status: %+v", result)
			}
			if event, ok := result.Events[0].(convevents.ChildStatus); !ok || event.ChildID != "child" {
				t.Fatalf("wrong child event: %+v", result.Events[0])
			}
		}
	}
	if p.summaryCalls != 0 {
		t.Fatalf("status frames performed %d accounting reads", p.summaryCalls)
	}
}

func TestConversationDescendantRejectsCyclesMismatchedIDsAndDeepChains(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := &descendantPlatform{fakePlatform: fakePlatform{PlatformID: "opencode"}, rows: map[string]db.Session{
		"cycle-a": {ID: "cycle-a", ParentID: "cycle-b"}, "cycle-b": {ID: "cycle-b", ParentID: "cycle-a"},
		"mismatch": {ID: "different", ParentID: "root"},
	}}
	for i := range 257 {
		p.rows[fmt.Sprint(i)] = db.Session{ID: fmt.Sprint(i), ParentID: fmt.Sprint(i + 1)}
	}
	p.rows["257"] = db.Session{ID: "257", ParentID: "root"}
	reg.Register(p)
	for _, child := range []string{"cycle-a", "mismatch", "0", "missing"} {
		if srv.isDescendantSession(t.Context(), convstream.Key{Platform: "opencode", SessionID: "root"}, child) {
			t.Fatalf("accepted %s", child)
		}
	}
	if p.summaryCalls != 0 {
		t.Fatalf("accounting reads: %d", p.summaryCalls)
	}
}

func TestConversationDescendantUsesRemoteOwner(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&descendantPlatform{fakePlatform: fakePlatform{PlatformID: "r-box:opencode"}, rows: map[string]db.Session{
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
	p := &descendantPlatform{fakePlatform: fakePlatform{PlatformID: "r-box:opencode", SessionList: []db.Session{{ID: "root"}},
		ListPermissionsFn: func(string) ([]platforms.LivePrompt, error) {
			return []platforms.LivePrompt{{"id": "permission", "sessionID": "child", "permission": "bash"}}, nil
		},
		ProxyEventsFn: func(ctx context.Context, _ string, w io.Writer, _ func()) error {
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
