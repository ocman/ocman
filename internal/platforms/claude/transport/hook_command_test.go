package claudecode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
)

func TestRunHookCommandJournalsEverySessionAndPostsOnlyWhenManaged(t *testing.T) {
	for _, marker := range []string{"", "1"} {
		t.Run("marker="+marker, func(t *testing.T) {
			t.Setenv("OCMAN_MANAGED", marker)
			journal := t.TempDir()
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			defer srv.Close()
			input := `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/w"}`
			if !RunHookCommand([]string{"hook", "--addr", strings.TrimPrefix(srv.URL, "http://"), "--journal", journal, "Stop"}, strings.NewReader(input)) {
				t.Fatal("hook not handled")
			}
			entries, err := readJournal(journal)
			if err != nil || len(entries) != 1 || entries[0].Record.Session != "s1" || entries[0].Record.Type != "Stop" {
				t.Fatalf("journal = %+v, %v", entries, err)
			}
			want := int32(0)
			if marker == "1" {
				want = 1
			}
			if got := requests.Load(); got != want {
				t.Fatalf("status posts = %d, want %d", got, want)
			}
		})
	}
	if !RunHookCommand([]string{"hook", "--bogus"}, strings.NewReader("")) {
		t.Fatal("invalid hook arguments must still succeed")
	}
}

func TestRunHookCommandManagedOnly(t *testing.T) {
	for _, marker := range []string{"", "0", "1"} {
		t.Run("marker="+marker, func(t *testing.T) {
			t.Setenv("OCMAN_MANAGED", marker)
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
			}))
			defer srv.Close()
			if !RunHookCommand([]string{"hook", "--addr", strings.TrimPrefix(srv.URL, "http://"), "Stop"}, strings.NewReader(`{"session_id":"s1"}`)) {
				t.Fatal("hook not handled")
			}
			want := int32(0)
			if marker == "1" {
				want = 1
			}
			if got := requests.Load(); got != want {
				t.Fatalf("requests = %d, want %d", got, want)
			}
		})
	}
}

// readJournal returns every complete record in the journal directory.
func readJournal(dir string) ([]historyjournal.Entry, error) {
	segments, err := historyjournal.Segments(dir)
	var out []historyjournal.Entry
	for _, segment := range segments {
		if err != nil {
			break
		}
		var entries []historyjournal.Entry
		entries, err = historyjournal.ReadSegment(dir, segment, 0, 100)
		out = append(out, entries...)
	}
	return out, err
}
