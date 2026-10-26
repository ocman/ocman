package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageSizes(t *testing.T) {
	config := t.TempDir()
	root := filepath.Join(config, "projects", "project")
	nested := filepath.Join(root, "session", "subagents")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(root, "session.jsonl")
	write(transcript, "raw transcript")
	write(filepath.Join(nested, "agent.jsonl"), "child")
	write(filepath.Join(root, "sessions-index.json"), "not a transcript")
	external := filepath.Join(t.TempDir(), "external.jsonl")
	write(external, "outside source")
	if err := os.Symlink(external, filepath.Join(root, "link.jsonl")); err != nil {
		t.Fatal(err)
	}
	p := &claudePlatform{configDir: config}
	for _, data := range []string{"raw transcript", "raw transcript grows"} {
		write(transcript, data)
		items, err := p.StorageSizes(t.Context())
		if err != nil || len(items) != 1 || items[0].Name != "claudecode:raw" || items[0].SizeBytes != int64(len(data)+5) {
			t.Fatalf("sizes = %+v, %v", items, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.StorageSizes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestStorageSizesMissingAndInvalidRoot(t *testing.T) {
	p := &claudePlatform{configDir: t.TempDir()}
	items, err := p.StorageSizes(t.Context())
	if err != nil || len(items) != 1 || items[0].SizeBytes != 0 {
		t.Fatalf("missing root = %+v, %v", items, err)
	}
	p.configDir = filepath.Join(p.configDir, "file")
	if err := os.WriteFile(p.configDir, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if items, err := p.StorageSizes(t.Context()); err == nil || len(items) != 0 {
		t.Fatalf("invalid root = %+v, %v", items, err)
	}
}
