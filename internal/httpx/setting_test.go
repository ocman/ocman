package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testSetting struct {
	N int `json:"n"`
}

func TestSettingRoute(t *testing.T) {
	stored, available := testSetting{N: 1}, true
	var getErr, setErr error
	route := SettingRoute[testSetting]{
		Name:      "test setting",
		Available: func() bool { return available },
		Get:       func(context.Context) (any, error) { return map[string]int{"got": stored.N}, getErr },
		Validate: func(v *testSetting) error {
			if v.N < 0 {
				return errors.New("n must be positive")
			}
			v.N *= 10
			return nil
		},
		Set: func(_ context.Context, v testSetting) error { stored = v; return setErr },
	}
	mux := http.NewServeMux()
	mount := func(route SettingRoute[testSetting]) {
		mux = http.NewServeMux()
		route.Mount(func(p string, h http.HandlerFunc) { mux.HandleFunc(p, h) }, "/s")
	}
	serve := func(method, body string) (int, string) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/s", strings.NewReader(body)))
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	mount(route)
	for _, tc := range []struct {
		method, body string
		code         int
		want         string
	}{
		{"GET", "", 200, `{"got":1}`},
		{"POST", `{"n":2}`, 200, `{"n":20}`},
		{"GET", "", 200, `{"got":20}`},
		{"POST", `{"n":-1}`, 400, "n must be positive"},
		{"POST", `{`, 400, "invalid JSON"},
		{"DELETE", "", 405, "Method Not Allowed"},
	} {
		if code, body := serve(tc.method, tc.body); code != tc.code || body != tc.want {
			t.Fatalf("%s %s: %d %q", tc.method, tc.body, code, body)
		}
	}

	setErr = errors.New("disk full")
	if code, _ := serve("POST", `{"n":3}`); code != 500 {
		t.Fatalf("set error: %d", code)
	}
	setErr, getErr = nil, errors.New("broken")
	if code, _ := serve("GET", ""); code != 500 {
		t.Fatalf("get error: %d", code)
	}
	getErr = nil

	route.Reread, route.MaxBody = true, 4
	mount(route)
	if code, body := serve("POST", `{"n":5}`); code != 400 || body != "invalid JSON" {
		t.Fatalf("max body: %d %q", code, body)
	}
	if code, body := serve("POST", `5`); code != 400 || body != "invalid JSON" {
		t.Fatalf("non-object: %d %q", code, body)
	}
	route.MaxBody = 0
	mount(route)
	if code, body := serve("POST", `{"n":4}`); code != 200 || body != `{"got":40}` {
		t.Fatalf("reread: %d %q", code, body)
	}

	available = false
	for _, method := range []string{"GET", "POST"} {
		if code, body := serve(method, `{"n":1}`); code != 503 || body != "state database not available" {
			t.Fatalf("%s unavailable: %d %q", method, code, body)
		}
	}
}
