package state

import (
	"sync"
	"testing"
)

func TestSessionCommitsAreOrderedAndIdempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	db.db.SetMaxOpenConns(1)
	branch := "main"
	base := SessionCommit{Platform: "opencode", SessionID: "s1", SourceMessageID: "m1", ToolPartID: "p1", ToolCallID: "c1", SourceCallID: "c1", Branch: &branch, Subject: "subject"}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row := base
			row.SHA = "abc1234"
			if _, err := db.RecordSessionCommit(t.Context(), row); err != nil {
				t.Errorf("record duplicate: %v", err)
			}
		}()
	}
	wg.Wait()
	second := base
	second.SHA, second.Subject, second.Branch = "def5678", "second", nil
	if inserted, err := db.RecordSessionCommit(t.Context(), second); err != nil || !inserted {
		t.Fatalf("record second = %v, %v", inserted, err)
	}

	got, err := db.ListSessionCommits(t.Context(), "opencode", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SHA != "abc1234" || got[1].SHA != "def5678" || got[1].Branch != nil || got[0].Order >= got[1].Order {
		t.Fatalf("commits = %#v", got)
	}
	other, err := db.ListSessionCommits(t.Context(), "opencode", "other")
	if err != nil || len(other) != 0 {
		t.Fatalf("other session = %#v, %v", other, err)
	}
}
