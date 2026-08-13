package server

import (
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
	ts := newTestRelay(t)

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

	chunks := readRelayChunks(t, ts.URL, link.RelayID, key)
	if len(chunks) < 2 {
		t.Fatalf("expected the snapshot to span multiple chunks, got %d", len(chunks))
	}
	if chunks[0].Session == nil {
		t.Error("no chunk carried the session")
	}

	messages, parts := mergeChunks(chunks)
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
	ts := newTestRelay(t)

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

	relayID, key := createShareForTest(t, srv, "ses_secret")

	// Assert on the decrypted payload: the ciphertext obviously never
	// contains the secret, so checking stored bytes would prove nothing.
	chunks := readRelayChunks(t, ts.URL, relayID, key)
	var published string
	for _, chunk := range chunks {
		for _, part := range chunk.Parts {
			published += string(part.Data)
		}
	}

	if strings.Contains(published, "SUPER_SECRET_TOKEN") {
		t.Error("the truncated tail was published to the relay")
	}
	if !strings.Contains(published, "truncated for sharing") {
		t.Error("published output carries no truncation marker")
	}
	if !strings.Contains(published, strings.Repeat("B", 1024)) {
		t.Error("published output lost the retained head of the tool result")
	}
}
