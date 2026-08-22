package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// newTestRelay starts an in-process relay backed by a temp disk store.
func newTestRelay(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	srv, err := relay.New(relay.Config{Store: store})
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// readRelayChunks fetches and decrypts every chunk of a share, the way
// the browser viewer does.
func readRelayChunks(t *testing.T, baseURL, id string, key share.Key) []relayChunk {
	t.Helper()
	resp, err := http.Get(baseURL + "/s/" + id + "?from=0")
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

	out := make([]relayChunk, 0, len(body.Chunks))
	for _, chunk := range body.Chunks {
		ciphertext, err := base64.RawStdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatalf("decode chunk %d: %v", chunk.Seq, err)
		}
		plain, err := share.Open(key, id, chunk.Seq, ciphertext)
		if err != nil {
			t.Fatalf("open chunk %d: %v", chunk.Seq, err)
		}
		var decoded relayChunk
		if err := json.Unmarshal(plain, &decoded); err != nil {
			t.Fatalf("unmarshal chunk %d: %v", chunk.Seq, err)
		}
		out = append(out, decoded)
	}
	return out
}

// mergeChunks collapses a chunk log into the id-keyed row sets the
// viewer builds, so assertions read as "what a reader ends up seeing".
func mergeChunks(chunks []relayChunk) (messages, parts map[string]bool) {
	messages, parts = map[string]bool{}, map[string]bool{}
	for _, chunk := range chunks {
		for _, m := range chunk.Messages {
			messages[m.ID] = true
		}
		for _, p := range chunk.Parts {
			parts[p.ID] = true
		}
	}
	return messages, parts
}

// createShareForTest publishes a share for the session and returns its
// relay id and key.
func createShareForTest(t *testing.T, srv *Server, sessionID string) (string, share.Key) {
	t.Helper()
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/"+sessionID+"/share", nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create share status = %d, want 201; body=%s", rr.Code, rr.Body)
	}
	var view shareLinkView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal share view: %v", err)
	}
	link, ok, err := srv.stateDB.GetActiveShareLink(t.Context(), view.Token)
	if err != nil || !ok {
		t.Fatalf("GetActiveShareLink: ok=%v err=%v", ok, err)
	}
	key, err := share.ParseKey(link.RelayKey)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return link.RelayID, key
}

// shareLinkFor returns the single active share link of a session.
func shareLinkFor(t *testing.T, srv *Server, sessionID string) int64 {
	t.Helper()
	links, err := srv.stateDB.ListActiveShareLinks(t.Context(), "fake", sessionID)
	if err != nil {
		t.Fatalf("ListActiveShareLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("got %d active share links, want 1", len(links))
	}
	return links[0].RelayLastSeq
}

// TestPublishCompletedTurnAppendsNewTurn covers the incremental path: a
// live conversation must keep updating an already-published share.
func TestPublishCompletedTurnAppendsNewTurn(t *testing.T) {
	ts := newTestRelay(t)

	detail := &platforms.SessionDetail{
		Session: &db.Session{ID: "ses_live", Title: "Live", Platform: "fake"},
		Messages: []db.Message{
			{ID: "m1", SessionID: "ses_live", TimeCreated: 1, Data: json.RawMessage(`{"role":"user"}`)},
			{ID: "m2", SessionID: "ses_live", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)},
		},
		Parts: []db.Part{
			{ID: "p1", MessageID: "m1", SessionID: "ses_live", Data: json.RawMessage(`{"type":"text","text":"first question"}`)},
			{ID: "p2", MessageID: "m2", SessionID: "ses_live", Data: json.RawMessage(`{"type":"text","text":"first answer"}`)},
		},
	}

	srv, reg := newSessionsTestServer(t)
	fake := &fakePlatform{
		id:              "fake",
		sessions:        []db.Session{{ID: "ses_live", Platform: "fake"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, nil },
	}
	reg.Register(fake)
	srv.WithRelay(ts.URL, "flag")

	relayID, key := createShareForTest(t, srv, "ses_live")
	if got := shareLinkFor(t, srv, "ses_live"); got != 0 {
		t.Fatalf("snapshot last seq = %d, want 0", got)
	}

	// A second turn completes, and the session gets retitled.
	detail.Session.Title = "Renamed mid-conversation"
	detail.Messages = append(detail.Messages,
		db.Message{ID: "m3", SessionID: "ses_live", TimeCreated: 3, Data: json.RawMessage(`{"role":"user"}`)},
		db.Message{ID: "m4", SessionID: "ses_live", TimeCreated: 4, Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)},
	)
	detail.Parts = append(detail.Parts,
		db.Part{ID: "p3", MessageID: "m3", SessionID: "ses_live", Data: json.RawMessage(`{"type":"text","text":"second question"}`)},
		db.Part{ID: "p4", MessageID: "m4", SessionID: "ses_live", Data: json.RawMessage(`{"type":"text","text":"second answer"}`)},
	)

	if err := srv.publishCompletedTurn(context.Background(), fake, "ses_live"); err != nil {
		t.Fatalf("publishCompletedTurn: %v", err)
	}

	chunks := readRelayChunks(t, ts.URL, relayID, key)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want the snapshot plus one turn", len(chunks))
	}
	if chunks[0].Session == nil {
		t.Error("snapshot chunk is missing the session")
	}
	// A delta repeats the session on purpose: the viewer takes the
	// latest one it sees, which is how a rename reaches an already
	// published share.
	if chunks[1].Session == nil || chunks[1].Session.Title != "Renamed mid-conversation" {
		t.Errorf("delta did not carry the updated session: %+v", chunks[1].Session)
	}

	messages, parts := mergeChunks(chunks)
	for _, id := range []string{"m1", "m2", "m3", "m4"} {
		if !messages[id] {
			t.Errorf("merged view is missing message %s", id)
		}
	}
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		if !parts[id] {
			t.Errorf("merged view is missing part %s", id)
		}
	}
	// The delta must be exactly the last turn, not the whole history.
	if len(chunks[1].Messages) != 2 {
		t.Errorf("delta carried %d messages, want only the last turn's 2", len(chunks[1].Messages))
	}
	if got := shareLinkFor(t, srv, "ses_live"); got != 1 {
		t.Errorf("persisted last seq = %d, want 1", got)
	}
}

// TestPublishCompletedTurnSplitsOversizedTurn guards the case a single
// turn exceeds the chunk cap on its own — the split must apply to the
// incremental path too, not just the initial snapshot.
func TestPublishCompletedTurnSplitsOversizedTurn(t *testing.T) {
	ts := newTestRelay(t)

	detail := &platforms.SessionDetail{
		Session: &db.Session{ID: "ses_big_turn", Platform: "fake"},
		Messages: []db.Message{
			{ID: "m1", SessionID: "ses_big_turn", TimeCreated: 1, Data: json.RawMessage(`{"role":"user"}`)},
		},
		Parts: []db.Part{
			{ID: "p1", MessageID: "m1", SessionID: "ses_big_turn", Data: json.RawMessage(`{"type":"text","text":"go"}`)},
		},
	}

	srv, reg := newSessionsTestServer(t)
	fake := &fakePlatform{
		id:              "fake",
		sessions:        []db.Session{{ID: "ses_big_turn", Platform: "fake"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, nil },
	}
	reg.Register(fake)
	srv.WithRelay(ts.URL, "flag")

	relayID, key := createShareForTest(t, srv, "ses_big_turn")

	// One assistant reply with hundreds of large tool calls: ~2.4 MiB
	// even after truncation, so it cannot fit a single chunk.
	detail.Messages = append(detail.Messages,
		db.Message{ID: "m2", SessionID: "ses_big_turn", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)},
	)
	for i := range 300 {
		detail.Parts = append(detail.Parts, db.Part{
			ID: fmt.Sprintf("big%d", i), MessageID: "m2", SessionID: "ses_big_turn",
			Data: json.RawMessage(fmt.Sprintf(
				`{"type":"tool","tool":"bash","state":{"status":"completed","output":%q}}`,
				strings.Repeat("T", 20<<10))),
		})
	}

	if err := srv.publishCompletedTurn(context.Background(), fake, "ses_big_turn"); err != nil {
		t.Fatalf("publishCompletedTurn: %v", err)
	}

	chunks := readRelayChunks(t, ts.URL, relayID, key)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks; the oversized turn should have split across several", len(chunks))
	}

	_, parts := mergeChunks(chunks)
	for i := range 300 {
		if !parts[fmt.Sprintf("big%d", i)] {
			t.Fatalf("merged view is missing part big%d", i)
		}
	}
	if got, want := shareLinkFor(t, srv, "ses_big_turn"), int64(len(chunks)-1); got != want {
		t.Errorf("persisted last seq = %d, want %d", got, want)
	}
}

// TestPublishCompletedTurnIgnoresSharesWithoutRelay makes sure a link
// that never reached a relay is skipped rather than erroring the
// publish for every other share.
func TestPublishCompletedTurnIgnoresSharesWithoutRelay(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	detail := shareTestDetail("ses_norelay")
	fake := &fakePlatform{
		id:              "fake",
		sessions:        []db.Session{{ID: "ses_norelay", Platform: "fake"}},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, nil },
	}
	reg.Register(fake)

	if _, err := srv.stateDB.CreateShareLink(t.Context(), "fake", "ses_norelay", 0); err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	if err := srv.publishCompletedTurn(context.Background(), fake, "ses_norelay"); err != nil {
		t.Fatalf("publishCompletedTurn: %v", err)
	}
	if got := shareLinkFor(t, srv, "ses_norelay"); got != -1 {
		t.Errorf("last seq = %d, want it untouched at -1", got)
	}
}

// bigChunk is a chunk large enough to trip the byte-size guards.
func bigChunk() relayChunk {
	return relayChunk{
		Parts: []db.Part{{
			ID:   "p1",
			Data: json.RawMessage(fmt.Sprintf(`{"type":"text","text":%q}`, strings.Repeat("Z", 4096))),
		}},
	}
}

// The three guards below must fail *before* any upload: an unreachable
// relay URL is used deliberately, so a transport error would prove the
// check ran too late.
func TestUploadChunksEnforcesRelayLimitsBeforeUploading(t *testing.T) {
	key, err := share.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	client := share.RelayClient{BaseURL: "http://127.0.0.1:1"}

	tests := []struct {
		name       string
		allocation share.RelayAllocation
		chunks     []relayChunk
		want       string
	}{
		{
			name:       "chunk over the per-chunk byte cap",
			allocation: share.RelayAllocation{ID: "share", MaxChunkBytes: 64},
			chunks:     []relayChunk{bigChunk()},
			want:       "chunk is too large after encryption",
		},
		{
			name:       "chunks over the per-share byte cap",
			allocation: share.RelayAllocation{ID: "share", MaxShareBytes: 64},
			chunks:     []relayChunk{bigChunk()},
			want:       "share is too large",
		},
		{
			name:       "appending past the chunk-count cap",
			allocation: share.RelayAllocation{ID: "share", MaxChunks: 2},
			chunks:     []relayChunk{{}},
			want:       "share has too many chunks",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			firstSeq := uint64(0)
			if tc.want == "share has too many chunks" {
				firstSeq = 2 // already at the cap
			}
			_, err := (&Server{}).uploadChunks(context.Background(), client, tc.allocation, key, firstSeq, tc.chunks)

			var relayErr *share.RelayError
			if !errors.As(err, &relayErr) {
				t.Fatalf("error = %v, want a *share.RelayError", err)
			}
			if relayErr.Status != http.StatusRequestEntityTooLarge || relayErr.Message != tc.want {
				t.Fatalf("error = %v, want 413 %q", err, tc.want)
			}
		})
	}
}

func TestSplitShareSnapshotReportsUnencodableRows(t *testing.T) {
	broken := json.RawMessage(`{"not closed"`)

	if _, err := splitShareSnapshot(nil, []db.Message{{ID: "m1", Data: broken}}, nil, 16<<10); err == nil {
		t.Error("expected an error for a message that cannot be encoded")
	}
	if _, err := splitShareSnapshot(nil, nil, []db.Part{{ID: "p1", Data: broken}}, 16<<10); err == nil {
		t.Error("expected an error for a part that cannot be encoded")
	}
}
