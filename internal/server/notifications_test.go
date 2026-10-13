package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type pushTestPlatform struct {
	*fakePlatform
	prompts   []platforms.LivePrompt
	life      *platforms.SessionLifecycle
	lifeErr   error
	promptErr error
}

func (p *pushTestPlatform) NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	return p.prompts, nil, p.promptErr
}
func (p *pushTestPlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return p.life, p.lifeErr
}

func TestPushSnapshotIncludesDoneWithoutChangingBrowserProjection(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{{ID: "shell", Platform: "fake", Status: db.StatusDone, TimeUpdated: time.Now().UnixMilli()}}}, life: &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "assistant-shell", LatestMessageRole: "assistant"}}
	s.registry.Register(p)
	rows, err := s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("done completion missing: %+v %v", rows, err)
	}
	browser, err := s.notificationEntries(t.Context(), 0, 500)
	if err != nil || len(browser) != 0 {
		t.Fatalf("browser contract changed: %+v %v", browser, err)
	}
}

func TestPushSnapshotRejectsPartialReads(t *testing.T) {
	for _, kind := range []string{"sessions", "lifecycle", "nil-lifecycle", "prompts", "invalid-prompts", "stale", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			s := testServer(t)
			p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "r-owner:opencode", sessions: []db.Session{{ID: "old", Platform: "r-owner:opencode", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()}}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageID: "old-message", LatestMessageRole: "assistant"}}
			switch kind {
			case "sessions":
				p.sessionsErr = errors.New("sessions failed")
			case "lifecycle":
				p.lifeErr = errors.New("lifecycle failed")
			case "nil-lifecycle":
				p.life = nil
			case "prompts":
				p.sessions[0].PendingPermission = true
				p.promptErr = errors.New("prompts failed")
			case "invalid-prompts":
				p.sessions[0].PendingPermission = true
				p.prompts = []platforms.LivePrompt{{"id": "missing-session"}}
			case "stale":
				p.sessions[0].Stale = true
			case "unavailable":
				available := false
				p.available = &available
			}
			s.registry.Register(p)
			if _, err := s.pushSnapshot(t.Context()); err == nil {
				t.Fatal("partial snapshot committed as complete")
			}
		})
	}
}

func TestPushSnapshotOwnerIdentityAndFixedText(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "r-laptop:opencode", sessions: []db.Session{{
		ID: "session", Platform: "r-laptop:opencode", Title: "secret title", Directory: "secret directory", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli(),
	}}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "msg"}}
	s.registry.Register(p)
	rows, err := s.pushSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != "ocman:r-laptop:opencode:session:completed:msg" {
		t.Fatalf("rows=%+v", rows)
	}
	if rows[0].Notification.URL != "/session/session?platform=r-laptop%3Aopencode" || strings.Contains(rows[0].Notification.Body, "secret") {
		t.Fatal("wrong owner or leaked content")
	}
	p.sessions[0].PendingPermission = true
	p.prompts = []platforms.LivePrompt{{"id": "request", "sessionID": "child", "permission": "secret command"}}
	rows, err = s.pushSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Notification.Kind != "prompt" || !strings.HasSuffix(rows[0].Key, ":child:request") {
		t.Fatalf("prompt did not take precedence: %+v", rows)
	}
	if strings.Contains(rows[0].Notification.Body, "secret") {
		t.Fatal("permission details leaked")
	}
	p.sessions[0].PendingPermission = false
	p.life.Status = db.StatusBusy
	rows, err = s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale terminal snapshot notified a running turn: %+v %v", rows, err)
	}
	p.life.Status = db.StatusWaiting
	p.life.LatestMessageRole = "user"
	rows, err = s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("user message notified as completion: %+v %v", rows, err)
	}
}
