package pluginsmod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestSessionCompletionPreservesLargeJSONAcrossFollowups(t *testing.T) {
	core := openStore(t)
	p := &followupPlatform{detail: &platforms.SessionDetail{Session: &db.Session{Status: db.StatusBusy}}}
	var sent []platforms.SendMessageRequest
	deps := Deps{Store: core,
		Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true },
		Enqueue: func(_ context.Context, _ string, _ bool, request platforms.SendMessageRequest) error {
			sent = append(sent, request)
			return nil
		},
	}
	m := NewModule(deps)
	if err := m.plugins.LinkPluginSession(t.Context(), "test", "session-a", "org.example.results"); err != nil {
		t.Fatal(err)
	}
	var operations []string
	var delivered []plugins.SessionCompleted
	handler := plugin.SessionHandler(func(_ context.Context, operation string, c plugin.SessionCompleted) error {
		operations = append(operations, operation)
		delivered = append(delivered, c)
		return nil
	})
	installBroker := func() {
		m.sessionOnce.Do(func() {})
		m.sessionBroker = plugins.NewSessionBroker(
			func(_ context.Context, _ string, use func(plugins.Description, []string, plugins.Config) error) error {
				return use(plugins.Description{Capabilities: []plugins.Capability{plugins.FollowupSessionCapability}}, []string{plugins.SessionRunGrant}, plugins.Config{})
			}, nil,
			func(ctx context.Context, _ string, call plugins.Call) (<-chan plugins.Reply, error) {
				// Exercise the actual NDJSON codec and public SDK handler, not just DTOs.
				call.ID = uint64(len(operations) + 1)
				var wire bytes.Buffer
				if err := plugins.NewEncoder(&wire).Encode(plugins.Envelope{Type: plugins.TypeCall, Call: &call}); err != nil {
					return nil, err
				}
				frame, err := plugins.NewDecoder(&wire).Decode()
				if err != nil {
					return nil, err
				}
				value, err := handler(ctx, *frame.Call, nil)
				if err != nil {
					return nil, err
				}
				replies := make(chan plugins.Reply, 1)
				replies <- plugins.Reply{Message: plugins.Envelope{Type: plugins.TypeResult, Result: &plugins.Result{Value: value}}}
				return replies, nil
			})
	}
	installBroker()
	for turn, size := range []int{90_000, 76_300} {
		answerID := fmt.Sprintf("answer-%d", turn)
		parent := fmt.Sprintf("user-%d", turn)
		text := `{"items":["` + strings.Repeat("x", size-len(`{"items":[""]}`)) + `"]}`
		if len(text) != size || !json.Valid([]byte(text)) {
			t.Fatal("invalid synthetic JSON fixture")
		}
		p.detail.Messages = append(p.detail.Messages,
			db.Message{ID: "progress-" + answerID, TimeCreated: int64(turn*3 + 1), Data: json.RawMessage(fmt.Sprintf(`{"role":"assistant","parentID":%q,"cost":0.25,"tokens":{"input":2,"output":3}}`, parent))},
			db.Message{ID: answerID, TimeCreated: int64(turn*3 + 2), Data: json.RawMessage(fmt.Sprintf(`{"role":"assistant","parentID":%q,"cost":0.5,"tokens":{"input":5,"output":7}}`, parent))})
		p.detail.Parts = append(p.detail.Parts,
			db.Part{MessageID: "progress-" + answerID, Data: mustPartText(t, "Intermediate progress, not a result")},
			db.Part{MessageID: answerID, Data: mustPartText(t, text)})
		p.detail.Session.Status = db.StatusBusy
		m.ReplyToSession(t.Context(), "test", "session-a")
		rows, err := m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
		if err != nil || len(rows) != 0 {
			t.Fatal("busy turn captured as complete")
		}
		p.detail.Session.Status = db.StatusDone
		m.ReplyToSession(t.Context(), "test", "session-a")
		m.ReplyToSession(t.Context(), "test", "session-a")
		// Reconstruct the module before delivery to read the durable payload.
		m = NewModule(deps)
		installBroker()
		rows, err = m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("completion receipt: %d rows, %v", len(rows), err)
		}
		m.deliverSessionCompletion(t.Context(), rows[0])
		if len(delivered) != turn+1 {
			t.Fatalf("turn %d not delivered", turn)
		}
		got := delivered[turn]
		if got.Text != text || !json.Valid([]byte(got.Text)) {
			t.Errorf("turn %d JSON changed: got %d bytes, want %d; valid=%v", turn, len(got.Text), size, json.Valid([]byte(got.Text)))
		}
		if got.SessionID != "session-a" || got.Usage == nil || *got.Usage != (plugins.SessionUsage{PromptTokens: 7, CompletionTokens: 10, CostUSD: 0.75}) {
			t.Fatalf("identity/turn usage changed: %+v", got)
		}
		rows, err = m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
		if err != nil || len(rows) != 0 {
			t.Fatal("successful delivery not acknowledged")
		}
		m.ReplyToSession(t.Context(), "test", "session-a")
		if turn == 0 {
			id, err := m.sendSession(t.Context(), "org.example.results", plugins.SessionSendParams{After: operations[turn], Prompt: "Return the revised inventory as JSON"}, "revision")
			if err != nil || id != "session-a" || len(sent) != 1 || !sent[0].PreserveSelections || sent[0].Model != "" || sent[0].Agent != "" {
				t.Fatalf("follow-up selections/identity: %q %+v %v", id, sent, err)
			}
		}
	}
	if operations[0] == operations[1] {
		t.Fatal("follow-up reused completion identity")
	}
}

func TestOversizedSessionCompletionRetainsFullOutboxPayload(t *testing.T) {
	core := openStore(t)
	p := &followupPlatform{detail: &platforms.SessionDetail{
		Session:  &db.Session{Status: db.StatusDone},
		Messages: []db.Message{{ID: "answer", Data: json.RawMessage(`{"role":"assistant"}`)}},
	}}
	// Escaping alone pushes this valid JSON over the encoded frame bound.
	text := `{"value":"` + strings.Repeat("<", plugins.MaxMessageBytes/6) + `"}`
	p.detail.Parts = []db.Part{{MessageID: "answer", Data: mustPartText(t, text)}}
	m := NewModule(Deps{Store: core, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return p, true }})
	if err := m.plugins.LinkPluginSession(t.Context(), "test", "session-a", "org.example.results"); err != nil {
		t.Fatal(err)
	}
	m.sessionOnce.Do(func() {})
	m.sessionBroker = plugins.NewSessionBroker(
		func(_ context.Context, _ string, use func(plugins.Description, []string, plugins.Config) error) error {
			return use(plugins.Description{Capabilities: []plugins.Capability{plugins.SessionCapability}}, []string{plugins.SessionRunGrant}, plugins.Config{})
		}, nil, func(context.Context, string, plugins.Call) (<-chan plugins.Reply, error) {
			t.Fatal("oversized completion dispatched")
			return nil, nil
		})
	m.ReplyToSession(t.Context(), "test", "session-a")
	rows, err := m.plugins.ClaimPluginSessionCompletions(t.Context(), 10)
	if err != nil || len(rows) != 1 || rows[0].Text != text {
		t.Fatal("oversized result was lost or changed during capture")
	}
	if err := m.completePluginSession(t.Context(), rows[0], "session-a"); plugins.RequestError(err).Category != plugins.ErrorInvalidArgument {
		t.Fatalf("missing structured delivery failure: %v", err)
	}
	m.deliverSessionCompletion(t.Context(), rows[0])
	var stored, status, reason string
	var attempts int
	if err := core.SQL().QueryRow(`SELECT text,status,attempts,last_error FROM plugin_session_outbox WHERE id=?`, rows[0].ID).Scan(&stored, &status, &attempts, &reason); err != nil {
		t.Fatal(err)
	}
	if stored != text || status != "pending" || attempts != 1 || reason == "" {
		t.Fatalf("failed completion acknowledged or lost: bytes=%d status=%s attempts=%d reason=%q", len(stored), status, attempts, reason)
	}
	// Repeated terminal edges cannot replace the full result or its receipt.
	m.ReplyToSession(t.Context(), "test", "session-a")
	var count int
	if err := core.SQL().QueryRow(`SELECT count(*) FROM plugin_session_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed completion duplicated: %d %v", count, err)
	}
}
