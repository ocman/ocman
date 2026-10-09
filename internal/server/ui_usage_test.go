package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestUIUsageAPI(t *testing.T) {
	s := testServer(t)
	now := time.Now().Add(-time.Second).UnixMilli()
	body := fmt.Sprintf(`{"intervals":[{"start":%d,"end":%d}]}`, now-10_000, now)
	for range 2 {
		w := httptest.NewRecorder()
		s.handleUIUsage(w, httptest.NewRequest(http.MethodPost, "/api/ui-usage", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.handleUIUsageDays(w, httptest.NewRequest(http.MethodGet, "/api/analytics/ui-usage?days=7", nil))
	var days []state.UIUsageDay
	if err := json.Unmarshal(w.Body.Bytes(), &days); err != nil {
		t.Fatal(err)
	}
	var seconds float64
	for _, day := range days {
		seconds += day.ActiveSeconds
	}
	if w.Code != 200 || len(days) != 7 || seconds != 10 {
		t.Fatalf("days = %v, status %d", days, w.Code)
	}
	w = httptest.NewRecorder()
	s.handleUIUsageDays(w, httptest.NewRequest(http.MethodGet, "/api/analytics/ui-usage?days=0", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, input := range []string{`{`, `{"unknown":true}`, `{} {}`, `{"intervals":[{"start":1,"end":2}]}`, `{"intervals":[{"start":5,"end":1}]}`} {
		w := httptest.NewRecorder()
		s.handleUIUsage(w, httptest.NewRequest(http.MethodPost, "/api/ui-usage", strings.NewReader(input)))
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", input, w.Code)
		}
	}
	for _, days := range []string{"-1", "abc", "3651"} {
		w := httptest.NewRecorder()
		s.handleUIUsageDays(w, httptest.NewRequest(http.MethodGet, "/api/analytics/ui-usage?days="+days, nil))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

func TestUIUsageEmptyAndUnavailable(t *testing.T) {
	s := testServer(t)
	w := httptest.NewRecorder()
	s.handleUIUsageDays(w, httptest.NewRequest(http.MethodGet, "/api/analytics/ui-usage?days=0", nil))
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	s.stateDB = nil
	for _, handler := range []http.HandlerFunc{s.handleUIUsage, s.handleUIUsageDays} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	}
}

func TestAgentRunHoursAPI(t *testing.T) {
	s := testServer(t)
	w := httptest.NewRecorder()
	s.handleAgentRunHours(w, httptest.NewRequest(http.MethodGet, "/api/analytics/agent-run-hours?days=7&dir=/repo", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"minutes":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
