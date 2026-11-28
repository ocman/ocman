package usagecollection

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/usagewire"
)

func TestHealthReportClosesDayAndHonorsOptOut(t *testing.T) {
	m, _ := fixture(t)
	now := m.now()
	m.now = func() time.Time { return now }
	var reports []usagewire.Report
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report usagewire.Report
		if r.URL.Path != "/v1/usage/health" || json.NewDecoder(r.Body).Decode(&report) != nil || !report.Valid(now) {
			t.Error("invalid daily health report")
		}
		reports = append(reports, report)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	m.deps.RelayURL = func() string { return srv.URL }
	m.RecordStart(t.Context(), "codex", nil)
	m.RecordStart(t.Context(), "codex", errors.New("private error"))
	finish := m.BeginHealthTurn(t.Context(), "codex")
	finish("done", 2*time.Second, true)
	finish("done", 2*time.Second, true) // A duplicate settlement changes nothing.
	m.sendHealth(t.Context())
	if len(reports) != 0 {
		t.Fatal("open day was submitted")
	}
	now = now.AddDate(0, 0, 1)
	m.sendHealth(t.Context())
	m.sendHealth(t.Context())
	if len(reports) != 1 || reports[0].Health[0].Starts.Ready != 1 || reports[0].Health[0].Starts.Failed != 1 {
		t.Fatalf("unexpected daily reports: %+v", reports)
	}
	turns := reports[0].Health[0].Turns
	if turns.Started != 1 || turns.Completed != 1 || len(turns.FirstResponse) != 7 || turns.FirstResponse[2] != 1 {
		t.Fatalf("unexpected turn counts: %+v", turns)
	}
	m.RecordStart(t.Context(), "codex", nil)
	stale := m.BeginHealthTurn(t.Context(), "codex")
	if w := setting(t, m, http.MethodPost, `{"enabled":false}`); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	m.RecordStart(t.Context(), "codex", nil)
	now = now.AddDate(0, 0, 1)
	if w := setting(t, m, http.MethodPost, `{"enabled":true}`); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	stale("error", 0, false)
	m.sendHealth(t.Context())
	now = now.AddDate(0, 0, 1)
	m.sendHealth(t.Context())
	if len(reports) != 1 {
		t.Fatal("opt-out data was replayed")
	}
}
