package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderUsageComposition(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	srv := testServer(t)
	ctx := t.Context()
	if err := srv.stateDB.SetSetting(ctx, "judge_endpoint", `{"format":"typesafe","endpoint":"https://api.typesafe.ai/v1/systemone","apiKey":"private-test-token","minSafeProbability":0.99}`); err != nil {
		t.Fatal(err)
	}
	deps := srv.autoapproveEngineDeps()
	input := int64(1_000_000)
	if deps.RecordTypeSafeUsage == nil {
		t.Fatal("missing provider usage recorder")
	}
	if err := deps.RecordTypeSafeUsage(ctx, "jev-1.14.0", &input, nil); err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/provider-usage", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	rec := get()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"estimatedUSD":0.042`) || strings.Contains(rec.Body.String(), "private-test-token") {
		t.Fatalf("provider usage = %d %s", rec.Code, rec.Body.String())
	}
	// Settings are read per request, not captured when either module is built.
	if err := srv.stateDB.SetSetting(ctx, "judge_endpoint", `{"minSafeProbability":0.99}`); err != nil {
		t.Fatal(err)
	}
	if rec := get(); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"id":"typesafe"`) {
		t.Fatalf("disabled provider = %d %s", rec.Code, rec.Body.String())
	}
}
