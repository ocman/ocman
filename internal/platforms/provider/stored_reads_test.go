package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestStoredObservationsNeverFetchNativeSessions(t *testing.T) {
	f := newFake(t, allCapabilities())
	a := f.adapter(t)
	a.live["root"] = platforms.LiveState{Status: "busy"}
	a.permissions["p"] = platforms.LivePrompt{"sessionID": "child"}
	a.questions["q"] = platforms.LivePrompt{"sessionID": "root"}
	rows := []db.Session{{ID: "root", Title: "stored", Status: db.StatusDone}}
	if err := a.EnrichStoredSessions(t.Context(), rows, map[string]string{"child": "root"}); err != nil {
		t.Fatal(err)
	}
	if rows[0].Title != "stored" || rows[0].Status != db.StatusWaiting || !rows[0].PendingPermission || !rows[0].PendingQuestion || !rows[0].LiveConnection {
		t.Fatalf("stored observations: %+v", rows)
	}
	f.mu.Lock()
	for _, call := range f.calls {
		if call.path != "/hello" {
			t.Fatalf("history read during overlay: %+v", call)
		}
	}
	f.mu.Unlock()
	a.draining = true
	rows[0].Status = db.StatusBusy
	if err := a.EnrichStoredSessions(t.Context(), rows, nil); err != nil {
		t.Fatal(err)
	}
	if rows[0].Status != db.StatusInterrupted || rows[0].PendingPermission || rows[0].PendingQuestion || rows[0].LiveConnection {
		t.Fatalf("disconnected observations leaked: %+v", rows)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.EnrichStoredSessions(ctx, rows, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.reply("GET", "/catalog", platforms.DirectoryCatalog{DefaultAgent: "live"})
	a.draining = false
	if catalog, err := a.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"}); err != nil || catalog.DefaultAgent != "live" {
		t.Fatalf("configuration: %+v %v", catalog, err)
	}
}
