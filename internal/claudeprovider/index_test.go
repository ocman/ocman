package claudeprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const (
	mainID  = "11111111-1111-4111-8111-111111111111"
	otherID = "22222222-2222-4222-8222-222222222222"
	childID = "agent-a1"
)

var past = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fixture copies testdata into a fresh config dir with old mtimes, so no
// transcript reads as live.
func fixture(t *testing.T) string {
	t.Helper()
	config := t.TempDir()
	err := filepath.Walk("testdata/projects", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(config, "projects", strings.TrimPrefix(path, "testdata/projects"))
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err == nil {
			err = os.WriteFile(target, data, 0o644)
		}
		if err == nil {
			err = os.Chtimes(target, past, past)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func openSynced(t *testing.T, config string) *Index {
	t.Helper()
	idx, err := OpenIndex(filepath.Join(t.TempDir(), "claude-index.db"), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	return idx
}

func get[T any](t *testing.T, idx *Index, path string) T {
	t.Helper()
	rec := httptest.NewRecorder()
	(&handler{idx: idx, live: newLive(idx, Options{}, claudetest.Version)}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestListProjection(t *testing.T) {
	idx := openSynced(t, fixture(t))
	sessions := get[[]db.Session](t, idx, "/sessions")
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want the two top-level sessions", sessions)
	}
	byID := map[string]db.Session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	main := byID[mainID]
	if main.Title != "Marker subagent run" || main.Directory != "/work/repo" || main.MessageCount != 4 ||
		main.TotalInputTokens != 21 || main.TotalOutputTokens != 10 || main.Status != db.StatusWaiting {
		t.Fatalf("main projection = %+v", main)
	}
	other := byID[otherID]
	if other.Title != "Fix the build" || other.Status != db.StatusInterrupted {
		t.Fatalf("an unfinished, stale turn must settle as interrupted: %+v", other)
	}
	if got := get[[]db.Session](t, idx, "/sessions?directory=/work/other"); len(got) != 1 || got[0].ID != otherID {
		t.Fatalf("directory filter = %+v", got)
	}
	life := get[platforms.SessionLifecycle](t, idx, "/sessions/"+mainID+"/lifecycle")
	if life.Status != db.StatusWaiting || life.LatestMessageID != "msg_C" || life.LatestMessageRole != "assistant" {
		t.Fatalf("lifecycle = %+v", life)
	}
}

func TestWarmStartParsesNoFiles(t *testing.T) {
	config := fixture(t)
	path := filepath.Join(t.TempDir(), "claude-index.db")
	open := func(config string) *Index {
		idx, err := OpenIndex(path, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := idx.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		return idx
	}
	idx := open(config)
	if n := idx.parsed.Load(); n != 3 {
		t.Fatalf("cold start parsed %d files, want 3", n)
	}
	_ = idx.Close()
	idx = open(config)
	if n := idx.parsed.Load(); n != 0 {
		t.Fatalf("warm start parsed %d files, want 0", n)
	}
	transcript := filepath.Join(config, "projects", "-work-other", otherID+".jsonl")
	if err := os.Chtimes(transcript, past.Add(time.Hour), past.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := idx.Sync(t.Context()); err != nil || idx.parsed.Load() != 1 {
		t.Fatalf("a touched file must be the only reparse: parsed=%d err=%v", idx.parsed.Load(), err)
	}
	_ = idx.Close()
	// Another config dir is another history: the index is wiped.
	other := fixture(t)
	idx = open(other)
	defer idx.Close()
	if n := idx.parsed.Load(); n != 3 {
		t.Fatalf("config change parsed %d files, want a full rebuild", n)
	}
}

func partsOf(t *testing.T, d platforms.SessionDetail) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, p := range d.Parts {
		var m map[string]any
		if err := json.Unmarshal(p.Data, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func tool(parts []map[string]any, name string) map[string]any {
	for _, p := range parts {
		if p["tool"] == name {
			return p["state"].(map[string]any)
		}
	}
	return nil
}

func TestSubagentRendersUnderTaskCall(t *testing.T) {
	idx := openSynced(t, fixture(t))
	d := get[platforms.SessionDetail](t, idx, "/sessions/"+mainID+"?limit=30&offset=0")
	if d.TotalMessages != 4 || len(d.Messages) != 4 || d.DefaultModel != "claude-sonnet-5" {
		t.Fatalf("detail = %d/%d messages, model %q", len(d.Messages), d.TotalMessages, d.DefaultModel)
	}
	parts := partsOf(t, d)
	task := tool(parts, "task")
	if task == nil || task["status"] != "completed" || task["output"] != "SUBAGENT_DONE" ||
		task["metadata"].(map[string]any)["sessionId"] != childID {
		t.Fatalf("Agent call must be a task part linked to its subagent: %+v", task)
	}
	bash := tool(parts, "bash")
	if bash == nil || bash["output"] != "file.txt" || bash["title"] != "List files" {
		t.Fatalf("tool_use/tool_result pairing: %+v", bash)
	}
	if len(d.SessionTree) != 2 {
		t.Fatalf("tree = %+v", d.SessionTree)
	}
	for _, s := range d.SessionTree {
		if s.ID == childID && s.ParentID != mainID {
			t.Fatalf("subagent parent = %q", s.ParentID)
		}
	}
	child := get[platforms.SessionDetail](t, idx, "/sessions/"+childID+"?limit=30&offset=0")
	cparts := partsOf(t, child)
	if len(child.Messages) != 3 || child.Session.ParentID != mainID {
		t.Fatalf("child detail = %+v", child)
	}
	var reasoning bool
	for _, p := range cparts {
		reasoning = reasoning || (p["type"] == "reasoning" && p["text"] == "I will run bash")
	}
	if !reasoning || tool(cparts, "bash")["status"] != "completed" {
		t.Fatalf("child parts = %+v", cparts)
	}
	// Part ids are stable across reads.
	again := get[platforms.SessionDetail](t, idx, "/sessions/"+mainID+"?limit=30&offset=0")
	for i := range d.Parts {
		if d.Parts[i].ID != again.Parts[i].ID {
			t.Fatalf("unstable part id %q vs %q", d.Parts[i].ID, again.Parts[i].ID)
		}
	}
}

func TestPagingReadsOnlyRequestedRange(t *testing.T) {
	idx := openSynced(t, fixture(t))
	offs, err := idx.offsets(t.Context(), mainID)
	if err != nil || len(offs) != 4 {
		t.Fatalf("offsets = %v, %v", offs, err)
	}
	idx.bytesRead.Store(0)
	d := get[platforms.SessionDetail](t, idx, "/sessions/"+mainID+"?limit=1&offset=1")
	if len(d.Messages) != 1 || d.Messages[0].ID != "msg_B" || d.TotalMessages != 4 {
		t.Fatalf("page = %+v", d.Messages)
	}
	if got, want := idx.bytesRead.Load(), offs[3]-offs[2]; got != want {
		t.Fatalf("read %d bytes, want exactly the page's %d", got, want)
	}
	if bash := tool(partsOf(t, d), "bash"); bash["output"] != "file.txt" {
		t.Fatalf("a page keeps its tool results: %+v", bash)
	}
	idx.bytesRead.Store(0)
	if d := get[platforms.SessionDetail](t, idx, "/sessions/"+mainID+"?limit=5&offset=10"); len(d.Messages) != 0 || idx.bytesRead.Load() != 0 {
		t.Fatalf("an empty page must read nothing: %d bytes", idx.bytesRead.Load())
	}
}

// A transcript written moments ago is not a running turn: without a process,
// hook or other observed lifecycle the open turn settles as interrupted (AD-10).
func TestUnobservedTranscriptIsNotBusyAndRefreshesOnRead(t *testing.T) {
	config := fixture(t)
	idx := openSynced(t, config)
	path := filepath.Join(config, "projects", "-work-other", otherID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"user","uuid":"o-0003","timestamp":"2026-10-04T09:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_o1","content":"package main"}]}}` + "\n")
	_ = f.Close()
	// No Sync: reading the session notices the change itself.
	d := get[platforms.SessionDetail](t, idx, "/sessions/"+otherID+"?limit=30&offset=0")
	if d.Session.Status != db.StatusInterrupted {
		t.Fatalf("an unobserved open turn must not read as busy from its mtime, got %s", d.Session.Status)
	}
	if read := tool(partsOf(t, d), "read"); read["output"] != "package main" || read["input"].(map[string]any)["filePath"] != "/work/other/main.go" {
		t.Fatalf("read part = %+v", read)
	}
}

func TestInferStatus(t *testing.T) {
	for _, tc := range []struct {
		role, finish  string
		apiErr, close bool
		want          db.SessionStatus
	}{
		{"assistant", "stop", false, false, db.StatusWaiting},
		{"assistant", "tool-calls", false, false, db.StatusBusy},
		{"assistant", "stop", true, false, db.StatusError},
		{"user", "", false, false, db.StatusBusy},
		{"user", "", false, true, db.StatusDone},
		{"", "", false, false, db.StatusDone},
	} {
		if got := inferStatus(tc.role, tc.finish, tc.apiErr, tc.close); got != tc.want {
			t.Errorf("inferStatus(%+v) = %s", tc, got)
		}
	}
}

func TestWatcherIndexesNewTranscript(t *testing.T) {
	config := fixture(t)
	idx, err := OpenIndex(filepath.Join(t.TempDir(), "claude-index.db"), config)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	go idx.Run(t.Context(), time.Hour) // the sweep never fires here
	deadline := time.Now().Add(10 * time.Second)
	for len(get[[]db.Session](t, idx, "/sessions")) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("initial sync did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	path := filepath.Join(config, "projects", "-work-other", "33333333-3333-4333-8333-333333333333.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","cwd":"/work/other","uuid":"n-1","timestamp":"2026-10-05T09:00:00.000Z","message":{"role":"user","content":"[Request interrupted by user]"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for len(get[[]db.Session](t, idx, "/sessions")) != 3 {
		if time.Now().After(deadline) {
			t.Fatal("directory watch did not index the new transcript")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for len(get[[]db.Session](t, idx, "/sessions")) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("removed transcript stayed indexed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Subagent transcripts cannot be driven as independent conversations.
func TestUnsupportedSessionOperations(t *testing.T) {
	idx := openSynced(t, fixture(t))
	h := &handler{idx: idx, live: newLive(idx, Options{}, claudetest.Version)}
	for _, c := range []struct {
		req  *http.Request
		code int
	}{
		{httptest.NewRequest(http.MethodPost, "/sessions/"+childID+"/send", strings.NewReader("{}")), http.StatusForbidden},
		{httptest.NewRequest(http.MethodPost, "/sessions/"+mainID+"/rename", strings.NewReader(`{"Title":"x"}`)), http.StatusForbidden},
		{httptest.NewRequest(http.MethodPost, "/sessions/missing/send", strings.NewReader("{}")), http.StatusNotFound},
		{httptest.NewRequest(http.MethodPost, "/sessions/"+mainID+"/shell", strings.NewReader("{}")), http.StatusNotImplemented},
		{httptest.NewRequest(http.MethodPost, "/other", strings.NewReader("{}")), http.StatusNotImplemented},
		{httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader("not json")), http.StatusBadRequest},
		{httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"Directory":"relative"}`)), http.StatusBadRequest},
		{httptest.NewRequest(http.MethodPost, "/sessions/"+mainID+"/fork", strings.NewReader(`{"MessageID":"m"}`)), http.StatusNotImplemented},
		{httptest.NewRequest(http.MethodPost, "/sessions/missing/fork", strings.NewReader(`{}`)), http.StatusNotFound},
		{httptest.NewRequest(http.MethodGet, "/unknown", nil), http.StatusNotImplemented},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, c.req)
		if rec.Code != c.code {
			t.Fatalf("%s %s = %d, want %d: %s", c.req.Method, c.req.URL.Path, rec.Code, c.code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d", rec.Code)
	}
}
