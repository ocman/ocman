package codexprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

type fakeHistory struct {
	lists    map[bool][]codexappserver.Thread
	listErr  error
	threads  []*codexappserver.Thread // successive thread/read results
	reads    int
	readErr  error
	turns    []codexappserver.Turn
	turnsErr error
	items    []codexappserver.ItemEntry
	itemsErr error
	onTurns  func()
}

func (f *fakeHistory) ListThreads(_ context.Context, p codexappserver.ThreadListParams) ([]codexappserver.Thread, error) {
	if len(p.SourceKinds) == 0 || p.Archived == nil {
		return nil, errors.New("inventory must ask for every source and archive state")
	}
	return f.lists[*p.Archived], f.listErr
}

func (f *fakeHistory) ReadThread(_ context.Context, _ string, turns bool) (*codexappserver.Thread, error) {
	if turns {
		return nil, errors.New("full-history reads are deprecated")
	}
	if f.readErr != nil {
		return nil, f.readErr
	}
	t := f.threads[min(f.reads, len(f.threads)-1)]
	f.reads++
	return t, nil
}

func (f *fakeHistory) ListTurns(_ context.Context, _, view string) ([]codexappserver.Turn, error) {
	if view != "notLoaded" {
		return nil, errors.New("turn items are read separately")
	}
	if f.onTurns != nil {
		f.onTurns()
	}
	return f.turns, f.turnsErr
}

func (f *fakeHistory) ListItems(context.Context, string) ([]codexappserver.ItemEntry, error) {
	return f.items, f.itemsErr
}

func recorded(t *testing.T) *fakeHistory {
	t.Helper()
	const dir = "testdata/0.164.0/"
	thread := load[codexappserver.ThreadReadResponse](t, dir+"thread-read.json").Thread
	return &fakeHistory{threads: []*codexappserver.Thread{&thread},
		turns: load[struct{ Data []codexappserver.Turn }](t, dir+"turns.json").Data,
		items: load[struct{ Data []codexappserver.ItemEntry }](t, dir+"items.json").Data}
}

var notLoaded = &codexappserver.RPCError{Code: -32600, Message: "thread not loaded: x"}

func TestInventory(t *testing.T) {
	f := &fakeHistory{lists: map[bool][]codexappserver.Thread{
		false: {{ID: "a", UpdatedAt: 2}, {ID: "eph", Ephemeral: true}},
		true:  {{ID: "b", UpdatedAt: 1, Name: "n"}, {ID: "a", UpdatedAt: 2}},
	}}
	got, err := (&HistoryReader{Client: f}).Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (db.HistoryEntry{ID: "a", Version: "2"}) || got[1] != (db.HistoryEntry{ID: "b", Version: "1:n"}) {
		t.Fatalf("inventory = %+v", got)
	}
	f.listErr = errors.New("db")
	if _, err := (&HistoryReader{Client: f}).Inventory(context.Background()); err == nil {
		t.Fatal("a failed listing returned an inventory")
	}
}

func TestSnapshotRecorded(t *testing.T) {
	f := recorded(t)
	snap, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), f.threads[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	s := snap.Session
	if s.ID != f.threads[0].ID || s.Platform != "codex" || s.Directory != "/fixture/repo" || s.Title != "Recorded fixture" ||
		s.TimeCreated != f.threads[0].CreatedAt*1000 {
		t.Fatalf("session = %+v", s)
	}
	// The last turn was interrupted: its reply carries an abort error.
	if snap.InferredStatus != db.StatusError || len(snap.Messages) != 4 || snap.Subagent || snap.Partial {
		t.Fatalf("snapshot = status %q, %d messages, subagent %v", snap.InferredStatus, len(snap.Messages), snap.Subagent)
	}
	if snap.Version != threadVersion(f.threads[0]) {
		t.Fatalf("version = %q", snap.Version)
	}
	meta, err := (&HistoryReader{Client: f}).SessionIdentityMetadata(context.Background(), s.ID)
	if err != nil || meta.Title != "Recorded fixture" {
		t.Fatalf("metadata = %+v, %v", meta, err)
	}
}

func TestSnapshotChangedDuringRead(t *testing.T) {
	f := recorded(t)
	later := *f.threads[0]
	later.UpdatedAt++
	f.threads = append(f.threads, &later)
	if _, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), later.ID); !errors.Is(err, db.ErrHistoryChanged) {
		t.Fatalf("err = %v", err)
	}
}

// The rollout grows while turns are listed but the second-resolution update
// time does not move: the version sampled before the content reads differs.
func TestSnapshotRolloutChangedDuringListing(t *testing.T) {
	f := recorded(t)
	th := *f.threads[0]
	th.Path = filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(th.Path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.threads = []*codexappserver.Thread{&th}
	f.onTurns = func() {
		if err := os.WriteFile(th.Path, []byte("a\nb\n"), 0o600); err != nil {
			t.Error(err)
		}
	}
	if _, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), th.ID); !errors.Is(err, db.ErrHistoryChanged) {
		t.Fatalf("err = %v", err)
	}
}

// The handwritten thread ends with a failed turn holding only its prompt.
func TestSnapshotUserOnlyFailedTurn(t *testing.T) {
	th := load[codexappserver.Thread](t, "testdata/handwritten/thread.json")
	f := &fakeHistory{threads: []*codexappserver.Thread{&th}}
	for _, turn := range th.Turns {
		for _, it := range turn.Items {
			f.items = append(f.items, codexappserver.ItemEntry{TurnID: turn.ID, Item: it})
		}
		turn.Items = nil
		f.turns = append(f.turns, turn)
	}
	snap, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.InferredStatus != db.StatusError {
		t.Fatalf("status = %q", snap.InferredStatus)
	}
	last := decodeMap(t, snap.Messages[len(snap.Messages)-1].Data)
	if last["error"].(map[string]any)["data"].(map[string]any)["message"] != "usage limit reached" {
		t.Fatalf("last message = %v", last)
	}
}

func TestSnapshotErrors(t *testing.T) {
	other := errors.New("transport")
	cases := []struct {
		name string
		edit func(*fakeHistory)
		want error
	}{
		{"missing thread", func(f *fakeHistory) { f.readErr = notLoaded }, platforms.ErrNotFound},
		{"invalid id", func(f *fakeHistory) {
			f.readErr = &codexappserver.RPCError{Code: -32600, Message: "invalid thread id: bad"}
		}, platforms.ErrNotFound},
		{"other invalid request", func(f *fakeHistory) {
			f.readErr = &codexappserver.RPCError{Code: -32600, Message: "server shutting down"}
		}, nil},
		{"read failure", func(f *fakeHistory) { f.readErr = other }, other},
		{"turns vanish", func(f *fakeHistory) { f.turnsErr = notLoaded }, platforms.ErrNotFound},
		{"items fail", func(f *fakeHistory) { f.itemsErr = other }, other},
	}
	for _, tc := range cases {
		f := recorded(t)
		tc.edit(f)
		_, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), "x")
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || (tc.want == nil && errors.Is(err, platforms.ErrNotFound)) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	f := recorded(t)
	f.readErr = notLoaded
	if _, err := (&HistoryReader{Client: f}).SessionIdentityMetadata(context.Background(), "x"); !errors.Is(err, platforms.ErrNotFound) {
		t.Errorf("metadata err = %v", err)
	}
	// The second read failing after a good first read is still an error.
	f = recorded(t)
	f.threads = []*codexappserver.Thread{f.threads[0]}
	reader := &HistoryReader{Client: &failSecondRead{fakeHistory: f}}
	if _, err := reader.Snapshot(context.Background(), "x"); !errors.Is(err, platforms.ErrNotFound) {
		t.Errorf("second read err = %v", err)
	}
}

type failSecondRead struct{ *fakeHistory }

func (f *failSecondRead) ReadThread(ctx context.Context, id string, turns bool) (*codexappserver.Thread, error) {
	if f.reads > 0 {
		return nil, notLoaded
	}
	return f.fakeHistory.ReadThread(ctx, id, turns)
}

func TestThreadVersionFollowsRollout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	th := &codexappserver.Thread{UpdatedAt: 7, Path: path}
	v1 := threadVersion(th)
	if !strings.HasPrefix(v1, "7:2:") {
		t.Fatalf("version = %q", v1)
	}
	// A write within the same second still changes the version.
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if threadVersion(th) == v1 {
		t.Fatal("version did not change with the rollout")
	}
	th.Path = filepath.Join(t.TempDir(), "gone.jsonl")
	if v := threadVersion(th); v != "7" {
		t.Fatalf("missing rollout version = %q", v)
	}
}

func TestSessionAndTitle(t *testing.T) {
	th := &codexappserver.Thread{ID: "t", ParentThreadID: "p", Preview: "  first line\nsecond", Cwd: "/r",
		CreatedAt: 1, UpdatedAt: 2, GitInfo: &codexappserver.GitInfo{Branch: "main"}}
	s := Session(th)
	if s.ParentID != "p" || s.Title != "first line" || s.Branch != "main" || s.TimeUpdated != 2000 {
		t.Fatalf("session = %+v", s)
	}
	th.Preview = strings.Repeat("é", 100)
	if r := []rune(Title(th)); len(r) != 80 || r[79] != '…' {
		t.Fatalf("long title = %q", Title(th))
	}
	if inferStatus(nil) != db.InferStoredSessionStatus("", "", "") {
		t.Fatal("empty thread status")
	}
}

func TestSubagentSnapshot(t *testing.T) {
	f := recorded(t)
	child := *f.threads[0]
	if err := child.Source.UnmarshalJSON([]byte(`{"subAgent":{}}`)); err != nil {
		t.Fatal(err)
	}
	f.threads = []*codexappserver.Thread{&child}
	snap, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), child.ID)
	if err != nil || !snap.Subagent {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
}
