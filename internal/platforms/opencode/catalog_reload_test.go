package opencode

import (
	"context"
	"testing"
	"time"
)

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

func TestCatalogReloadFencesInflightFetch(t *testing.T) {
	cache := newHTTPCache(time.Minute)
	started, release, oldDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release); <-oldDone })
	go func() {
		defer close(oldDone)
		cache.getOrFetch("port", "/agent", func() ([]byte, bool) {
			close(started)
			<-release
			return []byte("old"), true
		})
	}()
	<-started
	cache.invalidatePort("port")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	body, ok := cache.getOrFetchContext(ctx, "port", "/agent", func() ([]byte, bool) {
		return []byte("fresh"), true
	})
	if !ok || string(body) != "fresh" {
		t.Fatalf("post-reload fetch = %q, %v; want fresh without waiting for old fetch", body, ok)
	}
}

func TestCatalogReloadRejectsStalePublication(t *testing.T) {
	cache := newHTTPCache(time.Minute)
	started, release, oldDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(oldDone)
		cache.getOrFetch("port", "/agent", func() ([]byte, bool) {
			close(started)
			<-release
			return []byte("old"), true
		})
	}()
	<-started
	cache.invalidatePort("port")
	close(release)
	<-oldDone
	if body, ok := cache.get("port", "/agent"); ok {
		t.Fatalf("old fetch repopulated the cache: %q", body)
	}
}
