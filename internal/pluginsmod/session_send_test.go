package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type followupPlatform struct {
	platforms.Platform
	detail *platforms.SessionDetail
}

func (p *followupPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	return p.detail, nil
}

func followupFixture(t *testing.T) (*Module, *followupPlatform, string, *[]platforms.SendMessageRequest) {
	t.Helper()
	s := openPluginStore(t)
	if err := s.LinkPluginSession(t.Context(), "test", "session-a", "plugin-a"); err != nil {
		t.Fatal(err)
	}
	row, err := s.AppendPluginSessionCompletion(t.Context(), "plugin-a", "test", "session-a", "session-a:answer-a", "Index complete")
	if err != nil {
		t.Fatal(err)
	}
	p := &followupPlatform{detail: &platforms.SessionDetail{
		Session:  &db.Session{Status: db.StatusDone},
		Messages: []db.Message{{ID: "answer-a", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)}},
		Parts:    []db.Part{{MessageID: "answer-a", Data: json.RawMessage(`{"type":"text","text":"Index complete"}`)}},
	}}
	sent := new([]platforms.SendMessageRequest)
	m := &Module{plugins: s, deps: Deps{
		Adapter: func(_ context.Context, platform, session string) (platforms.Platform, bool) {
			if platform != "test" || session != "session-a" {
				t.Fatalf("wrong owner/session: %s %s", platform, session)
			}
			return p, true
		},
		Enqueue: func(_ context.Context, platform string, _ bool, r platforms.SendMessageRequest) error {
			if platform != "test" {
				t.Fatal("wrong queue owner")
			}
			*sent = append(*sent, r)
			return nil
		},
	}}
	return m, p, sessionOutboxOperation(row), sent
}

func TestSessionFollowupKeepsSessionAndDeduplicatesAcrossRestart(t *testing.T) {
	for _, prompt := range []string{"List the documentation headings as JSON", "Clarify the dependency manifest ambiguity"} {
		t.Run(prompt, func(t *testing.T) {
			m, _, after, sent := followupFixture(t)
			params := plugins.SessionSendParams{After: after, Prompt: prompt}
			id, err := m.sendSession(t.Context(), "plugin-a", params, "followup-a")
			if err != nil || id != "session-a" || len(*sent) != 1 {
				t.Fatalf("follow-up failed: id=%s sent=%d err=%v", id, len(*sent), err)
			}
			if (*sent)[0].SessionID != "session-a" || (*sent)[0].Agent != "" || (*sent)[0].Model != "" || (*sent)[0].Message != prompt {
				t.Fatalf("session selections changed: %+v", (*sent)[0])
			}
			restarted := &Module{plugins: m.plugins, deps: m.deps}
			if _, err := restarted.sendSession(t.Context(), "plugin-a", params, "followup-a"); err != nil || len(*sent) != 1 {
				t.Fatalf("replay duplicated follow-up: %v", err)
			}
			if _, err := restarted.sendSession(t.Context(), "plugin-a", params, "different-operation"); err == nil || len(*sent) != 1 {
				t.Fatal("second send admitted for same completion")
			}
		})
	}
}

func TestSessionFollowupRefusesForeignStaleBusyAndUncertainWork(t *testing.T) {
	for _, mode := range []string{"foreign", "busy", "stale", "newer-message", "enqueue-error", "invalid-after"} {
		t.Run(mode, func(t *testing.T) {
			m, p, after, sent := followupFixture(t)
			pluginID := "plugin-a"
			switch mode {
			case "foreign":
				pluginID = "plugin-b"
			case "busy":
				p.detail.Session.Status = db.StatusBusy
			case "stale":
				if _, err := m.plugins.AppendPluginSessionCompletion(t.Context(), "plugin-a", "test", "session-a", "session-a:answer-b", "new answer"); err != nil {
					t.Fatal(err)
				}
			case "newer-message":
				p.detail.Messages[0].ID = "answer-b"
			case "enqueue-error":
				m.deps.Enqueue = func(context.Context, string, bool, platforms.SendMessageRequest) error {
					return errors.New("uncertain enqueue")
				}
			case "invalid-after":
				after = "session-a"
			}
			params := plugins.SessionSendParams{After: after, Prompt: "Clarify"}
			if _, err := m.sendSession(t.Context(), pluginID, params, "followup-a"); err == nil {
				t.Fatal("unsafe follow-up accepted")
			}
			if len(*sent) != 0 {
				t.Fatal("refused follow-up sent a message")
			}
			if mode == "enqueue-error" {
				m.deps.Enqueue = func(context.Context, string, bool, platforms.SendMessageRequest) error {
					t.Fatal("uncertain enqueue replayed")
					return nil
				}
				if _, err := m.sendSession(t.Context(), pluginID, params, "followup-a"); err == nil {
					t.Fatal("uncertain admission became successful")
				}
			}
		})
	}
}
