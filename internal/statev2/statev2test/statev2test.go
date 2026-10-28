// Package statev2test opens disposable conversation-history stores for tests.
package statev2test

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

// Open returns an empty store in the test's temporary directory, closed at
// cleanup.
func Open(t testing.TB) *statev2.DB {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
