package analytics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// Integration tests for the `dir=` query parameter on the five Stats / Usage
// endpoints. These prove the full round-trip: handler parses + normalises +
// forwards the param, the DB layer applies the prefix predicate, and the
// JSON payload differs from the unfiltered call in the right direction.
//
// We keep the tests narrow: per endpoint, seed sessions whose directories
// exercise the AD-7 sibling-prefix trap, then assert the scoped call
// produces a strictly smaller (and correct) result than the unfiltered call.
// Detailed aggregation correctness is covered by stats_dir_filter_test.go in
// internal/db; here we're proving the handler wiring.

// dirFilterFixture seeds a fresh server with the four-session fixture
// described in stats_dir_filter_test.go. Returns the server.
func dirFilterFixture(t *testing.T) *fixture {
	t.Helper()
	srv := newFixture(t)
	rawDB := srv.raw
	now := time.Now().UnixMilli()

	mkSession := func(id, title, dir string) {
		_, err := rawDB.Exec(
			`INSERT INTO session (id, title, directory, time_created, time_updated)
			 VALUES (?, ?, ?, ?, ?)`,
			id, title, dir, now, now,
		)
		if err != nil {
			t.Fatalf("seeding session %s: %v", id, err)
		}
	}
	mkMessage := func(id, sessID string, in, out int) {
		data := fmt.Sprintf(`{
			"role": "assistant",
			"providerID": "anthropic",
			"modelID": "opus-4.1",
			"finish": "end_turn",
			"tokens": {"input": %d, "output": %d},
			"time": {"created": %d, "completed": %d}
		}`, in, out, now-1000, now)
		_, err := rawDB.Exec(
			`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
			id, sessID, now, data,
		)
		if err != nil {
			t.Fatalf("seeding message %s: %v", id, err)
		}
	}

	mkSession("s_exact", "Exact", "/repo/foo")
	mkSession("s_desc", "Descendant", "/repo/foo/sub")
	mkSession("s_sib", "Sibling", "/repo/foobar") // sibling-prefix trap
	mkSession("s_other", "Other", "/elsewhere")
	mkMessage("m_exact", "s_exact", 100, 0)
	mkMessage("m_desc", "s_desc", 200, 0)
	mkMessage("m_sib", "s_sib", 400, 0)
	mkMessage("m_other", "s_other", 800, 0)
	srv.importFixture()

	return srv
}

// TestDirFilterRoutes seeds the fixture once and checks each route. Scoped
// requests carry `dir` URL-encoded through url.Values so encoding matches
// what a real client (URLSearchParams) sends.
func TestDirFilterRoutes(t *testing.T) {
	srv := dirFilterFixture(t)

	// get serves path with query q, requires HTTP 200 and decodes into out.
	get := func(t *testing.T, path string, q url.Values, out any) {
		t.Helper()
		target := path
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %s", target, rr.Code, rr.Body.String())
		}
		if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
			t.Fatalf("%s decode: %v", target, err)
		}
	}
	scoped := url.Values{"dir": {"/repo/foo"}}

	t.Run("metrics", func(t *testing.T) {
		type metrics struct {
			Summary struct {
				Requests    int   `json:"requests"`
				InputTokens int64 `json:"inputTokens"`
			} `json:"summary"`
		}
		// Unfiltered: 4 requests, 1500 input tokens.
		var unfiltered metrics
		get(t, "/api/metrics", nil, &unfiltered)
		if unfiltered.Summary.Requests != 4 || unfiltered.Summary.InputTokens != 1500 {
			t.Fatalf("unfiltered metrics summary unexpected: %+v", unfiltered.Summary)
		}
		// Scoped to /repo/foo: 2 requests, 300 tokens. Sibling /repo/foobar
		// must NOT contribute (AD-7).
		var got metrics
		get(t, "/api/metrics", scoped, &got)
		if got.Summary.Requests != 2 {
			t.Errorf("scoped Requests = %d, want 2", got.Summary.Requests)
		}
		if got.Summary.InputTokens != 300 {
			t.Errorf("scoped InputTokens = %d, want 300 (sibling /repo/foobar should be excluded)", got.Summary.InputTokens)
		}
	})

	t.Run("metric logs select kind and dir", func(t *testing.T) {
		var got struct {
			Kind     string        `json:"kind"`
			Total    int           `json:"total"`
			Requests []interface{} `json:"requests"`
			Sessions []interface{} `json:"sessions"`
		}
		get(t, "/api/metric-logs", url.Values{"kind": {"request"}, "dir": {"/repo/foo"}}, &got)
		if got.Kind != "request" || got.Total != 2 || len(got.Requests) != 2 || got.Sessions != nil {
			t.Fatalf("unexpected metric logs: %+v", got)
		}

		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/metric-logs?kind=unknown", nil))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid kind returned HTTP %d, want 400", rr.Code)
		}
	})

	t.Run("performance omits logs", func(t *testing.T) {
		var got map[string]interface{}
		get(t, "/api/metrics/performance", nil, &got)
		if _, ok := got["summary"]; !ok {
			t.Fatal("performance response has no summary")
		}
		for _, field := range []string{"requests", "sessions", "projects"} {
			if _, ok := got[field]; ok {
				t.Fatalf("performance response unexpectedly contains %q", field)
			}
		}
	})

	t.Run("activity", func(t *testing.T) {
		var days []struct {
			Date     string `json:"date"`
			Sessions int    `json:"sessions"`
			Messages int    `json:"messages"`
		}
		get(t, "/api/activity", scoped, &days)
		var totalMsg int
		for _, d := range days {
			totalMsg += d.Messages
		}
		if totalMsg != 2 {
			t.Errorf("scoped messages total = %d, want 2", totalMsg)
		}
	})

	t.Run("models", func(t *testing.T) {
		var models []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Count    int    `json:"count"`
		}
		get(t, "/api/models", scoped, &models)
		if len(models) != 1 || models[0].Count != 2 {
			t.Errorf("scoped models = %#v, want one row with Count=2", models)
		}
	})

	t.Run("hourly", func(t *testing.T) {
		var hours []struct {
			Hour     int `json:"hour"`
			Sessions int `json:"sessions"`
		}
		get(t, "/api/hourly", scoped, &hours)
		var totalSess int
		for _, h := range hours {
			totalSess += h.Sessions
		}
		if totalSess != 2 {
			t.Errorf("scoped sessions = %d, want 2 (exact + descendant only)", totalSess)
		}
	})

	t.Run("hourly tokens", func(t *testing.T) {
		var rows []struct {
			Datetime string `json:"datetime"`
			TokensIn int64  `json:"tokensIn"`
		}
		get(t, "/api/hourly-tokens", scoped, &rows)
		var total int64
		for _, r := range rows {
			total += r.TokensIn
		}
		if total != 300 {
			t.Errorf("scoped tokensIn total = %d, want 300", total)
		}
	})
}
