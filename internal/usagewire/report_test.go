package usagewire

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeVersion(t *testing.T) {
	for input, want := range map[string]string{
		"v0.275.0": "0.275.0", "0.275.0-dev.16": "0.275.0-dev.16", "1.0.0-rc.2": "1.0.0-rc.2",
		"": "dev", "dev": "dev", "branch/private-project": "dev", "0.275.0+deadbeef": "dev",
		"v0.275.0-dirty": "dev", strings.Repeat("1", 100): "dev",
	} {
		if got := NormalizeVersion(input); got != want {
			t.Errorf("%q -> %q, want %q", input, got, want)
		}
	}
}

func TestValidReport(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	base := Report{InstallationID: "3f70f326-5593-4b78-a31f-c67a23920334", Date: "2026-11-11", Version: "0.275.0"}
	for _, tc := range []struct {
		name   string
		mutate func(*Report)
		valid  bool
	}{
		{"today", func(*Report) {}, true},
		{"yesterday", func(r *Report) { r.Date = "2026-11-10" }, true},
		{"future", func(r *Report) { r.Date = "2026-11-12" }, false},
		{"stale", func(r *Report) { r.Date = "2026-11-09" }, false},
		{"invalid date", func(r *Report) { r.Date = "2026-02-30" }, false},
		{"bad id", func(r *Report) { r.InstallationID = "../account-name" }, false},
		{"nonrandom id", func(r *Report) { r.InstallationID = "3f70f326-5593-1b78-a31f-c67a23920334" }, false},
		{"uppercase", func(r *Report) { r.InstallationID = strings.ToUpper(r.InstallationID) }, false},
		{"custom version", func(r *Report) { r.Version = "work/private-repo" }, false},
		{"dev", func(r *Report) { r.Version = "dev" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := base
			tc.mutate(&report)
			if got := report.Valid(now); got != tc.valid {
				t.Fatalf("valid=%v", got)
			}
		})
	}
}

func TestStrictJSON(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"date":"2026-11-11"}`, 200}, {`{"prompt":"do not forward"}`, 400}, {`{}`, 200},
		{`{} {}`, 400}, {`{`, 400}, {``, 400}, {strings.Repeat(" ", 1025) + `{}`, 413},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		var report Report
		valid := ReadJSON(w, r, &report)
		if w.Code != tc.code || valid != (tc.code == 200) {
			t.Fatalf("%q: %d %v", tc.body, w.Code, valid)
		}
	}
}
