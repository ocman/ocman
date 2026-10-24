package db

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestHistoryHumanActivityExcludesClaudeSubagents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := statev2.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "claude", OwnerID: "owner", Platform: "claudecode", SourceKey: "/source"}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	for _, child := range []bool{false, true} {
		id := "root"
		if child {
			id = "child"
		}
		_, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "claude", SessionID: id}, Title: "Ordinary title", CreatedAt: at, UpdatedAt: at, Subagent: child}, Messages: []statev2.Message{{ID: id + "-user", Role: "user", CreatedAt: at, Extensions: json.RawMessage(`{"role":"user"}`)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenHistoryAnalytics(path, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, cutoff := range []int64{0, at} {
		days, err := reader.GetDailyActivity(t.Context(), cutoff, "", "")
		if err != nil {
			t.Fatal(err)
		}
		var sessions, prompts int
		for _, day := range days {
			sessions += day.Sessions
			prompts += day.UserMessages
		}
		if sessions != 1 || prompts != 1 {
			t.Fatalf("human activity includes helper: sessions=%d prompts=%d cutoff=%d", sessions, prompts, cutoff)
		}
	}
}
