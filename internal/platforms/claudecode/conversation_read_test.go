package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestConversationHeadReadsIndexedClaudeMetadata(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.CopyFS(filepath.Join(config, "projects"), os.DirFS("../../claudeprovider/testdata/projects")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var history *claudeprovider.HistoryReader
	opts := claudeprovider.DefaultOptions(root)
	opts.ConfigDir, opts.CLI = config, claudetest.Binary(t)
	opts.HistoryReady = func(h *claudeprovider.HistoryReader) { history = h }
	adapter, err := startClaudeBrowser(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	p := &claudePlatform{Adapter: adapter, history: history, configDir: config, projects: &claudeProjects{}}
	if _, err := p.HistoryInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	head, err := p.ConversationHead(t.Context(), id)
	if err != nil || head.Session.ID != id || head.Version == "" || head.DefaultModel == "" || !head.Session.LiveConnection || head.Session.Status == db.StatusBusy {
		t.Fatalf("head: %+v %v", head, err)
	}
	if head, err := p.ConversationHead(t.Context(), "missing"); !errors.Is(err, platforms.ErrNotFound) || head == nil || head.Identity.SourceKey == "" {
		t.Fatal("missing identity lost", head, err)
	}
	p.history = nil
	if _, err := p.ConversationHead(t.Context(), id); err == nil {
		t.Fatal("missing source accepted")
	}
}
