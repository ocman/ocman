package opencode

import "testing"

func TestInvalidateCatalogsForPort(t *testing.T) {
	catalogCache.put("reload-test", "/agent", []byte("old"))
	catalogCache.put("other-test", "/agent", []byte("other"))
	t.Cleanup(func() { catalogCache.invalidatePort("other-test") })
	InvalidateCatalogsForPort("reload-test")
	if _, ok := catalogCache.get("reload-test", "/agent"); ok {
		t.Fatal("reloaded catalog remains cached")
	}
	if _, ok := catalogCache.get("other-test", "/agent"); !ok {
		t.Fatal("reload invalidated another server's catalog")
	}
}
