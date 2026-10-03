package composerattachments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func isolatedCache(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func TestSaveOwnerCache(t *testing.T) {
	isolatedCache(t)
	file, err := Save(t.Context(), hostsvc.ComposerAttachmentRequest{Directory: "/repo", SessionID: "s1", Name: "../../notes & data.bin"}, bytes.NewBufferString("owner bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if file.Name != "notes___data.bin" || file.Mime != "application/octet-stream" || file.Size != 11 {
		t.Fatalf("file=%+v", file)
	}
	data, err := os.ReadFile(file.Path)
	if err != nil || string(data) != "owner bytes" {
		t.Fatalf("data=%q error=%v", data, err)
	}
	info, _ := os.Stat(file.Path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	if rel, err := filepath.Rel(Root(), file.Path); err != nil || filepath.IsAbs(rel) || rel[:2] == ".." {
		t.Fatalf("path escapes cache: %s", file.Path)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("upload interrupted") }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestSaveRejectsInvalidAndInterruptedUploads(t *testing.T) {
	isolatedCache(t)
	for _, req := range []hostsvc.ComposerAttachmentRequest{{Directory: "relative", SessionID: "s1"}, {Directory: "/repo"}} {
		if _, err := Save(t.Context(), req, bytes.NewReader(nil)); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request := hostsvc.ComposerAttachmentRequest{Directory: "/repo", SessionID: "s1", Name: "file"}
	if _, err := Save(ctx, request, bytes.NewReader(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if _, err := Save(t.Context(), request, failingReader{}); err == nil {
		t.Fatal("read failure accepted")
	}
	if _, err := Save(t.Context(), request, io.LimitReader(zeroReader{}, MaxBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large error=%v", err)
	}
	_ = filepath.WalkDir(Root(), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			t.Errorf("partial file remains: %s", path)
		}
		return err
	})
}
