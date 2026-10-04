package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func newTestAdapter() *Adapter {
	return &Adapter{prompts: newLivePromptRegistry(), turns: newLiveStatusRegistry()}
}

func TestTurnRunning(t *testing.T) {
	cases := map[string]bool{
		"busy":    true,
		"retry":   true, // provider backoff *within* a turn
		"idle":    false,
		"":        false,
		"unknown": false, // a type a future OpenCode might add
	}
	for statusType, want := range cases {
		if got := turnRunning(statusType); got != want {
			t.Errorf("turnRunning(%q) = %v, want %v", statusType, got, want)
		}
	}
}

func TestTurnState_UnobservedWithoutLiveInstance(t *testing.T) {
	a := newTestAdapter()
	a.ObserveSessionStatus("7777", 0, "s1", "busy")
	// No port serves /work, so nothing observed can apply to it.
	if got := a.turns.turnState("s1", "/work", nil); got != db.TurnUnobserved {
		t.Errorf("turnState with no live port = %v, want TurnUnobserved", got)
	}
}

func TestTurnState_RunningAndSettled(t *testing.T) {
	a := newTestAdapter()
	ports := map[string]string{"/work": "7777"}

	// Before anything is known about the port, absence proves nothing.
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnUnobserved {
		t.Fatalf("turnState before seeding = %v, want TurnUnobserved", got)
	}

	a.SeedSessionStatus("7777", 0, 0, map[string]string{"s1": "busy"})
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnRunning {
		t.Errorf("turnState after busy seed = %v, want TurnRunning", got)
	}
	// A session the seeded instance doesn't list isn't running.
	if got := a.turns.turnState("s2", "/work", ports); got != db.TurnSettled {
		t.Errorf("turnState for unlisted session = %v, want TurnSettled", got)
	}

	a.ObserveSessionStatus("7777", 0, "s1", "idle")
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnSettled {
		t.Errorf("turnState after idle event = %v, want TurnSettled", got)
	}
}

// A new snapshot replaces the port's whole view: a session that was busy
// and is absent from the new snapshot must not stay pinned busy.
func TestSeedSessionStatus_ReplacesPortView(t *testing.T) {
	a := newTestAdapter()
	ports := map[string]string{"/work": "7777"}
	a.SeedSessionStatus("7777", 0, 0, map[string]string{"s1": "busy"})
	a.SeedSessionStatus("7777", 0, 0, map[string]string{})
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnSettled {
		t.Errorf("turnState after empty re-seed = %v, want TurnSettled", got)
	}
}

// Entries are per-port so one instance's snapshot can't erase another's.
func TestSeedSessionStatus_IsolatesPorts(t *testing.T) {
	a := newTestAdapter()
	ports := map[string]string{"/a": "7777", "/b": "8888"}
	a.SeedSessionStatus("7777", 0, 0, map[string]string{"s1": "busy"})
	a.SeedSessionStatus("8888", 0, 0, map[string]string{"s2": "busy"})
	a.SeedSessionStatus("8888", 0, 0, map[string]string{})

	if got := a.turns.turnState("s1", "/a", ports); got != db.TurnRunning {
		t.Errorf("port 7777's session = %v, want TurnRunning", got)
	}
	if got := a.turns.turnState("s2", "/b", ports); got != db.TurnSettled {
		t.Errorf("port 8888's session = %v, want TurnSettled", got)
	}
}

// A departing instance takes its knowledge with it: its sessions become
// unobservable, which is what settles an unfinished turn as interrupted.
func TestClearSessionStatusForPort(t *testing.T) {
	a := newTestAdapter()
	ports := map[string]string{"/work": "7777"}
	stale := a.StatusPortGeneration("7777")
	a.SeedSessionStatus("7777", stale, 0, map[string]string{"s1": "busy"})
	a.ClearSessionStatusForPort("7777")

	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnUnobserved {
		t.Errorf("turnState after clear = %v, want TurnUnobserved", got)
	}
	// The generation bump must reject a late event from the dead stream.
	a.ObserveSessionStatus("7777", stale, "s1", "busy")
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnUnobserved {
		t.Errorf("stale-generation event was accepted: %v", got)
	}
}

func TestStatusPortGeneration_RejectsStaleSeed(t *testing.T) {
	a := newTestAdapter()
	ports := map[string]string{"/work": "7777"}
	generation := a.StatusPortGeneration("7777")
	a.ClearSessionStatusForPort("7777") // bumps the generation
	a.SeedSessionStatus("7777", generation, 0, map[string]string{"s1": "busy"})
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnUnobserved {
		t.Errorf("stale-generation seed was accepted: %v", got)
	}
}

// A worktree session runs on the project instance, so its own directory is
// never a key in the port map.
func TestPortForDirectory_FoldsWorktreeToProjectRoot(t *testing.T) {
	ports := map[string]string{"/src/repo": "7777"}
	worktree := "/src/.worktrees/repo/feature"
	if got := portForDirectory(ports, worktree); got != "7777" {
		t.Errorf("portForDirectory(%q) = %q, want 7777", worktree, got)
	}
	if got := portForDirectory(ports, "/elsewhere"); got != "" {
		t.Errorf("portForDirectory for unserved directory = %q, want empty", got)
	}
}

func TestSettleStatus_NilRegistryLeavesInferenceAlone(t *testing.T) {
	var a *Adapter
	if got := a.settleStatus("s1", "/work", db.StatusBusy, nil); got != db.StatusBusy {
		t.Errorf("nil adapter settleStatus = %v, want the inference back", got)
	}
	bare := &Adapter{}
	if got := bare.settleStatus("s1", "/work", db.StatusBusy, nil); got != db.StatusBusy {
		t.Errorf("nil registry settleStatus = %v, want the inference back", got)
	}
}

// The end-to-end shape of the fix: an unfinished turn on a directory with
// no live instance is interrupted, not busy.
func TestSettleStatus_DeadInstanceInterruptsUnfinishedTurn(t *testing.T) {
	a := newTestAdapter()
	if got := a.settleStatus("s1", "/work", db.StatusBusy, nil); got != db.StatusInterrupted {
		t.Errorf("settleStatus with no live instance = %v, want interrupted", got)
	}
	// With the instance up and running the turn, it stays busy.
	ports := map[string]string{"/work": "7777"}
	a.SeedSessionStatus("7777", 0, 0, map[string]string{"s1": "busy"})
	if got := a.settleStatus("s1", "/work", db.StatusBusy, ports); got != db.StatusBusy {
		t.Errorf("settleStatus for a running turn = %v, want busy", got)
	}
}

func TestSeedSessionStatusFromInstance(t *testing.T) {
	fake := newOpencodeFake(t)
	fake.turnStatus = map[string]string{"s1": "busy", "s2": "idle"}

	a := newTestAdapter()
	port := fake.Port()
	if !a.SeedSessionStatusFromInstance(context.Background(), port, 0, nil) {
		t.Fatal("SeedSessionStatusFromInstance reported failure")
	}
	ports := map[string]string{"/work": port}
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnRunning {
		t.Errorf("seeded busy session = %v, want TurnRunning", got)
	}
	if got := a.turns.turnState("s2", "/work", ports); got != db.TurnSettled {
		t.Errorf("seeded idle session = %v, want TurnSettled", got)
	}
}

// OpenCode scopes /session/status per directory, so the unscoped read on a
// project instance never lists its worktree sessions. Seeding from it alone
// marked the port seeded and settled a busy worktree session (e.g. a `!`
// shell still running) as done.
//
// A busy row in a deleted worktree must not be read: OpenCode would boot
// state for it, and its failed read would block the whole seed.
func TestSeedSessionStatusFromInstance_IncludesWorktreeDirectories(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	worktree := filepath.Join(base, ".worktrees", "project", "feature")
	deleted := filepath.Join(base, ".worktrees", "project", "deleted")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	ResetCachesForTests()
	fake := newOpencodeFake(t)
	fake.turnStatus = map[string]string{}
	fake.turnStatusByDir = map[string]map[string]string{
		root:     {},
		worktree: {"ses-worktree": "busy"},
	}
	a := New(newTestDBWithSessions(t, []testSession{
		{id: "ses-root", directory: root},
		{id: "ses-worktree", directory: worktree, busy: true},
		{id: "ses-deleted", directory: deleted, busy: true},
	}), nil)
	port := fake.Port()
	if !a.SeedSessionStatusFromInstance(context.Background(), port, 0, []string{root}) {
		t.Fatal("SeedSessionStatusFromInstance reported failure")
	}
	if got := a.turns.turnStateForPort("ses-worktree", port); got != db.TurnRunning {
		t.Errorf("worktree session turnState = %v, want TurnRunning", got)
	}
	fake.mu.Lock()
	if slices.Contains(fake.dirHits, deleted) {
		t.Errorf("queried deleted worktree %q", deleted)
	}
	fake.mu.Unlock()

	// A failed worktree read must leave the port unseeded rather than
	// settle the sessions it could not see.
	fake.turnStatusDirCode = 500
	b := New(newTestDBWithSessions(t, []testSession{
		{id: "ses-worktree", directory: worktree, busy: true},
	}), nil)
	ResetCachesForTests()
	if b.SeedSessionStatusFromInstance(context.Background(), port, 0, []string{root}) {
		t.Fatal("SeedSessionStatusFromInstance reported success with a failed directory read")
	}
	if got := b.turns.turnStateForPort("ses-worktree", port); got != db.TurnUnobserved {
		t.Errorf("turnState after failed directory read = %v, want TurnUnobserved", got)
	}
}

// After a restart the registry is cold, and a just-started worktree turn
// infers as "done" (its last message is the user's prompt). It must still get
// a scoped read; a long-idle or deleted worktree must not, since OpenCode
// boots an instance for every directory it is asked about.
func TestSeedSessionStatusFromInstance_ReadsRecentWorktreeWithSettledStoredStatus(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	active := filepath.Join(base, ".worktrees", "project", "active")
	stale := filepath.Join(base, ".worktrees", "project", "stale")
	deleted := filepath.Join(base, ".worktrees", "project", "deleted")
	for _, dir := range []string{root, active, stale} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ResetCachesForTests()
	fake := newOpencodeFake(t)
	fake.turnStatus = map[string]string{}
	fake.turnStatusByDir = map[string]map[string]string{active: {"ses-active": "busy"}}
	now := time.Now().UnixMilli()
	userLast := `{"role":"user"}`
	a := New(newTestDBWithSessions(t, []testSession{
		{id: "ses-root", directory: root, updated: now},
		{id: "ses-active", directory: active, messageData: userLast, updated: now},
		{id: "ses-stale", directory: stale, messageData: userLast},
		{id: "ses-deleted", directory: deleted, messageData: userLast, updated: now},
	}), nil)
	port := fake.Port()
	if !a.SeedSessionStatusFromInstance(context.Background(), port, 0, []string{root}) {
		t.Fatal("SeedSessionStatusFromInstance reported failure")
	}
	if got := a.turns.turnStateForPort("ses-active", port); got != db.TurnRunning {
		t.Errorf("recent worktree session turnState = %v, want TurnRunning", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, hit := range fake.dirHits {
		if hit == stale || hit == deleted {
			t.Errorf("queried inactive worktree %q", hit)
		}
	}
}

// A warm sessions snapshot can predate a turn started while the watcher was
// disconnected: it still shows the worktree's row as old and settled. The
// window must come from the database, not that snapshot.
func TestSeedSessionStatusFromInstance_IgnoresStaleSessionsCache(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	worktree := filepath.Join(base, ".worktrees", "project", "feature")
	for _, dir := range []string{root, worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ResetCachesForTests()
	stale := newTestDBWithSessions(t, []testSession{
		{id: "ses-worktree", directory: worktree, messageData: `{"role":"assistant","finish":"stop"}`},
	})
	if _, err := getSessionsCached(context.Background(), stale, "", 0); err != nil {
		t.Fatal(err)
	}
	fresh := newTestDBWithSessions(t, []testSession{
		{id: "ses-worktree", directory: worktree, busy: true, updated: time.Now().UnixMilli()},
	})
	fake := newOpencodeFake(t)
	fake.turnStatus = map[string]string{}
	fake.turnStatusByDir = map[string]map[string]string{worktree: {"ses-worktree": "busy"}}
	a := New(fresh, nil)
	port := fake.Port()
	if !a.SeedSessionStatusFromInstance(context.Background(), port, 0, []string{root}) {
		t.Fatal("SeedSessionStatusFromInstance reported failure")
	}
	if got := a.turns.turnStateForPort("ses-worktree", port); got != db.TurnRunning {
		t.Errorf("worktree turnState with stale cache = %v, want TurnRunning", got)
	}
}

// A turn blocked for over a day (e.g. on a permission after a tool-call step)
// has an old session and a last message with finish "tool-calls", which
// infers as a terminal state. OpenCode treats that finish as non-terminal, so
// the directory must still be read, or the session reads waiting while busy.
func TestSeedSessionStatusFromInstance_ReadsOldWorktreeWithOpenToolCallTurn(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	worktree := filepath.Join(base, ".worktrees", "project", "blocked")
	for _, dir := range []string{root, worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ResetCachesForTests()
	fake := newOpencodeFake(t)
	fake.turnStatus = map[string]string{}
	fake.turnStatusByDir = map[string]map[string]string{worktree: {"ses-blocked": "busy"}}
	a := New(newTestDBWithSessions(t, []testSession{
		{id: "ses-blocked", directory: worktree, messageData: `{"role":"assistant","finish":"tool-calls"}`},
	}), nil)
	port := fake.Port()
	if !a.SeedSessionStatusFromInstance(context.Background(), port, 0, []string{root}) {
		t.Fatal("SeedSessionStatusFromInstance reported failure")
	}
	if got := a.turns.turnStateForPort("ses-blocked", port); got != db.TurnRunning {
		t.Errorf("old open-turn worktree turnState = %v, want TurnRunning", got)
	}
}

// If the session list cannot be read, the worktree candidates are unknown;
// seeding from the root read alone would settle busy worktree sessions.
func TestSeedSessionStatusFromInstance_SessionListFailureLeavesPortUnseeded(t *testing.T) {
	ResetCachesForTests()
	fake := newOpencodeFake(t)
	fake.turnStatus = map[string]string{}
	database := newTestDBWithSessions(t, nil)
	a := New(database, nil)
	_ = database.Close()
	port := fake.Port()
	if a.SeedSessionStatusFromInstance(context.Background(), port, 0, []string{"/repo/project"}) {
		t.Fatal("SeedSessionStatusFromInstance reported success without a session list")
	}
	if got := a.turns.turnStateForPort("ses-worktree", port); got != db.TurnUnobserved {
		t.Errorf("turnState after failed session list = %v, want TurnUnobserved", got)
	}
}

// A failed snapshot must not mark the port seeded: treating every session
// on a wedged instance as settled would report finished turns as done.
// An event that lands while the snapshot is in flight describes a newer
// transition than the snapshot, so it must survive the seed.
func TestSeedSessionStatus_DoesNotClobberNewerEvent(t *testing.T) {
	a := newTestAdapter()
	ports := map[string]string{"/work": "7777"}
	seq := a.statusSeq()
	// The stream reports the turn starting while the snapshot is in flight.
	a.ObserveSessionStatus("7777", 0, "s1", "busy")
	// The snapshot, taken before that event, still shows nothing running.
	a.SeedSessionStatus("7777", 0, seq, map[string]string{})

	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnRunning {
		t.Errorf("turnState = %v, want TurnRunning (the event is newer than the snapshot)", got)
	}
	// A later snapshot, taken after the event, is authoritative again.
	a.SeedSessionStatus("7777", 0, a.statusSeq(), map[string]string{})
	if got := a.turns.turnState("s1", "/work", ports); got != db.TurnSettled {
		t.Errorf("turnState after a fresh snapshot = %v, want TurnSettled", got)
	}
}

func TestSeedSessionStatusFromInstance_FailureLeavesPortUnseeded(t *testing.T) {
	fake := newOpencodeFake(t)
	fake.turnStatusCode = 500

	a := newTestAdapter()
	port := fake.Port()
	if a.SeedSessionStatusFromInstance(context.Background(), port, 0, nil) {
		t.Fatal("SeedSessionStatusFromInstance reported success on HTTP 500")
	}
	if got := a.turns.turnState("s1", "/work", map[string]string{"/work": port}); got != db.TurnUnobserved {
		t.Errorf("turnState after failed seed = %v, want TurnUnobserved", got)
	}
}

// --- Sessions() overlay ---

// A child session whose turn has just started has the user's prompt as its
// last message, which infers as "done" — the exact lag that used to hide it
// from the list. The live turn signal keeps it visible.
func TestSessions_LiveSignalKeepsJustStartedChildVisible(t *testing.T) {
	const dir = "/repo/main"
	parentID := "ses-parent"
	database := newTestDBWithSessions(t, []testSession{
		{id: parentID, directory: dir},
		{id: "ses-child", directory: dir, parentID: &parentID},
	})
	fake := newOpencodeFake(t)
	withTestPort(t, dir, fake.Port())
	InvalidateSessionsCache()

	a := New(database, nil)
	// Without a live signal the child is inferred done and filtered out.
	a.SeedSessionStatus(fake.Port(), 0, 0, map[string]string{})
	sessions, err := a.Sessions(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != parentID {
		t.Fatalf("settled child should be filtered out, got %+v", sessionIDs(sessions))
	}

	// The instance reports the child's turn running: it must reappear.
	a.SeedSessionStatus(fake.Port(), 0, 0, map[string]string{"ses-child": "busy"})
	sessions, err = a.Sessions(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	child, ok := sessionByID(sessions, "ses-child")
	if !ok {
		t.Fatalf("running child missing from listing, got %+v", sessionIDs(sessions))
	}
	if child.Status != db.StatusBusy {
		t.Errorf("child status = %q, want busy", child.Status)
	}
}

// The stale-spinner case: an unfinished assistant message infers as busy,
// but the instance says the turn is over.
func TestSessions_SettledInstanceEndsStaleBusy(t *testing.T) {
	const dir = "/repo/main"
	database := newTestDBWithSessions(t, []testSession{{id: "ses-1", directory: dir, busy: true}})
	fake := newOpencodeFake(t)
	withTestPort(t, dir, fake.Port())
	InvalidateSessionsCache()

	a := New(database, nil)
	a.SeedSessionStatus(fake.Port(), 0, 0, map[string]string{})
	sessions, err := a.Sessions(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Status != db.StatusDone {
		t.Errorf("status = %q, want done (the instance says the turn ended)", sessions[0].Status)
	}
}

func TestSessionStatusOnPortSettlesIdleWithoutListCache(t *testing.T) {
	database := newTestDBWithSessions(t, []testSession{{id: "ses-1", directory: "/repo/main", busy: true}})
	a := New(database, nil)
	a.ObserveSessionStatus("7777", 0, "ses-1", "idle")

	status, err := a.SessionStatusOnPort("ses-1", "7777")
	if err != nil {
		t.Fatalf("SessionStatusOnPort: %v", err)
	}
	if status != db.StatusDone {
		t.Fatalf("status = %q, want done", status)
	}
}

// A session whose agent process is gone mid-turn must settle as interrupted
// rather than spin forever.
func TestSessions_DeadInstanceReportsInterrupted(t *testing.T) {
	const dir = "/repo/main"
	database := newTestDBWithSessions(t, []testSession{{id: "ses-1", directory: dir, busy: true}})
	// No port serves dir: nothing is running.
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{} })
	resetPortCacheForTests()
	t.Cleanup(func() {
		restore()
		resetPortCacheForTests()
	})
	InvalidateSessionsCache()

	a := New(database, nil)
	sessions, err := a.Sessions(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Status != db.StatusInterrupted {
		t.Errorf("status = %q, want interrupted", sessions[0].Status)
	}
	if sessions[0].LiveConnection {
		t.Error("LiveConnection should be false with no instance")
	}
}

// A running instance whose conversation can't be fetched (e.g. a body
// over maxUpstreamConversationBytes) falls back to the DB, but the
// session is still live: reporting it as disconnected leaves the UI
// stuck on "OpenCode is not running" right after a successful launch.
func TestSession_DBFallbackStillReportsLiveInstance(t *testing.T) {
	const dir = "/repo/main"
	database := newTestDBWithSession(t, "ses-1", dir)
	fake := newOpencodeFake(t)
	fake.SetSession("ses-1", json.RawMessage(`{"id":"ses-1","directory":"/repo/main"}`))
	fake.messagesStatus = http.StatusInternalServerError
	withTestPort(t, dir, fake.Port())

	detail, err := New(database, nil).Session(context.Background(), "ses-1", 0, 0)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !detail.Session.LiveConnection {
		t.Error("LiveConnection should be true while an instance serves the directory")
	}
}

func sessionIDs(sessions []db.Session) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID)
	}
	return out
}

func sessionByID(sessions []db.Session, id string) (db.Session, bool) {
	for _, s := range sessions {
		if s.ID == id {
			return s, true
		}
	}
	return db.Session{}, false
}
