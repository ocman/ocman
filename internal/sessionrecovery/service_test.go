package sessionrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

type recoveryPlatform struct {
	platforms.Platform
	lifecycle *platforms.SessionLifecycle
	err       error
	detail    *platforms.SessionDetail
	reads     int
}

func (p *recoveryPlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return p.lifecycle, p.err
}

func (p *recoveryPlatform) Session(_ context.Context, _ string, limit, offset int) (*platforms.SessionDetail, error) {
	if limit != 1 || offset != 0 {
		panic("unbounded recovery read")
	}
	p.reads++
	return p.detail, nil
}

func TestLifecycleFallback(t *testing.T) {
	p := &recoveryPlatform{err: platforms.ErrUnsupported, detail: &platforms.SessionDetail{
		Session: &db.Session{Status: db.StatusInterrupted},
		Messages: []db.Message{
			{ID: "user", Data: json.RawMessage(`{"role":"user"}`)},
			{ID: "assistant", Data: json.RawMessage(`{"role":"assistant"}`)},
			{ID: "notice", Data: json.RawMessage(`{"role":"notice"}`)},
			{ID: "invalid", Data: json.RawMessage(`{`)},
		},
	}}
	l, err := interruptionLifecycle(t.Context(), p, "s")
	if err != nil || l.Status != db.StatusInterrupted || l.LatestMessageID != "assistant" || p.reads != 1 {
		t.Fatalf("fallback=%+v reads=%d err=%v", l, p.reads, err)
	}
	p.err = errors.New("lifecycle unavailable")
	if _, err := interruptionLifecycle(t.Context(), p, "s"); !errors.Is(err, p.err) || p.reads != 1 {
		t.Fatalf("unexpected fallback after real error: reads=%d err=%v", p.reads, err)
	}
	if got := latestHistoryMessage(nil); got != "" {
		t.Fatalf("empty history identity=%q", got)
	}
}

func TestHistoryUsesPlatformAndInjectionSeams(t *testing.T) {
	store := statetest.Open(t)
	p := &recoveryPlatform{lifecycle: &platforms.SessionLifecycle{Status: db.StatusInterrupted, LatestMessageID: "fresh"}}
	var injections int
	s := New(Deps{
		State: store,
		Platform: func(id platforms.ID) (platforms.Platform, bool) {
			if id != "claudecode" {
				t.Fatalf("wrong platform %q", id)
			}
			return p, true
		},
		InjectHistoryNotice: func(session, id string, at int64, data json.RawMessage, _ *[]db.Message, _ *[]db.Part) {
			injections++
			var text map[string]string
			if err := json.Unmarshal(data, &text); err != nil || text["type"] != "text" || text["text"] == "" || session != "s" || id != "ocman-interruption-fresh" || at <= 0 {
				t.Fatalf("invalid injection %q %q %d %s", session, id, at, data)
			}
		},
	})
	s.EnrichInterruptionHistory(t.Context(), "claudecode", "s", &platforms.SessionDetail{Session: &db.Session{Status: db.StatusInterrupted}})
	// A paged historical response must replay the durable notice, even after the
	// adapter moves on to another turn, without recording that new turn.
	p.lifecycle = &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "next"}
	s.EnrichInterruptionHistory(t.Context(), "claudecode", "s", &platforms.SessionDetail{Session: &db.Session{Status: db.StatusInterrupted}})
	notices, err := store.SessionInterruptions(t.Context(), "claudecode", "s")
	if err != nil || len(notices) != 1 || notices[0].MessageID != "fresh" || injections != 2 {
		t.Fatalf("notices=%v injections=%d err=%v", notices, injections, err)
	}
}

// One OpenCode server serves the machine, so a replacement affects every
// session regardless of its directory.
func TestMembershipIsMachineWide(t *testing.T) {
	s := New(Deps{})
	sessions := []db.Session{{Directory: "/checkout"}, {Directory: "/other"}}
	members, err := s.ReplacementMembership(t.Context(), "machine", sessions)
	if err != nil || len(members) != 2 || !members["/checkout"] || !members["/other"] {
		t.Fatalf("membership=%v err=%v", members, err)
	}
}

func TestMissingStateRecoveryHooks(t *testing.T) {
	s := New(Deps{Platform: func(platforms.ID) (platforms.Platform, bool) { return nil, false }})
	ctx := t.Context()
	for name, err := range map[string]error{
		"record":  s.RecordOpencodeReplacement(ctx, "/repo", "restart"),
		"begin":   s.BeginOpencodeReplacementStopWithInstance(ctx, "/repo", nil),
		"confirm": s.ConfirmOpencodeReplacement(ctx, "/repo"),
		"cancel":  s.CancelOpencodeReplacementStop(ctx, "/repo"),
	} {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if inst, err := s.OpencodeReplacementStopping(ctx, "/repo"); inst != nil || err != nil {
		t.Fatalf("stopping=%v err=%v", inst, err)
	}
}
