package claudeprovider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionContextTokenCount(t *testing.T) {
	for _, page := range []string{"limit=30&offset=0", "limit=1&offset=1", "limit=0&offset=0"} {
		t.Run(page, func(t *testing.T) {
			idx := openSynced(t, fixture(t))
			d := get[platforms.SessionDetail](t, idx, "/sessions/"+mainID+"?"+page)
			if d.ContextTokenCount != 6 {
				t.Fatalf("context tokens = %d, want latest assistant input=4 + output=2 regardless of page", d.ContextTokenCount)
			}
		})
	}
}

func TestSessionContextUsage(t *testing.T) {
	const user = `{"type":"user","uuid":"u","timestamp":"2026-10-05T10:00:00Z","message":{"content":"hello"}}`
	const assistant = `{"type":"assistant","uuid":"a","timestamp":"2026-10-05T10:00:01Z","message":{"id":"a","model":"claude-sonnet-5","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`
	const emptyOutput = `{"type":"assistant","uuid":"b","timestamp":"2026-10-05T10:00:02Z","message":{"id":"b","content":[{"type":"text","text":"error"}],"usage":{"input_tokens":99,"output_tokens":0}}}`
	for _, tt := range []struct {
		name  string
		lines []string
		want  int64
	}{
		{"user only", []string{user}, 0},
		{"cached input", []string{user, assistant}, 126},
		{"skip empty output", []string{user, assistant, emptyOutput}, 126},
		{"no successful output", []string{user, emptyOutput}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := fixture(t)
			path := filepath.Join(config, "projects", "-work-repo", mainID+".jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tt.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			idx := openSynced(t, config)
			d := get[platforms.SessionDetail](t, idx, "/sessions/"+mainID+"?limit=1&offset=0")
			if d.ContextTokenCount != tt.want {
				t.Fatalf("context tokens = %d, want %d", d.ContextTokenCount, tt.want)
			}
		})
	}
}

func TestSessionContextQueryError(t *testing.T) {
	idx := openSynced(t, fixture(t))
	if _, err := idx.db.Exec(`DROP TABLE usage`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&handler{idx: idx, live: newLive(idx, Options{}, "test")}).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/sessions/"+mainID+"?limit=1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when context usage cannot be read", rec.Code)
	}
}
