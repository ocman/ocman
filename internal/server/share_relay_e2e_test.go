package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/share"
)

// bigConversation builds a session whose raw payload is far larger than
// the relay's 1 MiB per-chunk limit — the shape that produced "chunk too
// large" and made sharing impossible for any real coding session.
func bigConversation(id string, turns int, outputBytes int) *platforms.SessionDetail {
	detail := &platforms.SessionDetail{
		Session: &db.Session{ID: id, Title: "Big Convo", Platform: "fake"},
	}
	for i := range turns {
		mid := fmt.Sprintf("m%d", i)
		detail.Messages = append(detail.Messages,
			db.Message{ID: mid, SessionID: id, TimeCreated: int64(i), Data: json.RawMessage(`{"role":"user"}`)},
		)
		detail.Parts = append(detail.Parts, db.Part{
			ID: fmt.Sprintf("p%d", i), MessageID: mid, SessionID: id, TimeCreated: int64(i),
			Data: json.RawMessage(fmt.Sprintf(
				`{"type":"tool","tool":"bash","state":{"status":"completed","output":%q}}`,
				strings.Repeat("L", outputBytes))),
		})
	}
	return detail
}

// TestCreateShareHandlesConversationLargerThanChunkLimit is the
// regression test for the reported failure: creating a share link for a
// large conversation returned "chunk too large".
func TestCreateShareHandlesConversationLargerThanChunkLimit(t *testing.T) {
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	relaySrv, err := relay.New(relay.Config{Store: store})
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}
	ts := httptest.NewServer(relaySrv)
	defer ts.Close()

	// Sized so the conversation is over the chunk limit both before
	// truncation (~6 MiB raw) and after it (300 x 8 KiB ~= 2.4 MiB), so
	// this exercises truncation and splitting rather than either alone.
	detail := bigConversation("ses_big", 300, 20<<10)
	raw, err := json.Marshal(detail.Parts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) <= relay.DefaultMaxChunkBytes {
		t.Fatalf("fixture is only %d bytes; it must exceed the %d chunk limit to be meaningful",
			len(raw), relay.DefaultMaxChunkBytes)
	}
	truncated, err := json.Marshal(truncateShareParts(detail.Parts, sharePartTextLimit))
	if err != nil {
		t.Fatalf("marshal truncated: %v", err)
	}
	if len(truncated) <= relay.DefaultMaxChunkBytes {
		t.Fatalf("fixture is %d bytes after truncation; it must still exceed the %d chunk limit "+
			"so splitting is exercised too", len(truncated), relay.DefaultMaxChunkBytes)
	}

	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:              "fake",
		sessions:        []db.Session{{ID: "ses_big", Platform: "fake"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, nil },
	})
	srv.WithRelay(ts.URL, "flag")

	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/ses_big/share", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body)
	}

	var view shareLinkView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(view.URL, ts.URL+"/v/") || !strings.Contains(view.URL, "#k=") {
		t.Fatalf("unexpected share URL %q", view.URL)
	}

	// Read it back the way the viewer does and confirm the whole
	// conversation reconstructs from the chunk log.
	link, ok, err := srv.stateDB.GetActiveShareLink(view.Token)
	if err != nil || !ok {
		t.Fatalf("GetActiveShareLink: ok=%v err=%v", ok, err)
	}
	key, err := share.ParseKey(link.RelayKey)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}

	resp, err := http.Get(ts.URL + "/s/" + link.RelayID + "?from=0")
	if err != nil {
		t.Fatalf("read relay: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Chunks []struct {
			Seq  uint64 `json:"seq"`
			Data string `json:"data"`
		} `json:"chunks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode relay read: %v", err)
	}
	if len(body.Chunks) < 2 {
		t.Fatalf("expected the snapshot to span multiple chunks, got %d", len(body.Chunks))
	}

	messages := map[string]bool{}
	parts := map[string]bool{}
	var sessionSeen bool
	for _, chunk := range body.Chunks {
		ciphertext, err := base64.RawStdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatalf("decode chunk %d: %v", chunk.Seq, err)
		}
		plain, err := share.Open(key, link.RelayID, chunk.Seq, ciphertext)
		if err != nil {
			t.Fatalf("open chunk %d: %v", chunk.Seq, err)
		}
		var decoded relayChunk
		if err := json.Unmarshal(plain, &decoded); err != nil {
			t.Fatalf("unmarshal chunk %d: %v", chunk.Seq, err)
		}
		if decoded.Session != nil {
			sessionSeen = true
		}
		for _, m := range decoded.Messages {
			messages[m.ID] = true
		}
		for _, p := range decoded.Parts {
			parts[p.ID] = true
		}
	}

	if !sessionSeen {
		t.Error("no chunk carried the session")
	}
	if len(messages) != len(detail.Messages) {
		t.Errorf("recovered %d messages, want %d", len(messages), len(detail.Messages))
	}
	if len(parts) != len(detail.Parts) {
		t.Errorf("recovered %d parts, want %d", len(parts), len(detail.Parts))
	}
}

// TestCreateShareTruncatesToolOutput proves the published copy carries
// shortened tool output. This bounds the upload and, because tool output
// is where secrets end up, limits what a leaked link discloses.
func TestCreateShareTruncatesToolOutput(t *testing.T) {
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	relaySrv, err := relay.New(relay.Config{Store: store})
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}
	ts := httptest.NewServer(relaySrv)
	defer ts.Close()

	secret := strings.Repeat("A", 4<<10) + "SUPER_SECRET_TOKEN"
	detail := &platforms.SessionDetail{
		Session:  &db.Session{ID: "ses_secret", Platform: "fake"},
		Messages: []db.Message{{ID: "m1", SessionID: "ses_secret", Data: json.RawMessage(`{"role":"user"}`)}},
		Parts: []db.Part{{
			ID: "p1", MessageID: "m1", SessionID: "ses_secret",
			Data: json.RawMessage(fmt.Sprintf(
				`{"type":"tool","tool":"bash","state":{"status":"completed","output":%q}}`,
				strings.Repeat("B", sharePartTextLimit)+secret)),
		}},
	}

	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:              "fake",
		sessions:        []db.Session{{ID: "ses_secret", Platform: "fake"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, nil },
	})
	srv.WithRelay(ts.URL, "flag")

	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/ses_secret/share", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rr.Code, rr.Body)
	}

	// Everything stored on the relay must be free of the tail content.
	objects, err := store.List(t.Context(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, object := range objects {
		blob, err := store.Get(t.Context(), object.Key)
		if err != nil {
			t.Fatalf("Get %s: %v", object.Key, err)
		}
		if strings.Contains(string(blob), "SUPER_SECRET_TOKEN") {
			t.Fatalf("truncated tail reached the relay in %s", object.Key)
		}
	}
}
