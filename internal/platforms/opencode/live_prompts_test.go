package opencode

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestAdapterLivePromptsBackSessionFlagsAndListingWithoutFanout(t *testing.T) {
	const (
		dir = "/repo/main"
		sid = "ses-main"
	)
	var promptHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permission" || r.URL.Path == "/question" {
			promptHits.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	withTestPort(t, dir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	// Seeded DB: force the listing below to read it rather than an
	// earlier test's snapshot.
	InvalidateSessionsCache()

	a := New(newTestDBWithSession(t, sid, dir), nil)
	a.ObservePromptAsked("", dir, "permission", platforms.LivePrompt{
		"id": "perm-1", "sessionID": sid, "permission": "Bash",
	})

	sessions, err := a.Sessions(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 || !sessions[0].PendingPermission {
		t.Fatalf("sessions pending permission = %+v, want true", sessions)
	}
	prompts, err := a.ListPermissions(context.Background(), sid)
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if len(prompts) != 1 || prompts[0]["id"] != "perm-1" {
		t.Fatalf("permissions = %#v, want perm-1", prompts)
	}
	if got := promptHits.Load(); got != 0 {
		t.Fatalf("prompt HTTP fanout hits = %d, want 0", got)
	}
}

func TestPermissionPromptBubblesFromGrandchildAcrossDirectories(t *testing.T) {
	parentID := "ses-parent"
	childID := "ses-child"
	grandchildID := "ses-grandchild"
	database := newTestDBWithSessions(t, []testSession{
		{id: parentID, directory: "/repo/main"},
		{id: childID, directory: "/repo/worktree", parentID: &parentID},
		{id: grandchildID, directory: "/repo/nested", parentID: &childID},
	})
	a := New(database, nil)
	a.ObservePromptAsked("", "/repo/nested", "permission", platforms.LivePrompt{
		"id": "perm-grandchild", "sessionID": grandchildID, "permission": "Bash",
	})

	prompts, err := a.ListPermissions(context.Background(), parentID)
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if len(prompts) != 1 || prompts[0]["id"] != "perm-grandchild" {
		t.Fatalf("permissions = %#v, want descendant perm-grandchild", prompts)
	}

	InvalidateSessionsCache()
	sessions, err := a.Sessions(context.Background(), "/repo/main", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 || !sessions[0].PendingPermission {
		t.Fatalf("top-level session pending permission = %+v, want true", sessions)
	}
}

func TestPromptReconciliationHydratesAndReplacesDirectorySnapshot(t *testing.T) {
	const dir = "/repo/worktree with space"
	var mu sync.Mutex
	pending := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != dir {
			t.Errorf("directory query = %q, want %q", got, dir)
		}
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/permission" && pending {
			_, _ = w.Write([]byte(`[{"id":"perm-snapshot","sessionID":"ses-snapshot","permission":"Bash"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	<-a.StartPromptReconciliation(context.Background(), port, []string{dir})
	prompts, _ := a.ListPermissions(context.Background(), "ses-snapshot")
	if len(prompts) != 1 {
		t.Fatalf("initial permissions = %#v, want snapshot prompt", prompts)
	}

	mu.Lock()
	pending = false
	mu.Unlock()
	<-a.StartPromptReconciliation(context.Background(), port, []string{dir})
	prompts, _ = a.ListPermissions(context.Background(), "ses-snapshot")
	if len(prompts) != 0 {
		t.Fatalf("reconnect permissions = %#v, want stale prompt removed", prompts)
	}
}

func TestPromptReconciliationDoesNotRestorePromptResolvedDuringSnapshot(t *testing.T) {
	const (
		dir = "/repo/race"
		sid = "ses-race"
		pid = "perm-race"
	)
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/permission" {
			close(requestStarted)
			<-releaseResponse
			_, _ = w.Write([]byte(`[{"id":"perm-race","sessionID":"ses-race","permission":"Bash"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	a.ObservePromptAsked("", dir, "permission", platforms.LivePrompt{
		"id": pid, "sessionID": sid, "permission": "Bash",
	})
	done := a.StartPromptReconciliation(context.Background(), strings.TrimPrefix(server.URL, "http://127.0.0.1:"), []string{dir})
	<-requestStarted
	a.ObservePromptResolved(dir, "permission", sid, pid)
	close(releaseResponse)
	<-done

	prompts, _ := a.ListPermissions(context.Background(), sid)
	if len(prompts) != 0 {
		t.Fatalf("permissions = %#v, stale snapshot restored resolved prompt", prompts)
	}
}

func TestPromptResponsesUseSessionDirectoryAndInvalidateRegistry(t *testing.T) {
	const (
		dir = "/repo/session with space"
		sid = "ses-response"
	)
	var requests atomic.Int32
	var permissionPath, permissionBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != dir {
			t.Errorf("%s directory query = %q, want %q", r.URL.Path, got, dir)
		}
		if r.Method == http.MethodPost {
			requests.Add(1)
			if strings.HasPrefix(r.URL.Path, "/permission/") {
				permissionPath = r.URL.Path
				body, _ := io.ReadAll(r.Body)
				permissionBody = string(body)
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	withTestPort(t, dir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	a := New(newTestDBWithSession(t, sid, dir), nil)
	a.ObservePromptAsked("", dir, "permission", platforms.LivePrompt{"id": "perm-1", "sessionID": sid, "permission": "Bash"})
	if err := a.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: sid, PermissionID: "perm-1", Reply: "once"}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	a.ObservePromptAsked("", dir, "question", platforms.LivePrompt{"id": "question-1", "sessionID": sid})
	if err := a.RespondQuestion(context.Background(), platforms.RespondQuestionRequest{SessionID: sid, RequestID: "question-1"}); err != nil {
		t.Fatalf("RespondQuestion: %v", err)
	}
	a.ObservePromptAsked("", dir, "question", platforms.LivePrompt{"id": "question-2", "sessionID": sid})
	if err := a.RejectQuestion(context.Background(), platforms.RejectQuestionRequest{SessionID: sid, RequestID: "question-2"}); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}

	permissions, _ := a.ListPermissions(context.Background(), sid)
	questions, _ := a.ListQuestions(context.Background(), sid)
	if len(permissions) != 0 || len(questions) != 0 {
		t.Fatalf("remaining prompts = permissions:%#v questions:%#v", permissions, questions)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("response requests = %d, want 3", got)
	}
	if permissionPath != "/permission/perm-1/reply" || permissionBody != `{"reply":"once"}` {
		t.Fatalf("permission response = %s %s", permissionPath, permissionBody)
	}
}

func TestProxyEventsUsesActualSessionDirectory(t *testing.T) {
	const (
		dir = "/repo/events with space"
		sid = "ses-events-dir"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != dir {
			t.Errorf("directory query = %q, want %q", got, dir)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {}\n\n"))
	}))
	defer server.Close()
	withTestPort(t, dir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	a := New(newTestDBWithSession(t, sid, dir), nil)
	var events bytes.Buffer
	if err := a.ProxyEvents(context.Background(), sid, &events, nil); err != nil {
		t.Fatalf("ProxyEvents: %v", err)
	}
}

func TestPromptReconciliationIncludesWorktreeSessionDirectories(t *testing.T) {
	const (
		root     = "/repo/project"
		worktree = "/repo/.worktrees/project/feature"
	)
	// promptDirectories reads the shared sessions cache; another test's
	// entry would otherwise satisfy the lookup.
	ResetCachesForTests()
	seen := make(map[string]bool)
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Query().Get("directory")] = true
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	database := newTestDBWithSessions(t, []testSession{
		{id: "ses-root", directory: root},
		{id: "ses-worktree", directory: worktree, busy: true},
		{id: "ses-old", directory: "/repo/.worktrees/project/old"},
	})
	a := New(database, nil)
	<-a.StartPromptReconciliation(context.Background(), strings.TrimPrefix(server.URL, "http://127.0.0.1:"), []string{root})

	mu.Lock()
	defer mu.Unlock()
	if !seen[root] || !seen[worktree] {
		t.Fatalf("reconciled directories = %v, want root and worktree", seen)
	}
	if seen["/repo/.worktrees/project/old"] {
		t.Fatalf("reconciled inactive directory: %v", seen)
	}
}

func TestNewerPromptReconciliationWinsOlderSlowSnapshot(t *testing.T) {
	const dir = "/repo/reconnect-race"
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var permissionRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/permission" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if permissionRequests.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			_, _ = w.Write([]byte(`[{"id":"stale-permission","sessionID":"ses-race","permission":"Bash"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	olderDone := a.StartPromptReconciliation(context.Background(), port, []string{dir})
	<-firstStarted
	<-a.StartPromptReconciliation(context.Background(), port, []string{dir})
	close(releaseFirst)
	<-olderDone

	prompts, _ := a.ListPermissions(context.Background(), "ses-race")
	if len(prompts) != 0 {
		t.Fatalf("permissions = %#v, older snapshot overwrote newer reconciliation", prompts)
	}
}

func TestPromptReconciliationReportsPendingPermissions(t *testing.T) {
	const dir = "/repo/reconcile-callback"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/permission" {
			_, _ = w.Write([]byte(`[{"id":"perm-reconcile","sessionID":"ses-reconcile","permission":"Bash","patterns":[],"metadata":{}}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	var got []platforms.LivePrompt
	<-a.StartPromptReconciliation(
		context.Background(),
		strings.TrimPrefix(server.URL, "http://127.0.0.1:"),
		[]string{dir},
		func(prompt platforms.LivePrompt) { got = append(got, prompt) },
	)
	if len(got) != 1 || got[0]["id"] != "perm-reconcile" {
		t.Fatalf("reported permissions = %#v, want perm-reconcile", got)
	}
}

func TestClearPromptsForPortRemovesOnlyOwnedDirectories(t *testing.T) {
	a := New(nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	<-a.StartPromptReconciliation(context.Background(), port, []string{"/repo/owned"})

	a.ObservePromptAsked(port, "/repo/owned", "permission", platforms.LivePrompt{"id": "owned", "sessionID": "ses-owned"})
	a.ObservePromptAsked("other-port", "/repo/other", "permission", platforms.LivePrompt{"id": "other", "sessionID": "ses-other"})
	a.ClearPromptsForPort(port)

	owned, _ := a.ListPermissions(context.Background(), "ses-owned")
	other, _ := a.ListPermissions(context.Background(), "ses-other")
	if len(owned) != 0 || len(other) != 1 {
		t.Fatalf("after clear: owned=%#v other=%#v", owned, other)
	}
}

func TestPromptReconciliationMergesUnrelatedConcurrentAsk(t *testing.T) {
	const dir = "/repo/merge-race"
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/permission" {
			close(started)
			<-release
			_, _ = w.Write([]byte(`[{"id":"before","sessionID":"ses-before","permission":"Bash"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	done := a.StartPromptReconciliation(context.Background(), port, []string{dir})
	<-started
	a.ObservePromptAsked(port, dir, "permission", platforms.LivePrompt{"id": "during", "sessionID": "ses-during"})
	close(release)
	<-done

	before, _ := a.ListPermissions(context.Background(), "ses-before")
	during, _ := a.ListPermissions(context.Background(), "ses-during")
	if len(before) != 1 || len(during) != 1 {
		t.Fatalf("merged snapshot: before=%#v during=%#v", before, during)
	}
}

func TestPromptReconciliationRetriesTransientSnapshotFailure(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/permission" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"after-retry","sessionID":"ses-retry","permission":"Bash"}]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	<-a.StartPromptReconciliation(context.Background(), strings.TrimPrefix(server.URL, "http://127.0.0.1:"), []string{"/repo/retry"})
	prompts, _ := a.ListPermissions(context.Background(), "ses-retry")
	if len(prompts) != 1 || hits.Load() != 2 {
		t.Fatalf("prompts=%#v hits=%d, want recovered second attempt", prompts, hits.Load())
	}
}

func TestListQuestionsRefreshesAuthoritativeSnapshot(t *testing.T) {
	const dir = "/repo/questions"
	const sid = "ses-questions"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	withTestPort(t, dir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	a := New(newTestDBWithSession(t, sid, dir), nil)
	a.ObservePromptAsked("", dir, "question", platforms.LivePrompt{"id": "stale", "sessionID": sid})
	prompts, err := a.ListQuestions(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 0 {
		t.Fatalf("questions=%#v, want authoritative dismissal", prompts)
	}
}

func TestBubbledQuestionResponseUsesIssuingChildDirectory(t *testing.T) {
	parentID, childID := "ses-parent-question", "ses-child-question"
	parentDir, childDir := "/repo/main", "/repo/worktree"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("directory"); got != childDir {
			t.Errorf("response directory=%q, want child directory %q", got, childDir)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	withTestPort(t, parentDir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	database := newTestDBWithSessions(t, []testSession{
		{id: parentID, directory: parentDir},
		{id: childID, directory: childDir, parentID: &parentID},
	})
	a := New(database, nil)
	a.ObservePromptAsked("", childDir, "question", platforms.LivePrompt{"id": "child-question", "sessionID": childID})
	if err := a.RespondQuestion(context.Background(), platforms.RespondQuestionRequest{SessionID: parentID, RequestID: "child-question"}); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectReconcilesPreviouslyObservedIdleDirectory(t *testing.T) {
	const root, worktree = "/repo/main", "/repo/worktree-idle"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")

	a := New(nil, nil)
	<-a.StartPromptReconciliation(context.Background(), port, []string{root})
	a.ObservePromptAsked(port, worktree, "permission", platforms.LivePrompt{"id": "stale-idle", "sessionID": "ses-idle"})
	<-a.StartPromptReconciliation(context.Background(), port, []string{root})

	prompts, _ := a.ListPermissions(context.Background(), "ses-idle")
	if len(prompts) != 0 {
		t.Fatalf("idle worktree prompts=%#v, want stale prompt removed on reconnect", prompts)
	}
}

func TestClearPromptsForPortBeatsInflightSnapshot(t *testing.T) {
	const dir = "/repo/disappeared"
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/permission" {
			close(started)
			<-release
			_, _ = w.Write([]byte(`[{"id":"ghost","sessionID":"ses-ghost","permission":"Bash"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	a := New(nil, nil)
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	done := a.StartPromptReconciliation(context.Background(), port, []string{dir})
	<-started
	a.ClearPromptsForPort(port)
	close(release)
	<-done
	prompts, _ := a.ListPermissions(context.Background(), "ses-ghost")
	if len(prompts) != 0 {
		t.Fatalf("dead-port prompts=%#v, want empty", prompts)
	}
}

func TestClearPromptsForPortRejectsBufferedOldStreamEvent(t *testing.T) {
	a := New(nil, nil)
	const port = "12345"
	generation := a.PromptPortGeneration(port)
	a.ClearPromptsForPort(port)
	a.ObservePromptAskedFromPort(port, generation, "/repo/dead", "permission", platforms.LivePrompt{"id": "ghost-event", "sessionID": "ses-ghost-event"})
	prompts, _ := a.ListPermissions(context.Background(), "ses-ghost-event")
	if len(prompts) != 0 {
		t.Fatalf("old-stream prompts=%#v, want empty", prompts)
	}
}

func TestListQuestionsReturnsErrorWhenAuthoritativeRefreshFails(t *testing.T) {
	const dir, sid = "/repo/question-failure", "ses-question-failure"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withTestPort(t, dir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	a := New(newTestDBWithSession(t, sid, dir), nil)
	if _, err := a.ListQuestions(context.Background(), sid); err == nil {
		t.Fatal("ListQuestions returned nil error for failed authoritative refresh")
	}
}

func TestListQuestionsDiscoversMissedChildDirectoryPrompt(t *testing.T) {
	parentID, childID := "ses-parent-missed", "ses-child-missed"
	parentDir, childDir := "/repo/main", "/repo/worktree-missed"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/question" && r.URL.Query().Get("directory") == childDir {
			_, _ = w.Write([]byte(`[{"id":"missed-child-question","sessionID":"ses-child-missed","questions":[]}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	withTestPort(t, parentDir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))

	database := newTestDBWithSessions(t, []testSession{
		{id: parentID, directory: parentDir},
		{id: childID, directory: childDir, parentID: &parentID},
	})
	a := New(database, nil)
	prompts, err := a.ListQuestions(context.Background(), parentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0]["id"] != "missed-child-question" {
		t.Fatalf("questions=%#v, want missed child prompt", prompts)
	}
}
