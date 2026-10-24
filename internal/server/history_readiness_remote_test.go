package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestConnectedRemoteDoesNotBlockLocalHistoryAnalytics(t *testing.T) {
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registry := platforms.NewRegistry()
	registry.Register(&historyTestPlatform{fakePlatform: &fakePlatform{id: "local"}, text: "retained"})
	registry.Register(&historyTestPlatform{fakePlatform: &fakePlatform{id: "r-existing:opencode"}, text: "remote"})
	s := New(nil, nil, "127.0.0.1:0", registry, nil).WithConversationHistory(store, "owner")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	if err := s.historyIngest.Start(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { s.historyIngest.Run(ctx); close(done) }()
	defer func() { cancel(); <-done; s.analyticsModule().Finalize() }()
	for deadline := time.Now().Add(3 * time.Second); ; {
		if ids, ready := s.historyIngest.ReadyInstances(); ready {
			if len(ids) != 1 {
				t.Fatalf("imported remote history locally: %v", ids)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local import did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	// Remote discovery adds adapters later than WithConversationHistory. They
	// are not local ingestion sources and must not change local readiness.
	registry.Register(&fakePlatform{id: "r-box:opencode"})
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("completed local history blocked by remote: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
		t.Fatal(w.Header())
	}
	// A newly enabled local platform makes analytics partial, not unavailable.
	registry.Register(&fakePlatform{id: "missing-local"})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if w.Code != http.StatusOK || w.Header().Get("X-Ocman-Analytics-Partial") != "true" {
		t.Fatalf("omitted local platform: %d %s", w.Code, w.Body)
	}
}
