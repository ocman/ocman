package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// The final reply streams, the source restarts and history has not stored it
// yet: the conversation read still contains it, with the cursor it covers, and
// the stream announces when history catches up.
func TestConversationReadComposesStreamedContentHistoryHasNotStored(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	srv.historyCommits = newHistoryCommits()
	live := make(chan string, 8)
	reg.Register(&fakePlatform{id: "fake", sessions: []db.Session{{ID: "s1", Platform: "fake"}},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "fake"},
				Messages: []db.Message{{ID: "prompt", SessionID: id, TimeCreated: 1, Data: json.RawMessage(`{"role":"user"}`)}}}, nil
		},
		proxyEventsFn: func(ctx context.Context, _ string, w io.Writer, _ func()) error {
			_, _ = io.WriteString(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case f := <-live:
					_, _ = io.WriteString(w, f)
				}
			}
		}})
	t.Cleanup(srv.convStreamModule().Close)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(mux)
	t.Cleanup(func() { hs.CloseClientConnections(); hs.Close() })
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, hs.URL+"/api/session/s1/stream?platform=fake", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	readOCE(t, r)
	live <- `data: {"type":"message.updated","properties":{"info":{"id":"reply","sessionID":"s1","role":"assistant","time":{"created":2}}}}` + "\n\n"
	live <- `data: {"type":"message.part.updated","properties":{"part":{"id":"p1","messageID":"reply","sessionID":"s1","type":"text","text":"Final answer","time":{"start":2,"end":3}}}}` + "\n\n"
	for env := readOCE(t, r); env.Event.EventType() != convevents.TypePartUpserted; env = readOCE(t, r) {
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/s1?platform=fake", nil))
	var body struct {
		Messages []db.Message `json:"messages"`
		Parts    []db.Part    `json:"parts"`
		Stream   struct {
			Cursor             string   `json:"cursor"`
			ComposedMessageIDs []string `json:"composedMessageIds"`
		} `json:"stream"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("read = %d %s %v", rec.Code, rec.Body, err)
	}
	if len(body.Messages) != 2 || body.Messages[1].ID != "reply" || len(body.Parts) != 1 || !bytes.Contains(body.Parts[0].Data, []byte("Final answer")) {
		t.Fatalf("read omits the streamed reply: %s", rec.Body)
	}
	if ids := body.Stream.ComposedMessageIDs; len(ids) != 1 || ids[0] != "reply" {
		t.Fatalf("composed ids = %v", ids)
	}
	if _, seq, ok := convevents.ParseCursor(body.Stream.Cursor); !ok || seq == 0 {
		t.Fatalf("cursor = %q", body.Stream.Cursor)
	}

	srv.historyCommits.observe(statev2.Key{InstanceID: "i", SessionID: "other"}, 3)
	srv.historyCommits.observe(statev2.Key{InstanceID: "i", SessionID: "s1"}, 4)
	env := readOCE(t, r)
	if c, ok := env.Event.(convevents.HistoryCommitted); !ok || c.Revision != 4 {
		t.Fatalf("event = %+v", env.Event)
	}
}

func TestHistoryCommitsCoalesceAndStopCleanly(t *testing.T) {
	h := newHistoryCommits()
	var out syncBuffer
	stop := h.forward(t.Context(), "s1", &out, nil)
	h.observe(statev2.Key{SessionID: "s1"}, 1)
	waitFor(t, func() bool { return strings.Count(out.String(), "ocman.history.committed\ndata") == 1 })
	// Commits within the interval coalesce to the newest revision.
	for revision := int64(2); revision <= 5; revision++ {
		h.observe(statev2.Key{SessionID: "s1"}, revision)
	}
	waitFor(t, func() bool { return strings.Contains(out.String(), `"revision":5`) })
	if strings.Contains(out.String(), `"revision":3`) {
		t.Fatalf("commits were not coalesced: %s", out.String())
	}
	stop()
	h.observe(statev2.Key{SessionID: "s1"}, 6)
	if len(h.listeners) != 0 || strings.Contains(out.String(), `"revision":6`) {
		t.Fatal("stopped forwarder still listening")
	}
	var none *historyCommits
	none.forward(t.Context(), "s1", &out, nil)()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
