package server

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
func dirFilterFixture(t *testing.T) *Server {
	t.Helper()
	srv, rawDB := testServerWithRawDB(t)
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

	return srv
}

// scopedRequest builds a GET request to `path` with `dir=value` URL-encoded
// in the query string. Uses url.Values to ensure encoding matches what a
// real client (URLSearchParams) sends.
func scopedRequest(path, dir string) *http.Request {
	q := url.Values{}
	q.Set("dir", dir)
	return httptest.NewRequest("GET", path+"?"+q.Encode(), nil)
}

func TestHandleMetrics_DirFilter(t *testing.T) {
	srv := dirFilterFixture(t)

	// Unfiltered: 4 requests, 1500 input tokens.
	rr := httptest.NewRecorder()
	srv.handleMetrics(rr, httptest.NewRequest("GET", "/api/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("unfiltered metrics: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var unfiltered struct {
		Summary struct {
			Requests    int   `json:"requests"`
			InputTokens int64 `json:"inputTokens"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &unfiltered); err != nil {
		t.Fatalf("unfiltered metrics decode: %v", err)
	}
	if unfiltered.Summary.Requests != 4 || unfiltered.Summary.InputTokens != 1500 {
		t.Fatalf("unfiltered metrics summary unexpected: %+v", unfiltered.Summary)
	}

	// Scoped to /repo/foo: 2 requests, 300 tokens. Sibling /repo/foobar
	// must NOT contribute (AD-7).
	rr = httptest.NewRecorder()
	srv.handleMetrics(rr, scopedRequest("/api/metrics", "/repo/foo"))
	if rr.Code != http.StatusOK {
		t.Fatalf("scoped metrics: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var scoped struct {
		Summary struct {
			Requests    int   `json:"requests"`
			InputTokens int64 `json:"inputTokens"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("scoped metrics decode: %v", err)
	}
	if scoped.Summary.Requests != 2 {
		t.Errorf("scoped Requests = %d, want 2", scoped.Summary.Requests)
	}
	if scoped.Summary.InputTokens != 300 {
		t.Errorf("scoped InputTokens = %d, want 300 (sibling /repo/foobar should be excluded)", scoped.Summary.InputTokens)
	}
}

func TestHandleActivity_DirFilter(t *testing.T) {
	srv := dirFilterFixture(t)

	rr := httptest.NewRecorder()
	srv.handleActivity(rr, scopedRequest("/api/activity", "/repo/foo"))
	if rr.Code != http.StatusOK {
		t.Fatalf("scoped activity: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var days []struct {
		Date     string `json:"date"`
		Sessions int    `json:"sessions"`
		Messages int    `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &days); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var totalMsg int
	for _, d := range days {
		totalMsg += d.Messages
	}
	if totalMsg != 2 {
		t.Errorf("scoped messages total = %d, want 2", totalMsg)
	}
}

func TestHandleModels_DirFilter(t *testing.T) {
	srv := dirFilterFixture(t)

	rr := httptest.NewRecorder()
	srv.handleModels(rr, scopedRequest("/api/models", "/repo/foo"))
	if rr.Code != http.StatusOK {
		t.Fatalf("scoped models: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var models []struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Count    int    `json:"count"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &models); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(models) != 1 || models[0].Count != 2 {
		t.Errorf("scoped models = %#v, want one row with Count=2", models)
	}
}

func TestHandleHourly_DirFilter(t *testing.T) {
	srv := dirFilterFixture(t)

	rr := httptest.NewRecorder()
	srv.handleHourly(rr, scopedRequest("/api/hourly", "/repo/foo"))
	if rr.Code != http.StatusOK {
		t.Fatalf("scoped hourly: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var hours []struct {
		Hour     int `json:"hour"`
		Sessions int `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &hours); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var totalSess int
	for _, h := range hours {
		totalSess += h.Sessions
	}
	if totalSess != 2 {
		t.Errorf("scoped sessions = %d, want 2 (exact + descendant only)", totalSess)
	}
}

func TestHandleHourlyTokens_DirFilter(t *testing.T) {
	srv := dirFilterFixture(t)

	rr := httptest.NewRecorder()
	srv.handleHourlyTokens(rr, scopedRequest("/api/hourly-tokens", "/repo/foo"))
	if rr.Code != http.StatusOK {
		t.Fatalf("scoped hourly-tokens: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var rows []struct {
		Datetime string `json:"datetime"`
		TokensIn int64  `json:"tokensIn"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var total int64
	for _, r := range rows {
		total += r.TokensIn
	}
	if total != 300 {
		t.Errorf("scoped tokensIn total = %d, want 300", total)
	}
}

func TestNormaliseDirParam(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"/repo/foo", "/repo/foo"},
		{"  /repo/foo  ", "/repo/foo"},
		{"/repo/foo/", "/repo/foo"},
		{"/", "/"}, // leave bare root alone — defensive
	}
	for _, tc := range cases {
		if got := normaliseDirParam(tc.in); got != tc.want {
			t.Errorf("normaliseDirParam(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
