package sharing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/relay"
	"github.com/ocman/ocman/internal/share"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// The retained transcript still has an empty streaming answer until ingestion
// acknowledges a refresh, even though the native turn has already gone idle.
type delayedHistoryAdapter struct {
	platformtest.Fake
	detail      *platforms.SessionDetail
	refreshes   int
	refreshErr  error
	refreshedID string
}

func (*delayedHistoryAdapter) MarkSessionDirty(string) {}

func (a *delayedHistoryAdapter) RefreshSession(ctx context.Context, id string) error {
	a.refreshes++
	a.refreshedID = id
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.refreshErr != nil {
		return a.refreshErr
	}
	a.detail.Messages[1].Data = json.RawMessage(`{"role":"assistant","finish":"stop"}`)
	a.detail.Parts[0].Data = json.RawMessage(`{"type":"text","text":"completed final answer"}`)
	return nil
}

func freshnessFixture(t *testing.T) (*Module, *delayedHistoryAdapter, state.ShareLink) {
	t.Helper()
	disk, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := relay.New(relay.Config{Store: disk})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	store := statetest.Open(t)
	link, err := store.CreateShareLink(t.Context(), "fake", "session", 0)
	if err != nil {
		t.Fatal(err)
	}
	a := &delayedHistoryAdapter{detail: &platforms.SessionDetail{
		Session: &db.Session{ID: "session"},
		Messages: []db.Message{
			{ID: "user", Data: json.RawMessage(`{"role":"user"}`)},
			{ID: "answer", Data: json.RawMessage(`{"role":"assistant"}`)},
		},
		Parts: []db.Part{{ID: "text", MessageID: "answer", Data: json.RawMessage(`{"type":"text","text":""}`)}},
	}}
	a.PlatformID = "fake"
	a.SessionFn = func(string) (*platforms.SessionDetail, error) { return a.detail, nil }
	m := NewModule(Deps{Store: store, RelayURL: func() string { return ts.URL }})
	return m, a, link
}

func assertRelayFinalAnswer(t *testing.T, link state.ShareLink, seq uint64) {
	t.Helper()
	resp, err := http.Get(link.RelayURL + "/s/" + link.RelayID + "?from=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Chunks []struct {
			Seq  uint64 `json:"seq"`
			Data string `json:"data"`
		} `json:"chunks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	key, err := share.ParseKey(link.RelayKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range body.Chunks {
		if chunk.Seq != seq {
			continue
		}
		ciphertext, err := base64.RawStdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := share.Open(key, link.RelayID, seq, ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		var decoded relayChunk
		if err := json.Unmarshal(plain, &decoded); err != nil {
			t.Fatal(err)
		}
		var part struct {
			Text string `json:"text"`
		}
		if len(decoded.Parts) != 1 {
			t.Fatalf("relay chunk %d has %d parts, want 1", seq, len(decoded.Parts))
		}
		if err := json.Unmarshal(decoded.Parts[0].Data, &part); err != nil {
			t.Fatal(err)
		}
		var message db.MessageData
		if err := json.Unmarshal(decoded.Messages[len(decoded.Messages)-1].Data, &message); err != nil {
			t.Fatal(err)
		}
		if part.Text != "completed final answer" || message.Finish != "stop" {
			t.Fatalf("relay chunk %d is missing the completed final answer: %s", seq, plain)
		}
		return
	}
	t.Fatalf("relay chunk %d missing", seq)
}

func TestRelayReadsRefreshedHistory(t *testing.T) {
	for _, incremental := range []bool{false, true} {
		name := "snapshot"
		if incremental {
			name = "completed turn"
		}
		t.Run(name, func(t *testing.T) {
			m, a, link := freshnessFixture(t)
			link, err := m.createRelayShare(t.Context(), link, a)
			if err != nil {
				t.Fatal(err)
			}
			seq := uint64(0)
			if incremental {
				// Simulate the next turn's final answer awaiting ingestion.
				a.detail.Messages[1].Data = json.RawMessage(`{"role":"assistant"}`)
				a.detail.Parts[0].Data = json.RawMessage(`{"type":"text","text":""}`)
				if err := m.PublishCompletedTurn(t.Context(), a, "session"); err != nil {
					t.Fatal(err)
				}
				seq = 1
			}
			assertRelayFinalAnswer(t, link, seq)
			if a.refreshes != int(seq)+1 {
				t.Fatalf("refresh count = %d, want %d", a.refreshes, seq+1)
			}
			if a.refreshedID != "session" {
				t.Fatalf("refreshed session %q, want session", a.refreshedID)
			}
		})
	}
}

func TestRelayDoesNotPublishWhenHistoryRefreshFails(t *testing.T) {
	m, a, link := freshnessFixture(t)
	want := errors.New("ingestion unavailable")
	a.refreshErr = want
	if _, err := m.createRelayShare(t.Context(), link, a); !errors.Is(err, want) {
		t.Fatalf("snapshot error = %v, want %v", err, want)
	}
	a.refreshErr = nil
	link, err := m.createRelayShare(t.Context(), link, a)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshErr = want
	if err := m.PublishCompletedTurn(t.Context(), a, "session"); !errors.Is(err, want) {
		t.Fatalf("completed turn error = %v, want %v", err, want)
	}
	stored, ok, err := m.Store.GetActiveShareLink(t.Context(), link.Token)
	if err != nil || !ok || stored.RelayLastSeq != 0 {
		t.Fatalf("failed refresh advanced relay sequence: %+v, %v", stored, err)
	}
}

func TestRelayRefreshHonorsCancellation(t *testing.T) {
	m, a, link := freshnessFixture(t)
	if _, err := m.createRelayShare(t.Context(), link, a); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := refreshedShareDetail(ctx, a, "session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled history refresh = %v", err)
	}
}

func TestUnsharedSessionDoesNotRefreshHistory(t *testing.T) {
	m, a, _ := freshnessFixture(t)
	if err := m.PublishCompletedTurn(t.Context(), a, "unshared"); err != nil {
		t.Fatal(err)
	}
	if a.refreshes != 0 {
		t.Fatalf("unshared session refreshed history %d times", a.refreshes)
	}
}
