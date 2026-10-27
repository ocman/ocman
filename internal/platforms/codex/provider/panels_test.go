package codexprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

func detailOf(c *Converter, dir string) *platforms.SessionDetail {
	return &platforms.SessionDetail{Session: &db.Session{ID: c.SessionID, Directory: dir}, Messages: c.Messages, Parts: c.Parts}
}

func TestChangesFromStored(t *testing.T) {
	const dir = "testdata/0.164.0/"
	thread := load[codexappserver.ThreadReadResponse](t, dir+"thread-read.json").Thread
	c := &Converter{}
	c.AddHistory(&thread, load[struct{ Data []codexappserver.Turn }](t, dir+"turns.json").Data,
		load[struct{ Data []codexappserver.ItemEntry }](t, dir+"items.json").Data)
	// The same file edited twice, and a no-op edit, on top of the recording.
	c.AddTurn(&codexappserver.Turn{ID: "more", Status: "completed", Items: []codexappserver.Item{{Type: "fileChange", ID: "f2", Status: "completed", StartedAtMs: 9,
		Changes: []codexappserver.FileUpdate{
			{Path: "/fixture/repo/hello.txt", Kind: codexappserver.FileUpdateKind{Type: "update"}, Diff: "@@ -1 +1 @@\n-hi\n+hello\n"},
			{Path: "/elsewhere/x", Kind: codexappserver.FileUpdateKind{Type: "update"}, Diff: "@@ -1 +1 @@\n"},
		}}}})
	changes, err := ChangesFromStored(context.Background(), detailOf(c, "/fixture/repo"))
	if err != nil {
		t.Fatal(err)
	}
	if !changes.Supported || changes.FilesChanged != 1 || changes.TotalAdditions != 2 || changes.TotalDeletions != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	f := changes.Files[0]
	if f.Path != "/fixture/repo/hello.txt" || f.DisplayPath != "hello.txt" || f.EditCount != 2 || len(f.Edits) != 2 ||
		f.Edits[0].Tool != "apply_patch" || f.LastEditAt != 9 || f.Patch == "" {
		t.Fatalf("file = %+v", f)
	}
	if _, err := ChangesFromStored(context.Background(), &platforms.SessionDetail{}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing session = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ChangesFromStored(canceled, detailOf(c, "")); err == nil {
		t.Fatal("canceled read succeeded")
	}
	if relative("/a/b", "") != "/a/b" || relative("/x/y", "/a") != "/x/y" {
		t.Fatal("relative paths")
	}
}

func TestInfoFromStored(t *testing.T) {
	thread := load[codexappserver.ThreadReadResponse](t, extras+"thread-read.json").Thread
	usage, err := ReadRolloutUsage(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	c := &Converter{Usage: usage}
	c.AddHistory(&thread, load[struct{ Data []codexappserver.Turn }](t, extras+"turns.json").Data,
		load[struct{ Data []codexappserver.ItemEntry }](t, extras+"items.json").Data)
	info, err := InfoFromStored(context.Background(), detailOf(c, ""))
	if err != nil {
		t.Fatal(err)
	}
	// Two turns spent tokens: 14619 + 14694 input, of which some cached.
	if got := info.Tokens.Input + info.Tokens.CacheRead; got != 14619+14694 {
		t.Fatalf("input tokens = %+v", info.Tokens)
	}
	if info.Messages.User == 0 || info.Messages.Assistant == 0 || info.Context.Tokens <= 0 || info.Context.Limit <= 0 || info.Supported {
		t.Fatalf("info = %+v", info)
	}
	if _, err := InfoFromStored(context.Background(), nil); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing session = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InfoFromStored(canceled, detailOf(c, "")); err == nil {
		t.Fatal("canceled read succeeded")
	}
}
