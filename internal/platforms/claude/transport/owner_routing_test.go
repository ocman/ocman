package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestSharedTranscriptListingCannotClaimAnotherProjectsSession(t *testing.T) {
	const id = "owned-session"
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "claude-sessions.json")
	raw, _ := json.Marshal(map[string]any{id: map[string]any{"directory": root, "projectRoot": root, "started": true}})
	if err := os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Older supervisors ignore runtime=1 and expose the shared transcript list.
	// Listing this session on another project must not grant it live ownership.
	wrong, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]db.Session{{ID: id, Directory: root, Status: db.StatusInterrupted}})
	})
	if _, err := wrong.RuntimeSessions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !wrong.Knows(id) {
		t.Fatal("fixture did not reproduce the shared known-session cache")
	}
	c := &claudeProjects{statePath: statePath, roots: map[string]*claudeProject{
		"/unrelated-project": {watched: watched{adapter: wrong}},
	}}
	if got := c.owner(id); got != nil {
		t.Fatal("a shared transcript listing routed the live conversation to an unrelated supervisor")
	}
	right := sseBrowser(t, id)
	c.roots[root] = &claudeProject{watched: watched{adapter: right}}
	// Ownership must work before a headless event or inventory seeds Knows.
	for range 20 {
		if got := c.owner(id); got != right {
			t.Fatal("recorded project owner lost to an arbitrary known-session cache")
		}
		got, owned, err := c.route(t.Context(), id, false)
		if err != nil || !owned || got != right {
			t.Fatalf("live stream route ignored recorded owner: owned=%v err=%v", owned, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	out := &cancelStreamWriter{cancel: cancel}
	p := &claudePlatform{projects: c}
	_ = p.ProxyEvents(ctx, id, out, nil)
	if !strings.Contains(out.String(), `"type":"message.updated"`) || !strings.Contains(out.String(), `"sessionID":"`+id+`"`) {
		t.Fatalf("conversation stream did not receive its recorded owner's events: %s", out.String())
	}
}

type cancelStreamWriter struct {
	strings.Builder
	cancel context.CancelFunc
}

func (w *cancelStreamWriter) Write(raw []byte) (int, error) {
	n, err := w.Builder.Write(raw)
	w.cancel()
	return n, err
}
