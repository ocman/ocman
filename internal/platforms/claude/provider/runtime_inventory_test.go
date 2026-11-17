package claudeprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// Every supervisor reads the shared ownership file. A started session whose
// transcript Claude has since pruned is not a pre-transcript session: listing
// it from every supervisor made history ingestion report it as ambiguously
// owned (busy) and retry it forever.
func TestRuntimeInventoryOmitsStartedSessionsWithoutLiveState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-sessions.json")
	if err := os.WriteFile(path, []byte(`{
		"fresh":{"directory":"/work"},
		"pruned":{"directory":"/work","started":true},
		"running":{"directory":"/work","started":true},
		"settled":{"directory":"/work","started":true}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := &live{opts: Options{StatePath: path, ConfigDir: "/config"}, status: map[string]db.Session{
		"running": {Status: db.StatusBusy}, "settled": {Status: db.StatusDone},
	}}
	rec := httptest.NewRecorder()
	(&handler{live: l}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions?runtime=1", nil))
	var rows []db.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode %d %s: %v", rec.Code, rec.Body, err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"fresh", "running", "settled"}) {
		t.Fatalf("runtime inventory = %v, want fresh, running and settled only", ids)
	}
	// Browsing reads still resolve the pruned session read-only.
	if _, ok := (&handler{live: l}).unindexed("pruned", platforms.ErrNotFound); !ok {
		t.Fatal("pruned owned session no longer resolves for browsing")
	}
}
