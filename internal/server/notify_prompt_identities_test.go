package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type cachedNotifyPlatform struct{ fakePlatform }

type emptyCachedNotifyPlatform struct{ fakePlatform }

func (p *emptyCachedNotifyPlatform) NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	return nil, nil, nil
}

func TestNotifyDropsBusyRowResolvedBeforeIdentityRead(t *testing.T) {
	s := testServer(t)
	s.registry.Register(&emptyCachedNotifyPlatform{fakePlatform{
		id: "fake", sessions: []db.Session{{ID: "parent", Platform: "fake", Status: db.StatusBusy, PendingPermission: true}},
	}})
	w := httptest.NewRecorder()
	s.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil))
	if w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("stale busy notification: status=%d body=%s", w.Code, w.Body)
	}
}

func (p *cachedNotifyPlatform) NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	return []platforms.LivePrompt{{"id": "p1", "sessionID": "child"}, {"id": "malformed"}},
		[]platforms.LivePrompt{{"id": "q1", "sessionID": "grandchild"}}, nil
}

type failedCachedNotifyPlatform struct{ fakePlatform }

func (p *failedCachedNotifyPlatform) NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	return nil, nil, errors.New("ancestor lookup failed")
}

func TestNotifyCachedLookupFailurePreservesFlags(t *testing.T) {
	s := testServer(t)
	s.registry.Register(&failedCachedNotifyPlatform{fakePlatform{id: "fake"}})
	entry := notifyEntry{ID: "parent", PendingPermission: true, PendingQuestion: true}
	s.notifyPromptIdentities(t.Context(), &db.Session{ID: "parent", Platform: "fake"}, &entry)
	if !entry.PendingPermission || !entry.PendingQuestion || entry.Permissions != nil || entry.Questions != nil {
		t.Fatalf("lookup failure hid pending child prompts: %+v", entry)
	}
}

func TestNotifyDeferredPermissionDoesNotTurnResolvedQuestionIntoCompletion(t *testing.T) {
	s := testServer(t).WithAutoApproveDefault(true)
	s.aaSvcCached = autoapprove.NewService(autoapprove.Deps{
		DefaultEnabled: true, SessionDir: func(string) (string, error) { return "/repo", nil },
	})
	s.aaOnce.Do(func() {})
	adapter := &emptyCachedNotifyPlatform{fakePlatform{
		id: "fake", caps: platforms.Capabilities{AutoApprove: true},
		sessions: []db.Session{{ID: "parent", Platform: "fake", Status: db.StatusWaiting, PendingPermission: true, PendingQuestion: true}},
		listPermissionsFn: func(string) ([]platforms.LivePrompt, error) {
			return []platforms.LivePrompt{{"id": "p1", "sessionID": "parent"}}, nil
		},
	}}
	s.registry.Register(adapter)
	s.aaSvc().SetJudgeDelayMs(30_000)
	s.aaSvc().Ensure("fake", adapter, "parent", "p1", "Bash", nil, map[string]any{"command": "pwd"})
	defer s.aaSvc().Cancel("parent", "p1")
	w := httptest.NewRecorder()
	s.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil))
	if w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("AI-deferred permission became completion: status=%d body=%s", w.Code, w.Body)
	}
}

type remoteNotifyPlatform struct {
	fakePlatform
	stall bool
}

func (p *remoteNotifyPlatform) ListPermissions(ctx context.Context, _ string) ([]platforms.LivePrompt, error) {
	if p.stall {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []platforms.LivePrompt{}, nil
}

func (p *remoteNotifyPlatform) ListQuestions(ctx context.Context, _ string) ([]platforms.LivePrompt, error) {
	if p.stall {
		return nil, errors.New("offline")
	}
	return []platforms.LivePrompt{}, nil
}

func TestNotifyIdentitiesEmptySnapshotClearsEarlierFlags(t *testing.T) {
	s := testServer(t)
	s.registry.Register(&remoteNotifyPlatform{fakePlatform: fakePlatform{id: "r-box:opencode"}})
	row := db.Session{ID: "parent", Platform: "r-box:opencode"}
	entry := notifyEntry{ID: "parent", PendingPermission: true, PendingQuestion: true}
	// Fan-out captured pending=true, then the child prompt resolved before
	// identity collection. Successful empty reads must clear the stale flags.
	s.notifyPromptIdentities(t.Context(), &row, &entry)
	body, _ := json.Marshal(entry)
	if entry.PendingPermission || entry.PendingQuestion || string(body) != `{"id":"parent","status":"","seen":false,"permissions":[],"questions":[]}` {
		t.Fatalf("entry=%+v JSON=%s", entry, body)
	}
}

func TestNotifyIdentitiesStalledRemoteDoesNotBlockHealthyOwner(t *testing.T) {
	s := testServer(t)
	s.registry.Register(&remoteNotifyPlatform{fakePlatform: fakePlatform{id: "r-box:opencode"}, stall: true})
	s.registry.Register(&cachedNotifyPlatform{fakePlatform{id: "local"}})
	rows := []db.Session{{ID: "same", Platform: "r-box:opencode"}, {ID: "same", Platform: "local"}}
	entries := []notifyEntry{
		{ID: "same", platform: "r-box:opencode", PendingPermission: true, PendingQuestion: true},
		{ID: "same", platform: "local", PendingPermission: true},
	}
	start := time.Now()
	done := make(chan struct{})
	go func() {
		s.enrichNotifyPrompts(t.Context(), rows, entries)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(remoteFanoutTimeout + time.Second):
		t.Fatal("stalled remote escaped the notify identity budget")
	}
	if time.Since(start) > remoteFanoutTimeout+time.Second || entries[0].Permissions != nil || !entries[0].PendingPermission || entries[1].Permissions == nil {
		t.Fatalf("unbounded or cross-owner enrichment: %+v", entries)
	}
}

func TestNotifyPromptIdentities(t *testing.T) {
	s := testServer(t)
	s.registry.Register(&cachedNotifyPlatform{fakePlatform{id: "r-laptop:opencode"}})
	row := db.Session{ID: "parent", Platform: "r-laptop:opencode"}
	entry := notifyEntry{ID: "parent", PendingPermission: true, PendingQuestion: true}
	s.notifyPromptIdentities(t.Context(), &row, &entry)
	if !reflect.DeepEqual(*entry.Permissions, []notifyPrompt{{"r-laptop:opencode", "child", "p1"}}) ||
		!reflect.DeepEqual(*entry.Questions, []notifyPrompt{{"r-laptop:opencode", "grandchild", "q1"}}) {
		t.Fatalf("entry=%+v", entry)
	}
	entry = notifyEntry{}
	s.notifyPromptIdentities(t.Context(), &row, &entry)
	if entry.Permissions != nil || entry.Questions != nil {
		t.Fatal("non-pending entry fetched prompt identities")
	}
	row.Platform = "absent"
	s.notifyPromptIdentities(t.Context(), &row, &entry)
	s.registry = nil
	s.notifyPromptIdentities(t.Context(), &row, &entry)
}
