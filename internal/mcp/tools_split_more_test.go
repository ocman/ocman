package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// splitToolReq builds a CallToolRequest carrying the given arguments.
func splitToolReq(args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Name = "new_session"
	req.Params.Arguments = args
	return req
}

// flakySessionReader is a sessionReader whose GetSession succeeds okCalls
// times and fails afterwards, so tests can separate PromptComposer's
// lookup from the handler's own one.
type flakySessionReader struct {
	session  *db.Session
	messages []db.Message
	okCalls  int
	calls    int
	err      error
}

func (f *flakySessionReader) GetSession(context.Context, string) (*db.Session, error) {
	f.calls++
	if f.calls > f.okCalls {
		return nil, f.err
	}
	return f.session, nil
}

func (f *flakySessionReader) GetSessionMessages(context.Context, string) ([]db.Message, error) {
	return f.messages, nil
}

// fakeInheriter implements permissionInheriter with scripted answers.
type fakeInheriter struct {
	on       bool
	onErr    error
	approved []state.ApprovedPermission
	listErr  error
}

func (f *fakeInheriter) GetWorktreeInheritPermissions(context.Context) (bool, error) {
	return f.on, f.onErr
}

func (f *fakeInheriter) ListApprovedPermissions(_ context.Context, _, _ string) ([]state.ApprovedPermission, error) {
	return f.approved, f.listErr
}

// scriptedChildStore is a childResultStore whose GetChildSession and
// CompareAndSetChildResultDelivery answers are scripted per call. Each
// slice is consumed in order and its last entry repeats, so a test only
// spells out the calls it cares about.
type scriptedChildStore struct {
	get      []scriptedGet
	getN     int
	list     []state.ChildSession
	listErr  error
	cas      []scriptedCAS
	casN     int
	casCalls []string
}

type scriptedGet struct {
	child *state.ChildSession
	err   error
}

type scriptedCAS struct {
	claimed bool
	err     error
}

func (s *scriptedChildStore) GetChildSession(context.Context, string) (*state.ChildSession, error) {
	if len(s.get) == 0 {
		return nil, sql.ErrNoRows
	}
	i := min(s.getN, len(s.get)-1)
	s.getN++
	return s.get[i].child, s.get[i].err
}

func (s *scriptedChildStore) ListDisconnectedChildSessions(context.Context, string) ([]state.ChildSession, error) {
	return s.list, s.listErr
}

func (s *scriptedChildStore) CompareAndSetChildResultDelivery(_ context.Context, id, from, to string) (bool, error) {
	s.casCalls = append(s.casCalls, id+":"+from+"->"+to)
	if len(s.cas) == 0 {
		return true, nil
	}
	i := min(s.casN, len(s.cas)-1)
	s.casN++
	return s.cas[i].claimed, s.cas[i].err
}

// newSplitTools assembles a splitTools over the given session reader and
// platform adapter, mirroring the production wiring.
func newSplitTools(t *testing.T, reader sessionReader, platform platformAdapter) *splitTools {
	t.Helper()
	return newSplitToolsWithCreator(t, reader, platform, noopWorktreeCreator)
}

// newSplitToolsWithCreator is newSplitTools with an explicit stand-in for
// the owning host's CreateWorktreeSession.
func newSplitToolsWithCreator(t *testing.T, reader sessionReader, platform platformAdapter, creator WorktreeSessionCreator) *splitTools {
	t.Helper()
	launcher := NewSessionLauncher(openTestStateDB(t), platform, creator, noopEnsurer)
	return &splitTools{
		composer: NewPromptComposer(reader, noopGit),
		launcher: launcher,
		platform: "opencode",
	}
}

// initTestRepo creates an empty git repository with one commit so
// git.ResolveRepoRoot / ResolveBaseRef succeed against it.
func initTestRepo(t *testing.T) string {
	t.Helper()
	testutil.RequireGit(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Strip GIT_DIR / GIT_INDEX_FILE so this operates on dir, not the
		// ambient repo when run inside a git hook.
		cmd.Env = gitexec.CleanEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// wantToolError asserts the result is an error result whose text contains
// want.
func wantToolError(t *testing.T, res *mcplib.CallToolResult, want string) {
	t.Helper()
	if res == nil {
		t.Fatal("nil tool result")
	}
	if !res.IsError {
		t.Fatalf("expected error result, got success: %s", toolText(res))
	}
	if got := toolText(res); !strings.Contains(got, want) {
		t.Fatalf("error text = %q, want it to contain %q", got, want)
	}
}

// toolText extracts the concatenated text content of a tool result.
func toolText(res *mcplib.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------
// addSplitTools
// ---------------------------------------------------------------------

func TestAddSplitTools_RegistersBothTools(t *testing.T) {
	srv := mcpserver.NewMCPServer("test", "0.0.1", mcpserver.WithToolCapabilities(false))
	addSplitTools(srv, newSplitTools(t, &fakeSessionReader{}, &fakePlatformAdapter{}))

	res := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshalling tools/list response: %v", err)
	}
	for _, name := range []string{"new_session", "await_session_result"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Fatalf("tools/list %s does not advertise %q", raw, name)
		}
	}
}

// ---------------------------------------------------------------------
// inheritedRules
// ---------------------------------------------------------------------

func TestInheritedRules(t *testing.T) {
	approval := state.ApprovedPermission{PermissionText: "bash", Patterns: []string{"git *"}}
	tests := []struct {
		name      string
		inherit   permissionInheriter
		launcher  *SessionLauncher
		parentID  string
		wantCount int
		wantNote  string
	}{
		{name: "no inheriter", inherit: nil, parentID: "p1"},
		{name: "empty parent session", inherit: &fakeInheriter{on: true}, parentID: ""},
		{
			name:     "setting read fails soft",
			inherit:  &fakeInheriter{onErr: errors.New("db down")},
			parentID: "p1",
			wantNote: "reading setting: db down",
		},
		{name: "inheritance disabled", inherit: &fakeInheriter{on: false}, parentID: "p1"},
		{
			name:     "listing approvals fails soft",
			inherit:  &fakeInheriter{on: true, listErr: errors.New("boom")},
			parentID: "p1",
			wantNote: "building rules: boom",
		},
		{
			name:      "approvals only (no launcher platform)",
			inherit:   &fakeInheriter{on: true, approved: []state.ApprovedPermission{approval}},
			parentID:  "p1",
			wantCount: 1,
		},
		{
			name:    "approvals merged with parent live ruleset",
			inherit: &fakeInheriter{on: true, approved: []state.ApprovedPermission{approval}},
			launcher: &SessionLauncher{platform: &fakePlatformAdapter{
				liveRules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}},
			}},
			parentID:  "p1",
			wantCount: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := &splitTools{inherit: tt.inherit, launcher: tt.launcher, platform: "opencode"}
			rules, count, note := tools.inheritedRules(t.Context(), tt.parentID)
			if note != tt.wantNote {
				t.Fatalf("note = %q, want %q", note, tt.wantNote)
			}
			if count != tt.wantCount {
				t.Fatalf("count = %d, want %d", count, tt.wantCount)
			}
			if len(rules) != tt.wantCount {
				t.Fatalf("len(rules) = %d, want %d", len(rules), tt.wantCount)
			}
		})
	}
}

// ---------------------------------------------------------------------
// handleNewSession error paths
// ---------------------------------------------------------------------

func TestHandleNewSession_ErrorPaths(t *testing.T) {
	tests := []struct {
		name     string
		reader   sessionReader
		platform *fakePlatformAdapter
		store    childResultStore
		args     map[string]any
		want     string
	}{
		{
			name: "missing session_id",
			args: map[string]any{"intent": "do a thing"},
			want: "session_id is required",
		},
		{
			name:  "parent is itself a child",
			store: &scriptedChildStore{get: []scriptedGet{{child: &state.ChildSession{ID: "p1"}}}},
			args:  map[string]any{"session_id": "p1", "intent": "do a thing"},
			want:  "new_session is limited to one generation",
		},
		{
			name:  "parent lookup fails",
			store: &scriptedChildStore{get: []scriptedGet{{err: errors.New("disk on fire")}}},
			args:  map[string]any{"session_id": "p1", "intent": "do a thing"},
			want:  "checking parent session: disk on fire",
		},
		{
			name: "missing intent",
			args: map[string]any{"session_id": "p1"},
			want: "intent is required",
		},
		{
			// Compose soft-fails on a missing session, so the handler's
			// own lookup is what rejects the call.
			name: "session lookup after compose fails",
			reader: &flakySessionReader{
				session: &db.Session{ID: "p1", Directory: "/repo"},
				okCalls: 1,
				err:     errors.New("vanished"),
			},
			args: map[string]any{"session_id": "p1", "intent": "do a thing"},
			want: "session not found: vanished",
		},
		{
			name:     "launching child fails",
			platform: &fakePlatformAdapter{createSessionErr: errors.New("unreachable")},
			args:     map[string]any{"session_id": "p1", "intent": "do a thing"},
			want:     "launching child session:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := tt.reader
			if reader == nil {
				reader = &fakeSessionReader{session: &db.Session{ID: "p1", Directory: "/repo"}}
			}
			platform := tt.platform
			if platform == nil {
				platform = &fakePlatformAdapter{}
			}
			tools := newSplitTools(t, reader, platform)
			tools.store = tt.store

			res, err := tools.handleNewSession(context.Background(), splitToolReq(tt.args))
			if err != nil {
				t.Fatalf("handleNewSession returned a transport error: %v", err)
			}
			wantToolError(t, res, tt.want)
		})
	}
}

func TestHandleNewSession_WaitFailureIsReported(t *testing.T) {
	tools := newSplitTools(t,
		&fakeSessionReader{session: &db.Session{ID: "p1", Directory: "/repo"}},
		&fakePlatformAdapter{createSessionID: "child-1"})
	broker := NewChildResultBroker()
	tools.results = broker
	tools.launcher = tools.launcher.WithChildResults(broker)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := tools.handleNewSession(ctx, splitToolReq(map[string]any{
		"session_id": "p1", "intent": "do a thing", "wait": true,
	}))
	if err != nil {
		t.Fatalf("handleNewSession returned a transport error: %v", err)
	}
	wantToolError(t, res, "waiting for child session:")
}

// TestHandleNewSession_AsyncSkipsWait covers the wait=false path: the
// handler returns the child id immediately without touching the broker.
func TestHandleNewSession_AsyncSkipsWait(t *testing.T) {
	tools := newSplitTools(t,
		&fakeSessionReader{session: &db.Session{ID: "p1", Directory: "/repo"}},
		&fakePlatformAdapter{createSessionID: "child-async"})
	broker := NewChildResultBroker()
	tools.results = broker
	tools.launcher = tools.launcher.WithChildResults(broker)
	tools.inherit = &fakeInheriter{on: true, listErr: errors.New("boom")}

	res, err := tools.handleNewSession(context.Background(), splitToolReq(map[string]any{
		"session_id": "p1", "intent": "do a thing", "wait": false,
	}))
	if err != nil {
		t.Fatalf("handleNewSession returned a transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", toolText(res))
	}
	var got struct {
		ChildSessionID string `json:"child_session_id"`
		Status         string `json:"status"`
		InheritError   string `json:"permissionsInheritError"`
	}
	if err := json.Unmarshal([]byte(toolText(res)), &got); err != nil {
		t.Fatalf("decoding result %q: %v", toolText(res), err)
	}
	if got.ChildSessionID != "child-async" || got.Status != "starting" {
		t.Fatalf("result = %+v, want child-async/starting", got)
	}
	if got.InheritError != "building rules: boom" {
		t.Fatalf("permissionsInheritError = %q, want the soft-fail note", got.InheritError)
	}
	if broker.Registered("child-async") {
		t.Fatal("wait=false still registered a synchronous result waiter")
	}
}

// ---------------------------------------------------------------------
// launchWorktree error paths
// ---------------------------------------------------------------------

func TestLaunchWorktree_ErrorPaths(t *testing.T) {
	repoRoot := initTestRepo(t)
	tests := []struct {
		name     string
		reader   sessionReader
		platform *fakePlatformAdapter
		creator  WorktreeSessionCreator
		want     string
	}{
		{
			name:   "session lookup fails",
			reader: &fakeSessionReader{sessErr: errors.New("vanished")},
			want:   "session not found: vanished",
		},
		{
			name:   "no host adapter is wired",
			reader: &fakeSessionReader{session: &db.Session{ID: "p1", Directory: repoRoot}},
			// creator left nil: must fail closed, not fall back to
			// creating a worktree on this machine.
			want: "worktree sessions are unavailable",
		},
		{
			name:   "the owning host refuses",
			reader: &fakeSessionReader{session: &db.Session{ID: "p1", Directory: repoRoot}},
			creator: func(context.Context, WorktreeSessionRequest) (*WorktreeSessionResult, error) {
				return nil, errors.New("branch is checked out elsewhere")
			},
			want: "launching worktree session:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := tt.platform
			if platform == nil {
				platform = &fakePlatformAdapter{}
			}
			tools := newSplitToolsWithCreator(t, tt.reader, platform, tt.creator)
			res, err := tools.launchWorktree(context.Background(),
				splitToolReq(map[string]any{"branch": "feat-x"}),
				"p1", "do a thing", sessionSettings{})
			if err != nil {
				t.Fatalf("launchWorktree returned a transport error: %v", err)
			}
			wantToolError(t, res, tt.want)
		})
	}
}

func TestLaunchWorktree_WaitFailureIsReported(t *testing.T) {
	repoRoot := initTestRepo(t)
	tools := newSplitTools(t,
		&fakeSessionReader{session: &db.Session{ID: "p1", Directory: repoRoot}},
		&fakePlatformAdapter{createSessionID: "child-wt"})
	// The broker is wired to the handler but not to the launcher, so no
	// waiter is registered and the wait fails immediately.
	tools.results = NewChildResultBroker()

	res, err := tools.launchWorktree(context.Background(),
		splitToolReq(map[string]any{"branch": "feat-y"}),
		"p1", "do a thing", sessionSettings{WaitForResult: true})
	if err != nil {
		t.Fatalf("launchWorktree returned a transport error: %v", err)
	}
	wantToolError(t, res, "waiting for child session:")
}

// ---------------------------------------------------------------------
// handleAwaitSessionResult
// ---------------------------------------------------------------------

func TestHandleAwaitSessionResult_ErrorPaths(t *testing.T) {
	terminal := func(delivery string) *state.ChildSession {
		return &state.ChildSession{
			ID: "c1", ParentSessionID: "p1", Status: "completed",
			Summary: "all done", ResultDelivery: delivery,
		}
	}
	running := func(delivery string) *state.ChildSession {
		return &state.ChildSession{
			ID: "c1", ParentSessionID: "p1", Status: "running", ResultDelivery: delivery,
		}
	}

	tests := []struct {
		name       string
		noStore    bool
		store      *scriptedChildStore
		args       map[string]any
		preclaimed bool // register c1 in the broker before the call
		want       string
	}{
		{
			name: "missing session_id",
			args: map[string]any{},
			want: "session_id is required",
		},
		{
			name:    "recovery unavailable without a store",
			noStore: true,
			args:    map[string]any{"session_id": "p1"},
			want:    "child result recovery is unavailable",
		},
		{
			name:  "child lookup fails",
			store: &scriptedChildStore{get: []scriptedGet{{err: errors.New("gone")}}},
			args:  map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want:  "child session not found: gone",
		},
		{
			name: "child belongs to another parent",
			store: &scriptedChildStore{get: []scriptedGet{
				{child: &state.ChildSession{ID: "c1", ParentSessionID: "other"}},
			}},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "child session does not belong to parent",
		},
		{
			name:  "listing disconnected children fails",
			store: &scriptedChildStore{listErr: errors.New("query failed")},
			args:  map[string]any{"session_id": "p1"},
			want:  "finding disconnected child: query failed",
		},
		{
			name:  "no disconnected child",
			store: &scriptedChildStore{},
			args:  map[string]any{"session_id": "p1"},
			want:  "no disconnected child session found",
		},
		{
			name: "multiple disconnected children",
			store: &scriptedChildStore{list: []state.ChildSession{
				*running("disconnected"), *running("disconnected"),
			}},
			args: map[string]any{"session_id": "p1"},
			want: "multiple disconnected child sessions found",
		},
		{
			name:  "still owned by the original call",
			store: &scriptedChildStore{get: []scriptedGet{{child: running("waiting")}}},
			args:  map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want:  "still connected to its original call",
		},
		{
			name:  "legacy detached child",
			store: &scriptedChildStore{get: []scriptedGet{{child: running("detached")}}},
			args:  map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want:  "predates asynchronous result delivery",
		},
		{
			name:  "already delivered",
			store: &scriptedChildStore{get: []scriptedGet{{child: terminal("delivered")}}},
			args:  map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want:  "belongs to asynchronous delivery",
		},
		{
			name: "terminal async_pending claim fails",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: terminal(state.ChildResultAsyncPending)}},
				cas: []scriptedCAS{{err: errors.New("cas broke")}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "claiming child result: cas broke",
		},
		{
			name: "terminal async_pending lost the claim",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: terminal(state.ChildResultAsyncPending)}},
				cas: []scriptedCAS{{claimed: false}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "claimed by asynchronous delivery",
		},
		{
			name: "terminal disconnected lost the claim",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: terminal("disconnected")}},
				cas: []scriptedCAS{{claimed: false}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "claimed by another delivery",
		},
		{
			name: "terminal disconnected claim errors",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: terminal("disconnected")}},
				cas: []scriptedCAS{{err: errors.New("cas broke")}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "marking child result delivered: cas broke",
		},
		{
			name:  "non-terminal in an unrecoverable delivery state",
			store: &scriptedChildStore{get: []scriptedGet{{child: running(state.ChildResultAsyncSending)}}},
			args:  map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want:  "not disconnected",
		},
		{
			name:       "child already has a waiter",
			store:      &scriptedChildStore{get: []scriptedGet{{child: running("disconnected")}}},
			args:       map[string]any{"session_id": "p1", "child_session_id": "c1"},
			preclaimed: true,
			want:       "already has a result waiter",
		},
		{
			name: "reconnect claim errors",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: running("disconnected")}},
				cas: []scriptedCAS{{err: errors.New("cas broke")}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "reconnecting child result: cas broke",
		},
		{
			name: "reconnect lost the claim",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: running("disconnected")}},
				cas: []scriptedCAS{{claimed: false}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "claimed by another delivery",
		},
		{
			name: "refresh after reconnect fails",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: running("disconnected")}, {err: errors.New("gone")}},
				cas: []scriptedCAS{{claimed: true}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "refreshing child session: gone",
		},
		{
			name: "raced to terminal, delivery claim errors",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: running("disconnected")}, {child: terminal("waiting")}},
				cas: []scriptedCAS{{claimed: true}, {err: errors.New("cas broke")}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "marking child result delivered: cas broke",
		},
		{
			name: "raced to terminal, delivery claim lost",
			store: &scriptedChildStore{
				get: []scriptedGet{{child: running("disconnected")}, {child: terminal("waiting")}},
				cas: []scriptedCAS{{claimed: true}, {claimed: false}},
			},
			args: map[string]any{"session_id": "p1", "child_session_id": "c1"},
			want: "claimed by another delivery",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := &splitTools{platform: "opencode", results: NewChildResultBroker()}
			if !tt.noStore {
				tools.store = tt.store
			}
			if tt.preclaimed {
				tools.results.Register("c1")
			}
			res, err := tools.handleAwaitSessionResult(context.Background(), splitToolReq(tt.args))
			if err != nil {
				t.Fatalf("handleAwaitSessionResult returned a transport error: %v", err)
			}
			wantToolError(t, res, tt.want)
		})
	}
}

// TestHandleAwaitSessionResult_TerminalAsyncPendingSucceeds covers the
// happy async_pending claim, including the summary passthrough.
func TestHandleAwaitSessionResult_TerminalAsyncPendingSucceeds(t *testing.T) {
	store := &scriptedChildStore{get: []scriptedGet{{child: &state.ChildSession{
		ID: "c1", ParentSessionID: "p1", Intent: "do a thing", Status: "completed",
		Summary: "all done", ResultDelivery: state.ChildResultAsyncPending,
	}}}}
	tools := &splitTools{platform: "opencode", results: NewChildResultBroker(), store: store}

	res, err := tools.handleAwaitSessionResult(context.Background(), splitToolReq(map[string]any{
		"session_id": "p1", "child_session_id": "c1",
	}))
	if err != nil {
		t.Fatalf("handleAwaitSessionResult returned a transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", toolText(res))
	}
	var got struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(toolText(res)), &got); err != nil {
		t.Fatalf("decoding result %q: %v", toolText(res), err)
	}
	if got.Status != "completed" || got.Summary != "all done" {
		t.Fatalf("result = %+v, want completed/all done", got)
	}
	want := "c1:" + state.ChildResultAsyncPending + "->delivered"
	if len(store.casCalls) != 1 || store.casCalls[0] != want {
		t.Fatalf("delivery transitions = %v, want [%s]", store.casCalls, want)
	}
}

// TestHandleAwaitSessionResult_RacedToTerminalSucceeds covers the
// reconnect path where the child reaches a terminal state between the
// waiting-claim and the refresh, so the result is returned without a wait.
func TestHandleAwaitSessionResult_RacedToTerminalSucceeds(t *testing.T) {
	store := &scriptedChildStore{
		get: []scriptedGet{
			{child: &state.ChildSession{ID: "c1", ParentSessionID: "p1", Status: "running", ResultDelivery: "disconnected"}},
			{child: &state.ChildSession{ID: "c1", ParentSessionID: "p1", Status: "error", Summary: "it broke", ResultDelivery: "waiting"}},
		},
		cas: []scriptedCAS{{claimed: true}, {claimed: true}},
	}
	broker := NewChildResultBroker()
	tools := &splitTools{platform: "opencode", results: broker, store: store}

	res, err := tools.handleAwaitSessionResult(context.Background(), splitToolReq(map[string]any{
		"session_id": "p1", "child_session_id": "c1",
	}))
	if err != nil {
		t.Fatalf("handleAwaitSessionResult returned a transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", toolText(res))
	}
	var got struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(toolText(res)), &got); err != nil {
		t.Fatalf("decoding result %q: %v", toolText(res), err)
	}
	if got.Status != "error" || got.Summary != "it broke" {
		t.Fatalf("result = %+v, want error/it broke", got)
	}
	if broker.Registered("c1") {
		t.Fatal("waiter was left registered after an immediate terminal result")
	}
}

// TestHandleAwaitSessionResult_WaitFailureIsReported covers the tail
// awaitChildResult error branch of the reconnect path.
func TestHandleAwaitSessionResult_WaitFailureIsReported(t *testing.T) {
	running := &state.ChildSession{ID: "c1", ParentSessionID: "p1", Status: "running", ResultDelivery: "disconnected"}
	store := &scriptedChildStore{
		get: []scriptedGet{{child: running}, {child: running}},
		cas: []scriptedCAS{{claimed: true}},
	}
	tools := &splitTools{platform: "opencode", results: NewChildResultBroker(), store: store}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := tools.handleAwaitSessionResult(ctx, splitToolReq(map[string]any{
		"session_id": "p1", "child_session_id": "c1",
	}))
	if err != nil {
		t.Fatalf("handleAwaitSessionResult returned a transport error: %v", err)
	}
	wantToolError(t, res, "waiting for child session:")
}

// TestHandleAwaitSessionResult_FindsSoleDisconnectedChild covers the
// child_session_id-omitted lookup branch.
func TestHandleAwaitSessionResult_FindsSoleDisconnectedChild(t *testing.T) {
	store := &scriptedChildStore{list: []state.ChildSession{{
		ID: "c1", ParentSessionID: "p1", Intent: "do a thing",
		Status: "completed", ResultDelivery: "disconnected",
	}}}
	tools := &splitTools{platform: "opencode", results: NewChildResultBroker(), store: store}

	res, err := tools.handleAwaitSessionResult(context.Background(), splitToolReq(map[string]any{
		"session_id": "p1",
	}))
	if err != nil {
		t.Fatalf("handleAwaitSessionResult returned a transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", toolText(res))
	}
	if !strings.Contains(toolText(res), `"child_session_id": "c1"`) {
		t.Fatalf("result %q does not name the sole disconnected child", toolText(res))
	}
}

// ---------------------------------------------------------------------
// awaitChildResult delivery-claim failures
// ---------------------------------------------------------------------

func TestAwaitChildResult_DeliveryClaimFailures(t *testing.T) {
	tests := []struct {
		name string
		cas  scriptedCAS
		want string
	}{
		{name: "claim errors", cas: scriptedCAS{err: errors.New("cas broke")}, want: "cas broke"},
		{name: "claim lost", cas: scriptedCAS{claimed: false}, want: "lost delivery ownership"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			broker := NewChildResultBroker()
			if !broker.Register("c1") {
				t.Fatal("registering waiter")
			}
			store := &scriptedChildStore{cas: []scriptedCAS{tt.cas}}
			result := map[string]interface{}{}
			done := make(chan error, 1)
			go func() {
				done <- awaitChildResult(context.Background(), mcplib.CallToolRequest{}, "c1", result, broker, store, nil)
			}()
			if !broker.Deliver("c1", ChildResult{Status: "completed", Summary: "ok"}) {
				t.Fatal("delivering result")
			}
			err := <-done
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("await error = %v, want it to contain %q", err, tt.want)
			}
			if _, ok := result["status"]; ok {
				t.Fatalf("result was populated despite a failed delivery claim: %v", result)
			}
		})
	}
}

// ---------------------------------------------------------------------
// progress reporting
// ---------------------------------------------------------------------

func TestStartChildResultProgress_NoopWithoutTokenOrServer(t *testing.T) {
	withMeta := func(m *mcplib.Meta) mcplib.CallToolRequest {
		var req mcplib.CallToolRequest
		req.Params.Meta = m
		return req
	}
	tests := []struct {
		name string
		req  mcplib.CallToolRequest
	}{
		{name: "no meta", req: mcplib.CallToolRequest{}},
		{name: "meta without progress token", req: withMeta(&mcplib.Meta{})},
		{name: "progress token but no server in context", req: withMeta(&mcplib.Meta{ProgressToken: "tok"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stop := startChildResultProgress(context.Background(), tt.req, "c1")
			if stop == nil {
				t.Fatal("startChildResultProgress returned a nil stop func")
			}
			stop()
		})
	}
}

func TestRunChildResultProgress_StopsWithoutNotifying(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	closedDone := make(chan struct{})
	close(closedDone)

	tests := []struct {
		name string
		ctx  context.Context
		done <-chan struct{}
	}{
		{name: "context already cancelled", ctx: cancelled, done: make(chan struct{})},
		{name: "done already closed", ctx: context.Background(), done: closedDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make(chan int, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				runChildResultProgress(tt.ctx, tt.done, time.Millisecond, func(s int) { steps <- s })
			}()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("runChildResultProgress did not return")
			}
			if len(steps) != 0 {
				t.Fatalf("emitted %d progress steps, want none", len(steps))
			}
		})
	}
}

// TestRunChildResultProgress_CancelStopsTickerLoop covers the in-loop
// ctx.Done() branch: the first step is emitted, then cancellation ends it.
func TestRunChildResultProgress_CancelStopsTickerLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	steps := make(chan int, 4)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runChildResultProgress(ctx, make(chan struct{}), time.Hour, func(s int) { steps <- s })
	}()
	select {
	case got := <-steps:
		if got != 1 {
			t.Fatalf("first progress step = %d, want 1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first progress step")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("runChildResultProgress did not stop on cancellation")
	}
}

// ---------------------------------------------------------------------
// argument parsing / result marshalling
// ---------------------------------------------------------------------

func TestParseContextOptions(t *testing.T) {
	all := DefaultContextOptions()
	none := all
	none.RecentMessages, none.RelevantFiles = false, false
	none.GitBranch, none.GitDiffStat, none.ProjectMeta = false, false, false

	tests := []struct {
		name string
		args map[string]any
		want ContextOptions
	}{
		{name: "no arguments", args: nil, want: all},
		{name: "absent key", args: map[string]any{"intent": "x"}, want: all},
		{name: "explicit null", args: map[string]any{"context_options": nil}, want: all},
		{name: "unmarshalable value", args: map[string]any{"context_options": make(chan int)}, want: all},
		{name: "wrong json shape", args: map[string]any{"context_options": "yes please"}, want: all},
		{name: "empty object keeps defaults", args: map[string]any{"context_options": map[string]any{}}, want: all},
		{
			name: "all sources disabled",
			args: map[string]any{"context_options": map[string]any{
				"recent_messages":  false,
				"relevant_files":   false,
				"git_branch":       false,
				"git_diff_stat":    false,
				"project_metadata": false,
			}},
			want: none,
		},
		{
			name: "partial override",
			args: map[string]any{"context_options": map[string]any{"git_diff_stat": false}},
			want: func() ContextOptions { o := all; o.GitDiffStat = false; return o }(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseContextOptions(splitToolReq(tt.args)); got != tt.want {
				t.Fatalf("parseContextOptions = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParsePermissionRules(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want []platforms.PermissionRule
	}{
		{name: "absent key", args: nil},
		{name: "explicit null", args: map[string]any{"permission": nil}},
		{name: "unmarshalable value", args: map[string]any{"permission": make(chan int)}},
		{name: "wrong json shape", args: map[string]any{"permission": "allow everything"}},
		{
			name: "typed rules",
			args: map[string]any{"permission": []any{
				map[string]any{"permission": "bash", "pattern": "git *", "action": "allow"},
			}},
			want: []platforms.PermissionRule{{Permission: "bash", Pattern: "git *", Action: "allow"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePermissionRules(splitToolReq(tt.args))
			if len(got) != len(tt.want) {
				t.Fatalf("parsePermissionRules = %+v, want %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("rule %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestAddInheritanceResult(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		errNote   string
		wantOn    bool
		wantNote  bool
		wantCount int
	}{
		{name: "nothing inherited", count: 0},
		{name: "rules inherited", count: 3, wantOn: true, wantCount: 3},
		{name: "soft failure note", count: 0, errNote: "reading setting: boom", wantNote: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := map[string]interface{}{}
			addInheritanceResult(result, tt.count, tt.errNote)
			if result["permissionsInherited"] != tt.wantOn {
				t.Fatalf("permissionsInherited = %v, want %v", result["permissionsInherited"], tt.wantOn)
			}
			if result["permissionsInheritedCount"] != tt.count {
				t.Fatalf("permissionsInheritedCount = %v, want %d", result["permissionsInheritedCount"], tt.count)
			}
			note, ok := result["permissionsInheritError"]
			if ok != tt.wantNote {
				t.Fatalf("permissionsInheritError present = %v, want %v", ok, tt.wantNote)
			}
			if tt.wantNote && note != tt.errNote {
				t.Fatalf("permissionsInheritError = %v, want %q", note, tt.errNote)
			}
		})
	}
}

func TestToolResultJSON_FallsBackOnMarshalError(t *testing.T) {
	res := toolResultJSON(map[string]interface{}{"bad": make(chan int)})
	if res.IsError {
		t.Fatalf("expected a text result, got an error result: %s", toolText(res))
	}
	if toolText(res) == "" {
		t.Fatal("expected a fallback string representation, got empty text")
	}
}

// TestParentModel_SkipsUnmarshalableLatestMessage covers the
// json.Unmarshal continue branch: the newest row is not valid JSON, so
// the scan falls through to the older model-bearing one.
func TestParentModel_SkipsUnmarshalableLatestMessage(t *testing.T) {
	tools := &splitTools{composer: &PromptComposer{db: &fakeSessionReader{messages: []db.Message{
		{Data: json.RawMessage(`{"providerID":"openai","modelID":"gpt-5"}`)},
		{Data: json.RawMessage(`not json`)},
	}}}}
	if got := tools.parentModel(t.Context(), "p1"); got != "openai/gpt-5" {
		t.Fatalf("parentModel = %q, want openai/gpt-5", got)
	}
}
