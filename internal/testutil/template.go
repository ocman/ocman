package testutil

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var templates sync.Map // name -> *template

type template struct {
	once sync.Once
	data []byte
	err  error
}

// TemplateCopy writes a copy of the file build produces to dst and
// returns dst. build runs once per test binary for each name; every
// later call only copies bytes.
//
// It exists for SQLite databases whose schema migrations dominate test
// setup: migrating a fresh state.db costs ~50 ms, copying one ~1.5 ms.
// build must leave a self-contained file (close the database, so the WAL
// is checkpointed) with no per-install data such as generated IDs.
func TemplateCopy(tb testing.TB, name, dst string, build func(path string) error) string {
	tb.Helper()
	v, _ := templates.LoadOrStore(name, &template{})
	tpl := v.(*template)
	tpl.once.Do(func() {
		dir, err := os.MkdirTemp("", "ocman-template-")
		if err != nil {
			tpl.err = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, name)
		if tpl.err = build(path); tpl.err == nil {
			tpl.data, tpl.err = os.ReadFile(path)
		}
	})
	if tpl.err != nil {
		tb.Fatalf("building %s template: %v", name, tpl.err)
	}
	if err := os.WriteFile(dst, tpl.data, 0o600); err != nil {
		tb.Fatal(err)
	}
	return dst
}
