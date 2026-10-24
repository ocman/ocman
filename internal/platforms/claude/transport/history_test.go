package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
)

func TestHistoryAdapterUsesBrowsingSourceWithoutLaunchingSession(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(config, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var history *claudeprovider.HistoryReader
	opts := claudeprovider.DefaultOptions(root)
	opts.ConfigDir, opts.CLI = config, claudetest.Binary(t)
	opts.HistoryReady = func(reader *claudeprovider.HistoryReader) { history = reader }
	if _, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(root, "spool")); err != nil {
		t.Fatal(err)
	}
	p := &claudePlatform{history: history, configDir: config}
	identity, err := p.HistoryIdentity(t.Context())
	canonicalConfig, pathErr := filepath.EvalSymlinks(config)
	if err != nil || pathErr != nil || identity.Platform != "claudecode" || identity.SourceKey != canonicalConfig {
		t.Fatalf("identity: %+v %v %v", identity, err, pathErr)
	}
	if entries, err := p.HistoryInventory(t.Context()); err != nil || len(entries) != 0 {
		t.Fatalf("inventory: %+v %v", entries, err)
	}
	if _, err := p.HistorySnapshot(t.Context(), "missing"); err == nil {
		t.Fatal("accepted nonexistent history")
	}
	p.configDir = filepath.Join(root, "unavailable")
	if identity, err := p.HistoryIdentity(t.Context()); err != nil || identity.SourceKey == "" {
		t.Fatalf("fresh configuration has no stable identity: %+v %v", identity, err)
	}
	p.history = nil
	if _, err := p.HistoryIdentity(t.Context()); err == nil {
		t.Fatal("accepted missing identity source")
	}
	if _, err := p.HistoryInventory(t.Context()); err == nil {
		t.Fatal("accepted missing inventory source")
	}
	if _, err := p.HistorySnapshot(t.Context(), "missing"); err == nil {
		t.Fatal("accepted missing snapshot source")
	}
}
