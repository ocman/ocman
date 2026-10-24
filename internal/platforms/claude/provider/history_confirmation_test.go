package claudeprovider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestRootTranscriptIgnoresConfigurationSubagentsAncestor(t *testing.T) {
	config := filepath.Join(t.TempDir(), "subagents", "claude")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fixture(t), config); err != nil {
		t.Fatal(err)
	}
	idx := openSynced(t, config)
	root, err := idx.get(t.Context(), mainID)
	if err != nil || root == nil || root.RootID != mainID || root.ParentID != "" {
		t.Fatalf("configuration ancestry changed root identity: %+v %v", root, err)
	}
}

func TestHistoryConfirmationReclaimsSettledChildView(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	if err := l.handle(p, []byte(`{"type":"assistant","parent_tool_use_id":"spawn","message":{"id":"child-message","role":"assistant","content":[{"type":"text","text":"Working"}]}}`)); err != nil {
		t.Fatal(err)
	}
	confirm := func(receipt providerprotocol.HistoryConfirmation) {
		t.Helper()
		raw, _ := json.Marshal(receipt)
		r := httptest.NewRequest(http.MethodPost, "/sessions/agent-child/history-confirmed", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		(&handler{live: l}).ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("confirmation endpoint: HTTP %d %s", w.Code, w.Body.String())
		}
	}
	detail := l.runtimeDetail(db.Session{ID: "agent-child"}, httptest.NewRequest(http.MethodGet, "/", nil))
	old := HistoryConfirmation(&db.HistorySnapshot{Messages: detail.Messages, Parts: detail.Parts})
	confirm(old)
	if len(l.view["agent-child"]) == 0 {
		t.Fatal("busy child was reclaimed")
	}
	if err := l.handle(p, []byte(`{"type":"assistant","parent_tool_use_id":"spawn","message":{"id":"later-message","role":"assistant","content":[{"type":"text","text":"Finished"}]}}`)); err != nil {
		t.Fatal(err)
	}
	l.system(p, []byte(`{"subtype":"task_notification","task_id":"child","status":"completed"}`))
	if len(l.view["agent-child"]) == 0 {
		t.Fatal("settled view was discarded before persistence")
	}
	confirm(old)
	confirm(providerprotocol.HistoryConfirmation{Entries: map[string]string{}})
	if len(l.view["agent-child"]) == 0 {
		t.Fatal("stale or incomplete persistence receipt reclaimed newer content")
	}
	detail = l.runtimeDetail(db.Session{ID: "agent-child"}, httptest.NewRequest(http.MethodGet, "/", nil))
	for key, digest := range HistoryConfirmation(&db.HistorySnapshot{Messages: detail.Messages, Parts: detail.Parts}).Entries {
		confirm(providerprotocol.HistoryConfirmation{Entries: map[string]string{key: digest}})
	}
	if _, retained := l.view["agent-child"]; retained {
		t.Fatal("durably committed settled child view was not reclaimed")
	}
	if p.children["spawn"].cur != nil {
		t.Fatal("settled converter retained child content")
	}
	if l.status["agent-child"].Status != db.StatusDone || !p.busy {
		t.Fatal("reclamation changed lifecycle")
	}
	confirm(old) // idempotent after reclamation
	l.confirmHistory("unknown", old)
	w := httptest.NewRecorder()
	(&handler{live: l}).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sessions/agent-child/history-confirmed", strings.NewReader("{")))
	if w.Code != http.StatusBadRequest {
		t.Fatal("accepted malformed confirmation")
	}
}

func TestHistoryConfirmationBeforeIdleReclaimsOnSettlement(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(startChild))
	if err := l.handle(p, []byte(`{"type":"assistant","parent_tool_use_id":"spawn","message":{"id":"m","role":"assistant","content":[{"type":"text","text":"Done"}]}}`)); err != nil {
		t.Fatal(err)
	}
	d := l.runtimeDetail(db.Session{ID: "agent-child"}, httptest.NewRequest(http.MethodGet, "/", nil))
	l.confirmHistory("agent-child", HistoryConfirmation(&db.HistorySnapshot{Messages: d.Messages, Parts: d.Parts}))
	if len(l.view["agent-child"]) == 0 {
		t.Fatal("reclaimed while busy")
	}
	l.system(p, []byte(`{"subtype":"task_notification","task_id":"child","status":"completed"}`))
	if _, retained := l.view["agent-child"]; retained {
		t.Fatal("confirmed content retained after idle")
	}
}

func TestHistoryConfirmationChecksContentNotLiveTiming(t *testing.T) {
	a := json.RawMessage(`{"id":"p","sessionID":"child","type":"tool","tool":"bash","state":{"status":"completed","output":"ok","time":{"start":1},"metadata":{"liveTools":[1]}}}`)
	b := json.RawMessage(`{"id":"p","sessionID":"canonical","type":"tool","tool":"bash","state":{"status":"completed","output":"ok","time":{"start":2},"metadata":{}}}`)
	if partContentDigest(a) != partContentDigest(b) {
		t.Fatal("timing or metadata prevented reclamation")
	}
	c := json.RawMessage(strings.Replace(string(b), `"output":"ok"`, `"output":"new"`, 1))
	if partContentDigest(a) == partContentDigest(c) {
		t.Fatal("changed tool output matched a stale receipt")
	}
	if partContentDigest(json.RawMessage("{")) != "" || partContentDigest(json.RawMessage(`{"type":"reasoning","text":"live-only"}`)) != "" {
		t.Fatal("invalid or intentionally unpersisted reasoning was confirmed as content")
	}
}
