package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// maxShortTempRoot is the longest temp root ShortTempDir uses: the length of
// /private/tmp, which the callers' unix socket paths were sized against on
// macOS.
const maxShortTempRoot = len("/private/tmp")

// ShortTempDir creates a removed-on-cleanup directory whose path is short
// enough for unix sockets nested inside it (104 bytes on macOS, 108 on Linux).
// It uses os.TempDir when that is short, so a RAM-backed TMPDIR such as CI's
// is honoured, and falls back to /tmp when it is not (macOS's per-user
// $TMPDIR is about 50 bytes).
func ShortTempDir(tb testing.TB, prefix string) string {
	tb.Helper()
	dir, err := os.MkdirTemp(shortTempRoot(os.TempDir()), prefix)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func shortTempRoot(tmp string) string {
	if tmp = filepath.Clean(tmp); len(tmp) <= maxShortTempRoot {
		return tmp
	}
	return "/tmp"
}
