package db

import (
	"reflect"
	"sort"
	"testing"
)

func TestUnfinishedTurnDirectoriesMatchesInferredBusyWorktrees(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	sessions := map[string]struct {
		dir    string
		latest map[string]any
	}{
		"user":       {"/r/.worktrees/p/user", map[string]any{"role": "user"}},
		"open":       {"/r/.worktrees/p/open", map[string]any{"role": "assistant"}},
		"tools":      {"/r/.worktrees/p/tools", map[string]any{"role": "assistant", "finish": "tool-calls"}},
		"done":       {"/r/.worktrees/p/done", map[string]any{"role": "assistant", "finish": "stop"}},
		"error":      {"/r/.worktrees/p/error", map[string]any{"role": "assistant", "error": map[string]any{"name": "APIError"}}},
		"root":       {"/r/project", map[string]any{"role": "user"}},
		"superseded": {"/r/.worktrees/p/old", map[string]any{"role": "assistant", "finish": "stop"}},
	}
	for id, s := range sessions {
		insertSession(t, d, id, id, s.dir, 1, 1)
		insertMessage(t, d, id+"-latest", id, 10, s.latest)
	}
	// Only the latest message counts: an earlier open turn was answered.
	insertMessage(t, d, "superseded-earlier", "superseded", 5, map[string]any{"role": "assistant"})
	got, err := d.UnfinishedTurnDirectories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	// A user-last turn infers done and tool-calls infers waiting, as in
	// InferSessionStatus; only an unfinished assistant envelope is busy.
	want := []string{"/r/.worktrees/p/open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directories = %v, want %v", got, want)
	}
	_ = d.Close()
	if _, err := d.UnfinishedTurnDirectories(t.Context()); err == nil {
		t.Fatal("closed database accepted")
	}
}
