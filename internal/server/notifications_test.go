package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type pushTestPlatform struct {
	*fakePlatform
	prompts []platforms.LivePrompt
	life    *platforms.SessionLifecycle
}

func (p *pushTestPlatform) NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	return p.prompts, nil, nil
}
func (p *pushTestPlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return p.life, nil
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
