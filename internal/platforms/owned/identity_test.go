package owned

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
	"github.com/google/uuid"
)

type nativePlatform struct {
	platforms.Platform
	id           platforms.ID
	source       string
	rows         []db.Session
	detail       *platforms.SessionDetail
	sent         []platforms.SendMessageRequest
	createErr    error
	beforeCreate func(platforms.CreateSessionRequest)
	forkRequest  platforms.ForkSessionRequest
	creates      int
	events       string
}

func (p *nativePlatform) ForkSession(_ context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.creates++
	p.forkRequest = req
	return &platforms.CreateSessionResponse{ID: "forked-native"}, nil
}

func (p *nativePlatform) ID() platforms.ID               { return p.id }
func (p *nativePlatform) Available(context.Context) bool { return true }
func (p *nativePlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: string(p.id), SourceKey: p.source}, nil
}
func (p *nativePlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return p.rows, nil
}
func (p *nativePlatform) SessionIdentityMetadata(_ context.Context, id string) (*db.Session, error) {
	for _, row := range p.rows {
		if row.ID == id {
			return &row, nil
		}
	}
	return nil, platforms.ErrNotFound
}
func (p *nativePlatform) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	if id != "native" {
		return nil, fmt.Errorf("native adapter received %s", id)
	}
	return p.detail, nil
}
func (p *nativePlatform) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	p.sent = append(p.sent, req)
	return nil
}
func (p *nativePlatform) LiveStatus(id string) *platforms.LiveState {
	if id == "native" {
		return &platforms.LiveState{Status: "busy"}
	}
	return nil
}
func (p *nativePlatform) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.creates++
	if p.beforeCreate != nil {
		p.beforeCreate(req)
	}
	if p.createErr != nil {
		return nil, p.createErr
	}
	return &platforms.CreateSessionResponse{ID: "created-native"}, nil
}
func (p *nativePlatform) ProxyEvents(_ context.Context, id string, out io.Writer, flush func()) error {
	if id != "native" {
		return platforms.ErrNotFound
	}
	for _, fragment := range []string{p.events[:len(p.events)/2], p.events[len(p.events)/2:]} {
		if _, err := io.WriteString(out, fragment); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
	}
	return nil
}

func fixture(t *testing.T) (*Adapter, *nativePlatform, *sessioncatalog.Store) {
	t.Helper()
	database, err := state.Open(statetest.Path(t, sessioncatalog.Schema), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := sessioncatalog.New(database)
	if err != nil {
		t.Fatal(err)
	}
	p := &nativePlatform{id: "test", source: "source", rows: []db.Session{{ID: "native", Directory: "/repo", LastErrorName: "retained-internal-metadata"}}}
	history := statev2test.Open(t)
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "source"}); err != nil {
		t.Fatal(err)
	}
	a, err := New(t.Context(), p, store, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	return a, p, store
}

// publish commits native rows to the adapter's read model as ingestion would:
// canonical identities and parents, retained metadata only.
func publish(t *testing.T, a *Adapter, rows ...db.Session) {
	t.Helper()
	for _, row := range rows {
		snapshot := &db.HistorySnapshot{Session: row, Messages: []db.Message{}, Parts: []db.Part{}}
		ingest(t, a, snapshot)
	}
}

// ingest converts a native snapshot at the integration boundary and commits
// it to the adapter's source instance, as the ingestion worker does.
func ingest(t *testing.T, a *Adapter, snapshot *db.HistorySnapshot) string {
	t.Helper()
	instance, err := a.history.FindInstance(t.Context(), a.owner, a.source.Platform, a.source.SourceKey)
	if errors.Is(err, statev2.ErrNotFound) {
		instance = statev2.Instance{ID: statev2.SourceInstanceID(a.owner, a.source.Platform, a.source.SourceKey), OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}
		err = a.history.RegisterInstance(t.Context(), instance)
	}
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := a.historySnapshot(t.Context(), snapshot.Session.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	key := statev2.Key{InstanceID: instance.ID, SessionID: canonical.Session.ID}
	batch, _, err := conversationingest.Normalize(key, canonical)
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.history.SessionState(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	batch.ExpectedRevision = current.Revision
	if _, err := a.history.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	return canonical.Session.ID
}

func TestPlatformInterfaceUsesCanonicalSessionIDs(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || !sessioncatalog.IsID(rows[0].ID) {
		t.Fatalf("list: %+v %v", rows, err)
	}
	id := rows[0].ID
	native := &db.HistorySnapshot{Session: p.rows[0], Messages: []db.Message{{ID: "message", SessionID: "native", Data: json.RawMessage(`{"role":"user","sessionID":"opaque-native"}`)}}, Parts: []db.Part{{ID: "part", MessageID: "message", SessionID: "native", Data: json.RawMessage(`{"type":"text","text":"hi","input":{"sessionId":"opaque-native"}}`)}}}
	ingest(t, a, native)
	detail, err := a.Session(t.Context(), id, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Session.ID != id || detail.Messages[0].ID != "message" || detail.Messages[0].SessionID != id || detail.Parts[0].ID != "part" || detail.Parts[0].MessageID != "message" || detail.Parts[0].SessionID != id {
		t.Fatalf("incorrect canonical detail: %+v", detail)
	}
	var message, part map[string]any
	if json.Unmarshal(detail.Messages[0].Data, &message) != nil || message["sessionID"] != "opaque-native" || json.Unmarshal(detail.Parts[0].Data, &part) != nil || part["input"].(map[string]any)["sessionId"] != "opaque-native" {
		t.Fatalf("opaque payloads changed: %s %s", detail.Messages[0].Data, detail.Parts[0].Data)
	}
	if native.Messages[0].SessionID != "native" || p.rows[0].ID != "native" {
		t.Fatal("native snapshot or rows were mutated")
	}
	req := platforms.SendMessageRequest{SessionID: id, Message: "native is just user text"}
	if err := a.SendMessage(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if len(p.sent) != 1 || p.sent[0].SessionID != "native" || req.SessionID != id {
		t.Fatalf("mutation identity conversion: %+v %+v", p.sent, req)
	}
	if a.LiveStatus("native") != nil || a.LiveStatus(id) == nil {
		t.Fatal("live state accepts native identity")
	}
}

func TestPlatformNeverFallsBackFromUnknownCanonicalOrNativeID(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	for _, id := range []string{"native", "ocm_" + uuid.NewString()} {
		if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id}); err == nil {
			t.Fatalf("invalid identity accepted: %s", id)
		}
	}
	if len(p.sent) != 0 {
		t.Fatal("invalid reference reached native platform")
	}
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	p.source = "replacement-source"
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: rows[0].ID}); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("inactive binding regained execution: %v", err)
	}
}

func TestQuarantinedReferenceCannotAcquireExecutionAuthority(t *testing.T) {
	a, p, _ := fixture(t)
	err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: identity.PendingReference(), Message: "held until owner resolves"})
	if !errors.Is(err, platforms.ErrPlatformUnreachable) || len(p.sent) != 0 {
		t.Fatalf("quarantined reference was not unavailable: %+v %v", p.sent, err)
	}
}

func TestNativeIDCollisionsDoNotShareCanonicalIdentity(t *testing.T) {
	a, _, store := fixture(t)
	other := &nativePlatform{id: "test", source: "another-source", rows: []db.Session{{ID: "native", Directory: "/other"}}}
	b, err := New(t.Context(), other, store, statev2test.Open(t), "other-owner")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, a, db.Session{ID: "native", Directory: "/repo"})
	publish(t, b, other.rows...)
	first, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ID == second[0].ID {
		t.Fatal("native collision reused another owner's identity")
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: second[0].ID}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("cross-owner send accepted: %v", err)
	}
}

func TestNativeStreamReferencesAreConvertedInsideIntegration(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	p.events = "id: 42\r\ndata: {\"type\":\"message.part.updated\",\"properties\":{\"part\":{\"id\":\"part\",\"messageID\":\"message\",\"sessionID\":\"native\",\"state\":{\"input\":{\"sessionID\":\"native\"}}}}}\r\n\r\n: heartbeat\r\n\r\n"
	var output bytes.Buffer
	if err := a.ProxyEvents(t.Context(), rows[0].ID, &output, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: 42", `"sessionID":"` + rows[0].ID + `"`, `"id":"part"`, `"messageID":"message"`, `"input":{"sessionID":"native"}`, ": heartbeat"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("stream lost %s: %s", want, output.String())
		}
	}
}
