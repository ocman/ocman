package server

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

type initialImportPlatform struct{ fakePlatform }

func (*initialImportPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "initial"}, nil
}

func (*initialImportPlatform) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	var entries []db.HistoryEntry
	for i := range 25 {
		entries = append(entries, db.HistoryEntry{ID: fmt.Sprintf("session-%02d", i), Version: "1"})
	}
	return entries, nil
}

func (*initialImportPlatform) HistorySnapshot(_ context.Context, id string) (*db.HistorySnapshot, error) {
	return &db.HistorySnapshot{Session: db.Session{ID: id, TimeUpdated: time.Now().Add(-30 * 24 * time.Hour).UnixMilli()}, Version: "1"}, nil
}

func TestInitialImportArchiveServerWiring(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	registry := platforms.NewRegistry()
	registry.Register(&initialImportPlatform{fakePlatform: fakePlatform{PlatformID: "test"}})
	s := New(nil, core, "127.0.0.1:0", registry, nil).WithConversationHistory(history, "owner")
	if err := s.taskManager().Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer s.taskManager().Stop()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.historyIngest.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(10 * time.Second); !s.historyIngest.InitialImportReady("test"); {
		if time.Now().After(deadline) {
			t.Fatal("import never completed")
		}
		time.Sleep(time.Millisecond)
	}
	archives, err := core.ArchivedSessions(t.Context())
	if err != nil || len(archives) != 5 {
		t.Fatalf("archives=%v err=%v", archives, err)
	}
	var restored state.Key
	for key := range archives {
		restored = key
		break
	}
	if err := core.UnarchiveSession(t.Context(), restored.Platform, restored.SessionID); err != nil {
		t.Fatal(err)
	}
	// The ordinary startup sweep must not immediately undo the keep-twenty
	// selection, even though every imported conversation is a month old.
	s.autoArchiveModule().ArchiveSessions(t.Context())
	archives, err = core.ArchivedSessions(t.Context())
	if err != nil || len(archives) != 4 {
		t.Fatalf("startup sweep changed initial selection: %v %v", archives, err)
	}
}
