package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
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
	if rows[0].Notification.BadgeCount != 0 {
		t.Fatalf("push badge counts a done envelope hidden by the app: %+v", rows)
	}
	browser, err := s.notificationEntries(t.Context(), 0, 500)
	if err != nil || len(browser) != 0 {
		t.Fatalf("browser contract changed: %+v %v", browser, err)
	}
	w := httptest.NewRecorder()
	s.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify?includeDone=1", nil))
	var badgeEntries []notifyEntry
	if err := json.Unmarshal(w.Body.Bytes(), &badgeEntries); err != nil || len(badgeEntries) != 1 || badgeEntries[0].Platform != "fake" {
		t.Fatalf("badge projection omitted completed envelope: %s", w.Body.String())
	}
}

func TestPushSnapshotUsesRemoteOwnersPermissionDecision(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "r-owner:opencode", sessions: []db.Session{{ID: "session", Platform: "r-owner:opencode", PendingPermission: true, Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()}}},
		prompts: []platforms.LivePrompt{{"id": "request", "sessionID": "child", "ocmanNotificationDeferred": true}},
		life:    &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "old"}}
	s.registry.Register(p)
	p.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return p.prompts, nil }
	rows, err := s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("judge-owned permission alerted: %+v %v", rows, err)
	}
	p.prompts[0]["ocmanNotificationDeferred"] = false
	rows, err = s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Notification.Kind != "prompt" {
		t.Fatalf("manual permission not alerted: %+v %v", rows, err)
	}
	p.prompts = append(p.prompts, platforms.LivePrompt{"id": "judging", "sessionID": "other-child", platforms.NotificationDeferredField: true}, platforms.LivePrompt{"id": "legacy", "sessionID": "old-child"})
	rows, err = s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 1 || !strings.HasSuffix(rows[0].Key, ":child:request") {
		t.Fatalf("mixed permission decisions lost: %+v %v", rows, err)
	}
	p.prompts = append(p.prompts, platforms.LivePrompt{"id": "another-request", "sessionID": "another-child", platforms.NotificationDeferredField: false})
	rows, err = s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 2 || rows[0].Notification.BadgeCount != 1 || rows[1].Notification.BadgeCount != 1 {
		t.Fatalf("badge counted prompts rather than sessions: %+v %v", rows, err)
	}
	p.prompts = p.prompts[2:3]
	rows, err = s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("legacy owner guessed as manual: %+v %v", rows, err)
	}
	browser, err := s.notificationEntries(t.Context(), 0, 500)
	if err != nil || len(browser) != 1 || !browser[0].PendingPermission {
		t.Fatalf("legacy browser projection changed: %+v %v", browser, err)
	}
}

func TestOwnerRPCDefersPermissionsDuringJudgeDelay(t *testing.T) {
	s := testServer(t).WithAutoApproveDefault(true)
	s.autoapproveMod.set(s.newAutoapproveModule(autoapprove.Deps{DefaultEnabled: true, SessionDir: func(string) (string, error) { return "/repo", nil }}))
	p := &fakePlatform{id: "owner", caps: platforms.Capabilities{AutoApprove: true}, listPermissionsFn: func(string) ([]platforms.LivePrompt, error) {
		return []platforms.LivePrompt{{"id": "request", "sessionID": "child"}}, nil
	}}
	s.registry.Register(p)
	s.aaSvc().SetJudgeDelayMs(30_000)
	s.aaSvc().Ensure("owner", p, "child", "request", "Bash", nil, map[string]any{"command": "pwd"})
	defer s.aaSvc().Cancel("child", "request")
	owner := remote.NewServer(s.registry, nil, "machine", "test").UsePermissionNotificationDeferrer(s.PermissionNotificationDeferred)
	for _, want := range []bool{true, false} {
		resp, err := owner.ListPermissions(t.Context(), &pb.SessionRef{Platform: "owner", SessionId: "parent"})
		if err != nil {
			t.Fatal(err)
		}
		var prompts []platforms.LivePrompt
		if err := json.Unmarshal(resp.Payload, &prompts); err != nil {
			t.Fatal(err)
		}
		if prompts[0][platforms.NotificationDeferredField] != want {
			t.Fatalf("owner judge decision=%v want=%v", prompts[0], want)
		}
		s.aaSvc().Cancel("child", "request")
		deadline := time.Now().Add(time.Second)
		for s.PermissionNotificationDeferred("child", "request") && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
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

func TestPushSnapshotOwnerIdentityAndSessionContext(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "r-laptop:opencode", sessions: []db.Session{{
		ID: "session", Platform: "r-laptop:opencode", Title: "Fix billing", Directory: "/repo/billing", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli(),
	}}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "msg"}}
	s.registry.Register(p)
	rows, err := s.pushSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != "ocman:r-laptop:opencode:session:completed:msg" {
		t.Fatalf("rows=%+v", rows)
	}
	if rows[0].Notification.URL != "/session/session?platform=r-laptop%3Aopencode&notification=1" || rows[0].Notification.Title != "Fix billing" || rows[0].Notification.Body != "Finished · billing" {
		t.Fatal("wrong owner or missing session context")
	}
	p.sessions[0].PendingPermission = true
	p.prompts = []platforms.LivePrompt{{"id": "request", "sessionID": "child", "permission": "secret command", platforms.NotificationDeferredField: false}}
	p.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return p.prompts, nil }
	rows, err = s.pushSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Notification.Kind != "prompt" || !strings.HasSuffix(rows[0].Key, ":child:request") {
		t.Fatalf("prompt did not take precedence: %+v", rows)
	}
	if strings.Contains(rows[0].Notification.Body, "secret command") || rows[0].Notification.Body != "Permission needed · billing" {
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

func TestNotificationText(t *testing.T) {
	for _, tc := range []struct{ title, directory, action, wantTitle, wantBody string }{
		{"Fix checkout", "/repo/shop", "Finished", "Fix checkout", "Finished · shop"},
		{"  Debug\n tests ", `C:\repo\service`, "Answer needed", "Debug tests", "Answer needed · service"},
		{"", "", "Failed", "Untitled session", "Failed"},
		{"Root task", "/", "Permission needed", "Root task", "Permission needed"},
	} {
		title, body := notificationText(tc.title, tc.directory, tc.action)
		if title != tc.wantTitle || body != tc.wantBody {
			t.Fatalf("title=%q body=%q", title, body)
		}
	}
	title, body := notificationText(strings.Repeat("界", 200), "/repo/"+strings.Repeat("名", 200), "Finished")
	if len([]rune(title)) != 161 || len(body) > 1000 {
		t.Fatal("notification payload is unbounded")
	}
}

func TestPushBadgeCountsInterruptedAttentionSessions(t *testing.T) {
	s := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{
		{ID: "interrupted", Platform: "fake", Status: db.StatusInterrupted, TimeUpdated: time.Now().UnixMilli()},
		{ID: "finished", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()},
		{ID: "shell", Platform: "fake", Status: db.StatusDone, TimeUpdated: time.Now().UnixMilli()},
		{ID: "read", Platform: "fake", Status: db.StatusWaiting, Seen: true, TimeUpdated: time.Now().UnixMilli()},
	}}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "message"}}
	s.registry.Register(p)
	rows, err := s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 2 || rows[0].Notification.BadgeCount != 2 || rows[1].Notification.BadgeCount != 2 {
		t.Fatalf("attention badge missed interrupted session: %+v %v", rows, err)
	}
}

func TestNotificationBadgeExcludesArchivedSessions(t *testing.T) {
	s := testServer(t)
	now := time.Now().UnixMilli()
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "fake", sessions: []db.Session{
		{ID: "unread", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: now},
		{ID: "prompt", Platform: "fake", Status: db.StatusBusy, Seen: true, PendingPermission: true, TimeUpdated: now},
		{ID: "archived", Platform: "fake", Status: db.StatusWaiting, TimeUpdated: now},
		{ID: "archived-project", Platform: "fake", Directory: "/archived-project", Status: db.StatusWaiting, TimeUpdated: now},
	}}, prompts: []platforms.LivePrompt{{"id": "request", "sessionID": "prompt"}},
		life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "message"}}
	s.registry.Register(p)
	if err := s.stateDB.ArchiveSession(t.Context(), "fake", "archived", now); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.ArchiveProject(t.Context(), state.LocalRemoteID, "/archived-project"); err != nil {
		t.Fatal(err)
	}
	for _, includeDone := range []string{"", "&includeDone=1"} {
		w := httptest.NewRecorder()
		s.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify?limit=500"+includeDone, nil))
		var entries []notifyEntry
		if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil || len(entries) != 2 {
			t.Fatalf("browser badge should count only unread and prompt sessions: %s, %v", w.Body.String(), err)
		}
	}
	rows, err := s.pushSnapshot(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatalf("push should notify only unread and prompt sessions: %+v, %v", rows, err)
	}
	for _, row := range rows {
		if row.Notification.BadgeCount != 2 {
			t.Fatalf("push badge should be 2: %+v", row)
		}
	}
}

func TestNotifyProjectArchiveIsOwnerScoped(t *testing.T) {
	s := testServer(t)
	if err := s.stateDB.ArchiveProject(t.Context(), "owner", "/repo"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		owner   string
		updated int64
		want    int
	}{
		{"owner", 1, 0},
		{"other", 1, 1},
		{"", 1, 1},
		{"owner", time.Now().Add(time.Hour).UnixMilli(), 1},
	} {
		entries, err := s.notificationEntriesForSessions(t.Context(), []db.Session{{
			ID: "session", Platform: "fake", RemoteID: tc.owner, Directory: "/repo",
			Status: db.StatusWaiting, TimeUpdated: tc.updated,
		}}, false, true)
		if err != nil || len(entries) != tc.want {
			t.Fatalf("owner=%q updated=%d: entries=%+v, err=%v", tc.owner, tc.updated, entries, err)
		}
	}
}
